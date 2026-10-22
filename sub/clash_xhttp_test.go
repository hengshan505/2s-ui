package sub

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/shenaba/2s-ui/util"

	"gopkg.in/yaml.v3"
)

// XHTTP links may carry populated, zero-valued, or null extra settings.
func fourXHTTPLinks(t *testing.T) ([]string, json.RawMessage) {
	t.Helper()
	fullExtra := `{
		"host":"","path":"","mode":"","headers":null,
		"xPaddingBytes":"100-1000","xPaddingObfsMode":false,
		"xPaddingKey":"","xPaddingHeader":"","xPaddingPlacement":"","xPaddingMethod":"",
		"uplinkHTTPMethod":"","sessionIDPlacement":"","sessionIDKey":"","sessionIDTable":"","sessionIDLength":"0",
		"seqPlacement":"","seqKey":"","uplinkDataPlacement":"","uplinkDataKey":"","uplinkChunkSize":"0",
		"noGRPCHeader":false,"noSSEHeader":false,"scMaxEachPostBytes":"1000000","scMinPostsIntervalMs":"30",
		"scMaxBufferedPosts":0,"scStreamUpServerSecs":"0","serverMaxHeaderBytes":0,
		"xmux":{"maxConcurrency":"16-32","maxConnections":"0","cMaxReuseTimes":"64-128","hMaxRequestTimes":"0","hMaxReusableSecs":"0","hKeepAlivePeriod":0},
		"downloadSettings":null,"extra":null
	}`
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(fullExtra), &extra); err != nil {
		t.Fatal(err)
	}
	extra["scMaxEachPostBytes"] = "0"
	extra["scMinPostsIntervalMs"] = "0"
	zeroExtra, err := json.Marshal(extra)
	if err != nil {
		t.Fatal(err)
	}
	extras := []string{fullExtra, string(zeroExtra), "null", fullExtra}
	var uris []string
	var links []Link
	for index, rawExtra := range extras {
		host := fmt.Sprintf("test-%d.example", index+1)
		query := url.Values{
			"type": {"xhttp"}, "host": {host}, "path": {"/api/v1/upload"}, "mode": {"auto"},
			"security": {"tls"}, "sni": {host}, "fp": {"firefox"}, "alpn": {"h2,http/1.1"},
			"encryption": {"none"}, "extra": {rawExtra},
		}
		uri := fmt.Sprintf("vless://00000000-0000-4000-8000-00000000000%d@192.0.2.%d:2053?%s#XHTTP", index+1, index+1, query.Encode())
		uris = append(uris, uri)
		links = append(links, Link{Type: "external", Uri: uri})
	}
	raw, err := json.Marshal(links)
	if err != nil {
		t.Fatal(err)
	}
	return uris, json.RawMessage(raw)
}

func TestExternalXHTTPClashSubscription(t *testing.T) {
	setupSubDB(t) // ConvertToClashMeta reads the subClash* settings
	service := &ClashService{}
	uris, raw := fourXHTTPLinks(t)
	originalJSON := append([]byte(nil), raw...)
	if got := service.LinkService.GetLinks(&raw, "all", ""); !reflect.DeepEqual(got, uris) {
		t.Fatal("Happ/raw subscription changed the source URI")
	}
	ordinaryBefore, ordinaryTags := service.LinkService.GetExternalOutbounds(&raw)
	outbounds, tags := service.LinkService.GetExternalOutboundsForClash(&raw)
	// GetClash settles duplicate names after merging every source.
	uniqueOutboundTags(outbounds, tags)
	if len(outbounds) != 4 || !reflect.DeepEqual(tags, []string{"XHTTP", "XHTTP-2", "XHTTP-3", "XHTTP-4"}) {
		t.Fatalf("four external nodes or unique names missing: %v", tags)
	}
	result, err := service.ConvertToClashMeta(&outbounds, "mode: rule\nrules:\n  - MATCH,Proxy\n")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Proxies []map[string]interface{} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(result), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Proxies) != 4 {
		t.Fatalf("wrong proxy count: %d", len(document.Proxies))
	}
	for index, proxy := range document.Proxies {
		host := fmt.Sprintf("test-%d.example", index+1)
		if proxy["name"] != tags[index] || proxy["type"] != "vless" || proxy["network"] != "xhttp" || proxy["server"] != fmt.Sprintf("192.0.2.%d", index+1) || proxy["port"] != 2053 {
			t.Fatalf("endpoint/name/transport lost for node %d: %v", index, proxy)
		}
		if proxy["uuid"] != fmt.Sprintf("00000000-0000-4000-8000-00000000000%d", index+1) || proxy["tls"] != true || proxy["servername"] != host || proxy["client-fingerprint"] != "firefox" {
			t.Fatalf("identity or TLS lost for node %d: %v", index, proxy)
		}
		if !reflect.DeepEqual(proxy["alpn"], []interface{}{"h2", "http/1.1"}) {
			t.Fatalf("ALPN lost for node %d: %v", index, proxy["alpn"])
		}
		if insecure, _ := proxy["skip-cert-verify"].(bool); insecure {
			t.Fatal("certificate verification disabled without URI permission")
		}
		opts := proxy["xhttp-opts"].(map[string]interface{})
		if opts["host"] != host || opts["path"] != "/api/v1/upload" || opts["mode"] != "auto" {
			t.Fatalf("top-level XHTTP options lost for node %d: %v", index, opts)
		}
		if index == 2 {
			if len(opts) != 3 {
				t.Fatalf("null extra produced unexpected fields: %v", opts)
			}
			continue
		}
		if opts["x-padding-bytes"] != "100-1000" {
			t.Fatalf("padding range lost: %v", opts)
		}
		reuse := opts["reuse-settings"].(map[string]interface{})
		if reuse["max-concurrency"] != "16-32" || reuse["max-connections"] != "0" || reuse["c-max-reuse-times"] != "64-128" {
			t.Fatalf("XMUX settings lost: %v", reuse)
		}
		for _, key := range []string{"session-length", "uplink-chunk-size", "download-settings", "no-sse-header", "sc-max-buffered-posts", "sc-stream-up-server-secs", "server-max-header-bytes"} {
			if _, exists := opts[key]; exists {
				t.Errorf("default-zero/server-only option must be omitted: %s", key)
			}
		}
		if index == 1 {
			for _, key := range []string{"sc-max-each-post-bytes", "sc-min-posts-interval-ms"} {
				if _, exists := opts[key]; exists {
					t.Fatalf("Mihomo rejects explicit zero sc option: %s=%v", key, opts[key])
				}
			}
		} else if opts["sc-max-each-post-bytes"] != "1000000" || opts["sc-min-posts-interval-ms"] != "30" {
			t.Fatalf("sc parameters must remain string ranges: %v", opts)
		}
	}
	ordinaryAfter, afterTags := service.LinkService.GetExternalOutbounds(&raw)
	if !reflect.DeepEqual(ordinaryBefore, ordinaryAfter) || !reflect.DeepEqual(ordinaryTags, afterTags) || !reflect.DeepEqual([]byte(raw), originalJSON) {
		t.Fatal("Clash export mutated raw links or ordinary sing-box JSON generation")
	}
	for _, outbound := range ordinaryAfter {
		if len(outbound["transport"].(map[string]interface{})) != 0 {
			t.Fatal("Clash-only transport escaped into ordinary JSON generation")
		}
	}
}

func TestXHTTPOptionsPrecedenceAndPacketUp(t *testing.T) {
	extra := map[string]interface{}{"host": "extra.example", "path": "/extra", "mode": "auto"}
	got := xhttpOptsFromTransport(map[string]interface{}{
		"host": "uri.example", "path": "/uri", "mode": "packet-up", "extra": extra,
	})
	want := map[string]interface{}{"host": "uri.example", "path": "/uri", "mode": "packet-up"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("URI settings lost precedence: %v", got)
	}
	got = xhttpOptsFromTransport(map[string]interface{}{"host": "", "extra": extra})
	if _, exists := got["host"]; exists || got["path"] != "/extra" || got["mode"] != "auto" {
		t.Fatalf("explicit empty and absent fields conflated: %v", got)
	}
}

func TestXHTTPRangeMapping(t *testing.T) {
	for _, test := range []struct {
		input interface{}
		want  string
	}{
		{"100-1000", "100-1000"}, {" 10 - 20 ", "10-20"}, {json.Number("30"), "30"}, {float64(30), "30"},
		{"0", ""}, {"0-10", ""}, {"10-1", ""}, {"-1", ""}, {"1-2-3", ""}, {"1.5", ""},
		{"2147483647", "2147483647"}, {"2147483648", ""}, {"1-2147483648", ""},
		{float64(1.5), ""}, {true, ""}, {nil, ""}, {"9223372036854775808", ""},
	} {
		got := make(map[string]interface{})
		xhttpPutRange(got, "range", test.input, true)
		if test.want == "" {
			if len(got) != 0 {
				t.Errorf("invalid/zero range %v emitted as %v", test.input, got)
			}
		} else if got["range"] != test.want {
			t.Errorf("range %v: got %v want %q", test.input, got, test.want)
		}
	}
	reuse := xhttpMapReuse(map[string]interface{}{"maxConcurrency": "0", "maxConnections": json.Number("4"), "hKeepAlivePeriod": float64(-1)})
	if reuse["max-concurrency"] != "0" || reuse["max-connections"] != "4" || reuse["h-keep-alive-period"] != int64(-1) {
		t.Fatalf("zero concurrency erased connection limit or signed keepalive: %v", reuse)
	}
}

func TestXHTTPExtraMappingAndMalformedValues(t *testing.T) {
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(`{
		"headers":{"User-Agent":"test","Bad":42},"noGRPCHeader":false,"xPaddingObfsMode":true,
		"xPaddingKey":"custom","xPaddingHeader":"","sessionIDLength":"16-32","uplinkChunkSize":"2000-3000",
		"scMaxEachPostBytes":"1000000-2000000","scMinPostsIntervalMs":"30-60",
		"xmux":{"maxConcurrency":4,"maxConnections":0,"hKeepAlivePeriod":1.5},
		"downloadSettings":"invalid","unknown":"ignored"
	}`), &extra); err != nil {
		t.Fatal(err)
	}
	opts := xhttpOptsFromTransport(map[string]interface{}{"extra": extra})
	for key, want := range map[string]interface{}{
		"no-grpc-header": false, "x-padding-obfs-mode": true, "x-padding-key": "custom", "x-padding-header": "X-Padding",
		"x-padding-placement": "queryInHeader", "x-padding-method": "repeat-x", "session-length": "16-32",
		"uplink-chunk-size": "2000-3000", "sc-max-each-post-bytes": "1000000-2000000", "sc-min-posts-interval-ms": "30-60",
	} {
		if opts[key] != want {
			t.Errorf("%s: got %v want %v", key, opts[key], want)
		}
	}
	if !reflect.DeepEqual(opts["headers"], map[string]interface{}{"User-Agent": "test"}) {
		t.Errorf("malformed header not filtered: %v", opts["headers"])
	}
	if _, exists := opts["download-settings"]; exists {
		t.Fatal("malformed optional downloadSettings emitted")
	}
	if _, exists := opts["unknown"]; exists {
		t.Fatal("unknown Xray extra leaked into Mihomo")
	}
	for _, malformed := range []interface{}{nil, "bad", []interface{}{}, float64(1), true} {
		got := xhttpOptsFromTransport(map[string]interface{}{"host": "example.com", "extra": malformed})
		if !reflect.DeepEqual(got, map[string]interface{}{"host": "example.com"}) {
			t.Fatalf("malformed extra affected base transport: %v", got)
		}
	}
}

func TestXHTTPDownloadStreamConfig(t *testing.T) {
	var extra map[string]interface{}
	if err := json.Unmarshal([]byte(`{"downloadSettings":{
		"address":"download.example","port":8443,"security":"tls",
		"tlsSettings":{"serverName":"download-sni.example","fingerprint":"firefox","alpn":["h2"],"allowInsecure":false},
		"xhttpSettings":{"host":"download-host.example","path":"/download","headers":{"X-Test":"yes"},
			"extra":{"xmux":{"maxConcurrency":"8-16","maxConnections":"0"}}}
	}}`), &extra); err != nil {
		t.Fatal(err)
	}
	opts := xhttpOptsFromTransport(map[string]interface{}{"extra": extra})
	download := opts["download-settings"].(map[string]interface{})
	for key, want := range map[string]interface{}{
		"server": "download.example", "port": int64(8443), "tls": true, "servername": "download-sni.example",
		"client-fingerprint": "firefox", "skip-cert-verify": false, "host": "download-host.example", "path": "/download",
	} {
		if download[key] != want {
			t.Errorf("download %s: got %v want %v", key, download[key], want)
		}
	}
	if !reflect.DeepEqual(download["alpn"], []string{"h2"}) || !reflect.DeepEqual(download["headers"], map[string]interface{}{"X-Test": "yes"}) {
		t.Fatalf("download TLS/HTTP fields lost: %v", download)
	}
	reuse := download["reuse-settings"].(map[string]interface{})
	if reuse["max-concurrency"] != "8-16" {
		t.Fatalf("nested download XMUX lost: %v", reuse)
	}
	if got := xhttpMapDownload(map[string]interface{}{"security": "none"}); !reflect.DeepEqual(got, map[string]interface{}{
		"tls": false, "skip-cert-verify": false, "reality-opts": map[string]interface{}{},
	}) {
		t.Fatalf("explicit plaintext download override lost: %v", got)
	}
	if got := xhttpMapDownload(map[string]interface{}{"port": float64(65536), "tlsSettings": "bad", "xhttpSettings": []interface{}{}}); len(got) != 0 {
		t.Fatalf("malformed download fields emitted: %v", got)
	}
}

func TestXHTTPDownloadUsesSelectedSecurity(t *testing.T) {
	for _, test := range []struct {
		security    string
		wantTLS     bool
		wantSNI     string
		wantReality bool
	}{
		{"tls", true, "tls.example", false},
		{"reality", true, "reality.example", true},
		{"none", false, "", false},
		{"", false, "", false},
	} {
		t.Run(test.security, func(t *testing.T) {
			got := xhttpMapDownload(map[string]interface{}{
				"security":        test.security,
				"tlsSettings":     map[string]interface{}{"serverName": "tls.example", "allowInsecure": true},
				"realitySettings": map[string]interface{}{"serverName": "reality.example", "publicKey": "test-key"},
			})
			if got["tls"] != test.wantTLS {
				t.Fatalf("wrong TLS selection: %v", got)
			}
			sni, _ := got["servername"].(string)
			if sni != test.wantSNI {
				t.Errorf("inactive security mode overwrote SNI: %v", got)
			}
			reality, exists := got["reality-opts"].(map[string]interface{})
			if !exists || (len(reality) > 0) != test.wantReality {
				t.Errorf("inactive or inherited REALITY settings changed selected security: %v", got)
			}
			if got["skip-cert-verify"] != (test.security == "tls") {
				t.Errorf("inactive TLS settings changed certificate verification: %v", got)
			}
		})
	}
	got := xhttpMapDownload(map[string]interface{}{"security": "tls", "tlsSettings": map[string]interface{}{"serverName": "verified.example"}})
	if got["skip-cert-verify"] != false || !reflect.DeepEqual(got["reality-opts"], map[string]interface{}{}) {
		t.Fatalf("TLS download may inherit insecure or REALITY uplink settings: %v", got)
	}
	reuse := xhttpMapReuse(map[string]interface{}{"maxConcurrency": "0-9223372036854775807", "maxConnections": "4"})
	if _, exists := reuse["max-concurrency"]; exists || reuse["max-connections"] != "4" {
		t.Fatalf("overflowing range emitted or valid independent range lost: %v", reuse)
	}
}

func TestExternalNonXHTTPAndLocalLinksUnchanged(t *testing.T) {
	links := []Link{
		{Type: "external", Uri: "vless://test@192.0.2.1:443?type=grpc&security=reality&serviceName=test&pbk=public&sid=1234#grpc"},
		{Type: "external", Uri: "vless://test@192.0.2.1:443?type=ws&security=tls&host=example.com&path=%2Fws#ws"},
		{Type: "external", Uri: "hy2://test@192.0.2.1:443?sni=example.com#hy2"},
		{Type: "external", Uri: "tuic://00000000-0000-4000-8000-000000000001:test@192.0.2.1:443?sni=example.com#tuic"},
		{Type: "local", Uri: "vless://local@192.0.2.1:443?type=ws#local"},
	}
	data, err := json.Marshal(links)
	if err != nil {
		t.Fatal(err)
	}
	raw := json.RawMessage(data)
	service := &LinkService{}
	want, wantTags := service.GetExternalOutbounds(&raw)
	got, gotTags := service.GetExternalOutboundsForClash(&raw)
	if len(got) != 4 || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(gotTags, wantTags) {
		t.Fatalf("other external transports/local handling changed: %v", got)
	}
	if gotLinks := service.GetLinks(&raw, "all", ""); len(gotLinks) != 5 || gotLinks[4] != links[4].Uri {
		t.Fatal("local/raw link generation changed")
	}
}

func externalSubTestLinks(t *testing.T, links []Link) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(links)
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(raw)
}

func externalSubTestServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func externalSubTestProxies(t *testing.T, service *ClashService, outbounds []map[string]interface{}) []map[string]interface{} {
	t.Helper()
	result, err := service.ConvertToClashMeta(&outbounds, "mode: rule\nrules:\n  - MATCH,Proxy\n")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Proxies []map[string]interface{} `yaml:"proxies"`
	}
	if err := yaml.Unmarshal([]byte(result), &document); err != nil {
		t.Fatal(err)
	}
	return document.Proxies
}

func TestExternalSubsXHTTPClashPlaintextAndBase64(t *testing.T) {
	setupSubDB(t) // ConvertToClashMeta reads the subClash* settings
	service := &ClashService{}
	uris, _ := fourXHTTPLinks(t)
	// The alias and an unrelated transport exercise the same fetched URI list.
	uris = append(uris, strings.Replace(uris[2], "type=xhttp", "type=splithttp", 1))
	uris = append(uris, "vless://00000000-0000-4000-8000-000000000099@192.0.2.99:443?type=ws&security=tls&sni=ws.example&host=ws.example&path=%2Fsocket#WebSocket")
	var direct []Link
	for _, uri := range uris {
		direct = append(direct, Link{Type: "external", Uri: uri})
	}
	directRaw := externalSubTestLinks(t, direct)
	wantClash, wantTags := service.GetExternalOutboundsForClash(&directRaw)
	wantOrdinary, wantOrdinaryTags := service.GetExternalOutbounds(&directRaw)
	wantProxies := externalSubTestProxies(t, service, wantClash)
	if len(wantProxies) != 6 {
		t.Fatalf("direct reference lost nodes: %d", len(wantProxies))
	}

	for _, format := range []struct {
		name   string
		base64 bool
	}{
		{name: "plaintext"},
		{name: "base64", base64: true},
	} {
		t.Run(format.name, func(t *testing.T) {
			// Blank lines, CRLF, surrounding whitespace and malformed URIs must
			// neither lose valid nodes nor cause a nil dereference.
			decoded := "\r\n  " + strings.Join(uris, "  \r\n\r\n  ") + "  \r\nunsupported://node\r\nvless://%zz\r\n\r\n"
			body := decoded
			if format.base64 {
				body = base64.StdEncoding.EncodeToString([]byte(decoded))
			}
			server := externalSubTestServer(t, body)
			raw := externalSubTestLinks(t, []Link{{Type: "sub", Uri: server.URL}})
			originalRaw := append([]byte(nil), raw...)

			ordinaryBefore, ordinaryTags := service.GetExternalOutbounds(&raw)
			if !reflect.DeepEqual(ordinaryBefore, wantOrdinary) || !reflect.DeepEqual(ordinaryTags, wantOrdinaryTags) {
				t.Fatalf("ordinary sing-box export changed: %v", ordinaryTags)
			}

			outbounds, tags := service.GetExternalOutboundsForClash(&raw)
			if !reflect.DeepEqual(outbounds, wantClash) || !reflect.DeepEqual(tags, wantTags) {
				t.Fatalf("External Subs differs from direct External Links: tags=%v", tags)
			}
			proxies := externalSubTestProxies(t, service, outbounds)
			if !reflect.DeepEqual(proxies, wantProxies) {
				t.Fatal("fetched URI list produced different Clash proxies from direct links")
			}
			first := proxies[0]
			if first["network"] != "xhttp" || first["tls"] != true || first["servername"] != "test-1.example" || first["client-fingerprint"] != "firefox" || !reflect.DeepEqual(first["alpn"], []interface{}{"h2", "http/1.1"}) {
				t.Fatalf("XHTTP or TLS/ALPN missing: %v", first)
			}
			opts, ok := first["xhttp-opts"].(map[string]interface{})
			if !ok || opts["host"] != "test-1.example" || opts["path"] != "/api/v1/upload" || opts["mode"] != "auto" || opts["x-padding-bytes"] != "100-1000" || opts["sc-max-each-post-bytes"] != "1000000" || opts["sc-min-posts-interval-ms"] != "30" {
				t.Fatalf("XHTTP Extra fields missing: %v", opts)
			}
			reuse, ok := opts["reuse-settings"].(map[string]interface{})
			if !ok || reuse["max-concurrency"] != "16-32" || reuse["max-connections"] != "0" || reuse["c-max-reuse-times"] != "64-128" {
				t.Fatalf("XMUX fields missing: %v", reuse)
			}
			if proxies[4]["network"] != "xhttp" || proxies[5]["network"] != "ws" {
				t.Fatalf("alias or WebSocket lost: %v / %v", proxies[4], proxies[5])
			}
			ordinaryAfter, afterTags := service.GetExternalOutbounds(&raw)
			if !reflect.DeepEqual(ordinaryAfter, ordinaryBefore) || !reflect.DeepEqual(afterTags, ordinaryTags) || !reflect.DeepEqual([]byte(raw), originalRaw) {
				t.Fatal("Clash export mutated the source or ordinary sing-box export")
			}
		})
	}
}

func TestExternalSubsSingBoxJSONUnchanged(t *testing.T) {
	const body = `{"outbounds":[
		{"type":"direct","tag":"direct"},
		{"type":"selector","tag":"select","outbounds":["node"]},
		{"type":"vless","tag":"node","server":"192.0.2.30","server_port":443,"uuid":"00000000-0000-4000-8000-000000000030",
		 "tls":{"enabled":true,"server_name":"json.example","alpn":["h2"]},
		 "transport":{"type":"ws","path":"/json","headers":{"Host":"json.example"}},"custom_field":{"keep":true}}
	]}`
	var document struct {
		Outbounds []map[string]interface{} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	want := document.Outbounds[2:]
	for _, encoded := range []bool{false, true} {
		response := body
		if encoded {
			response = base64.StdEncoding.EncodeToString([]byte(body))
		}
		server := externalSubTestServer(t, response)
		for _, mode := range []struct {
			name  string
			parse func(string) ([]map[string]interface{}, error)
		}{
			{name: "ordinary", parse: util.GetExternalSub},
			{name: "clash", parse: util.GetExternalSubForClash},
		} {
			got, err := mode.parse(server.URL)
			if err != nil || !reflect.DeepEqual(got, want) {
				t.Fatalf("sing-box JSON changed (base64=%v, export=%s): got=%v err=%v", encoded, mode.name, got, err)
			}
		}
		raw := externalSubTestLinks(t, []Link{{Type: "sub", Uri: server.URL}})
		var service LinkService
		ordinary, ordinaryTags := service.GetExternalOutbounds(&raw)
		clash, clashTags := service.GetExternalOutboundsForClash(&raw)
		if !reflect.DeepEqual(ordinary, want) || !reflect.DeepEqual(clash, want) || !reflect.DeepEqual(ordinaryTags, []string{"node"}) || !reflect.DeepEqual(clashTags, ordinaryTags) {
			t.Fatal("JSON input changed through LinkService")
		}
	}
}

func TestExternalSubsEmptyAndInvalidURILists(t *testing.T) {
	for _, body := range []string{"\r\n \t\r\n", "unsupported://node\r\nvless://%zz\r\n"} {
		server := externalSubTestServer(t, body)
		for _, parse := range []func(string) ([]map[string]interface{}, error){
			util.GetExternalSub,
			util.GetExternalSubForClash,
		} {
			outbounds, err := parse(server.URL)
			if err == nil || len(outbounds) != 0 {
				t.Fatalf("empty/invalid list should yield no result: %v, %v", outbounds, err)
			}
		}
	}
}

// GetClash itself has to take external links through the Clash parser: the
// tests above call LinkService and ConvertToClashMeta directly, so they would
// all still pass with GetClash back on GetExternalOutbounds -- and every XHTTP
// node in a served subscription a dead TCP one again.
func TestGetClashKeepsExternalXHTTP(t *testing.T) {
	setupSubDB(t)
	id := seedSS(t, "ss-in", 20000, "")
	link := "vless://00000000-0000-4000-8000-000000000001@192.0.2.1:2053?" + url.Values{
		"type": {"xhttp"}, "host": {"cdn.example"}, "path": {"/up"}, "mode": {"packet-up"},
		"security": {"tls"}, "sni": {"cdn.example"},
	}.Encode() + "#xhttp-node"
	links, err := json.Marshal([]Link{{Remark: "x", Type: "external", Uri: link}})
	if err != nil {
		t.Fatal(err)
	}
	seedClientOn(t, "subxhttp", "", string(links), id)

	clash, _, err := (&ClashService{}).GetClash("subxhttp")
	if err != nil {
		t.Fatalf("GetClash: %v", err)
	}
	proxy := clashProxy(t, *clash, "xhttp-node")
	if proxy["network"] != "xhttp" {
		t.Fatalf("network = %v, want xhttp:\n%s", proxy["network"], *clash)
	}
	opts, _ := proxy["xhttp-opts"].(map[string]interface{})
	if opts["host"] != "cdn.example" || opts["path"] != "/up" || opts["mode"] != "packet-up" {
		t.Errorf("xhttp-opts = %v, want host cdn.example, path /up, mode packet-up", opts)
	}
	// The JSON subscription of the same client is untouched.
	raw, _, err := (&JsonService{}).GetJson("subxhttp", "json")
	if err != nil {
		t.Fatalf("GetJson: %v", err)
	}
	node := outboundsByTag(t, *raw)["xhttp-node"]
	if node == nil {
		t.Fatalf("the JSON subscription lost the external node:\n%s", *raw)
	}
	if tr, _ := node["transport"].(map[string]interface{}); len(tr) != 0 {
		t.Errorf("the sing-box JSON carries a transport sing-box has no form of: %v", tr)
	}
}

// mihomo fails the whole profile on one proxy it cannot build, so each value
// it refuses while building has to be dropped rather than passed on.
func TestXHTTPDropsWhatMihomoRefusesToBuild(t *testing.T) {
	for _, test := range []struct {
		name string
		uri  map[string]interface{}
		keep string
	}{
		{"auto", map[string]interface{}{"mode": "auto"}, "auto"},
		{"stream-one", map[string]interface{}{"mode": "stream-one"}, "stream-one"},
		{"case differs", map[string]interface{}{"mode": "Auto"}, ""},
		{"unknown", map[string]interface{}{"mode": "stream-two"}, ""},
		{"unknown in extra", map[string]interface{}{"extra": map[string]interface{}{"mode": "bogus"}}, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, _ := xhttpOptsFromTransport(test.uri)["mode"].(string)
			if got != test.keep {
				t.Errorf("mode = %q, want %q", got, test.keep)
			}
		})
	}

	download := map[string]interface{}{"address": "dl.example", "security": "tls"}
	for mode, want := range map[string]bool{"stream-one": false, "stream-up": true, "packet-up": true, "": true} {
		opts := xhttpOptsFromTransport(map[string]interface{}{
			"mode": mode, "extra": map[string]interface{}{"downloadSettings": download},
		})
		if _, got := opts["download-settings"]; got != want {
			t.Errorf("mode %q: download-settings present = %v, want %v", mode, got, want)
		}
	}
}

// A download REALITY key mihomo cannot parse takes the whole download-settings
// with it: dropping reality-opts alone would hand the download side the
// uplink's REALITY instead.
func TestXHTTPDownloadDroppedOnBadReality(t *testing.T) {
	const key = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY" // 32 bytes, base64url
	for _, test := range []struct {
		name, publicKey, shortID string
		keep                     bool
	}{
		{"valid", key, "0123abcd", true},
		{"valid, no short id", key, "", true},
		{"no key is no REALITY", "", "zz", true},
		{"not base64url", "test-key!", "", false},
		{"wrong length", "dGVzdC1rZXk", "", false},
		{"padded", key + "=", "", false},
		{"short id not hex", key, "xyz1", false},
		{"short id odd length", key, "abc", false},
		{"short id too long", key, "0123456789abcdef01", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := xhttpOptsFromTransport(map[string]interface{}{"extra": map[string]interface{}{
				"downloadSettings": map[string]interface{}{
					"address": "dl.example", "security": "reality",
					"realitySettings": map[string]interface{}{"publicKey": test.publicKey, "shortId": test.shortID},
				},
			}})
			if _, got := opts["download-settings"]; got != test.keep {
				t.Errorf("download-settings present = %v, want %v: %v", got, test.keep, opts)
			}
		})
	}
}

func TestXHTTPSessionTableKeptOnlyWhenMihomoTakesIt(t *testing.T) {
	for _, test := range []struct {
		table, length string
		keep          bool
	}{
		{"uuid", "", true},
		{"hex", "", true},        // 16^16.. is plenty
		{"number", "4-8", false}, // ~1.1e8 IDs < 2^31
		{"number", "10", true},   // exactly 1e10
		{"ab", "4-8", false},
		{"ab", "31", true}, // 2^31 exactly
		{"ab", "30", false},
		{"a", "1-2147483647", false}, // one symbol: never enough, and must not loop 2^31 times
		{"0123456789abcdef", "1-2147483647", true},
		{"абв", "", false}, // not ASCII
	} {
		extra := map[string]interface{}{"sessionIDTable": test.table}
		if test.length != "" {
			extra["sessionIDLength"] = test.length
		}
		opts := xhttpOptsFromTransport(map[string]interface{}{"extra": extra})
		if _, got := opts["session-table"]; got != test.keep {
			t.Errorf("table %q length %q: kept = %v, want %v", test.table, test.length, got, test.keep)
		}
	}
}
