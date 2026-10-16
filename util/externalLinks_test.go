package util

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// vmess payloads come in every base64 form, and the panel's own are standard
// base64 -- whose '/' url.Parse took for the start of a path, cutting the
// payload short. A non-ASCII remark is enough to put one there (#1281).
func TestVmessLinkPayloadForms(t *testing.T) {
	const payload = `{"v":"2","ps":"香港节点🇭🇰","add":"1.2.3.4","port":"443","id":"b831381d-6324-4d53-ad4f-8cda48b30811","net":"ws","path":"/a?ed=2048"}`
	std := base64.StdEncoding.EncodeToString([]byte(payload))
	if !strings.Contains(std, "/") {
		t.Fatalf("test payload encodes without a '/', so it proves nothing: %s", std)
	}

	cases := map[string]string{
		"standard with a slash": std,
		"unpadded":              base64.RawStdEncoding.EncodeToString([]byte(payload)),
		"url-safe unpadded":     base64.RawURLEncoding.EncodeToString([]byte(payload)),
		"remark appended":       std + "#extra",
		"query appended":        std + "?remarks=x",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			out, tag, err := GetOutbound("vmess://"+body, 0)
			if err != nil {
				t.Fatalf("GetOutbound: %v", err)
			}
			o := *out
			if tag != "香港节点🇭🇰" || o["server"] != "1.2.3.4" || o["uuid"] != "b831381d-6324-4d53-ad4f-8cda48b30811" {
				t.Errorf("tag %q, got %v", tag, o)
			}
		})
	}
}

// Every shape of ss link url.Parse finds no userinfo in, and the ones it reads
// already, which must come through unchanged (#1281).
func TestShadowsocksLinkForms(t *testing.T) {
	// Three '?' in a row put one at a position that encodes to '/' in the
	// standard alphabet, wherever the run starts.
	const method, password = "aes-256-gcm", "pw???x"
	userInfo := method + ":" + password
	if !strings.Contains(base64.StdEncoding.EncodeToString([]byte(userInfo)), "/") {
		t.Fatal("test userinfo encodes without a '/', so it proves nothing")
	}
	const v6 = "[240e:1:2::1234]:22851"
	legacyStd := base64.StdEncoding.EncodeToString([]byte(userInfo + "@" + v6))
	if !strings.Contains(legacyStd, "/") {
		t.Fatal("test legacy blob encodes without a '/', so it proves nothing")
	}

	cases := map[string]string{
		"sip002 base64url":            base64.RawURLEncoding.EncodeToString([]byte(userInfo)) + "@" + v6,
		"sip002 base64url with query": base64.RawURLEncoding.EncodeToString([]byte(userInfo)) + "@" + v6 + "/?plugin=none",
		"sip002 standard with slash":  base64.StdEncoding.EncodeToString([]byte(userInfo)) + "@" + v6,
		"legacy standard with slash":  legacyStd,
		"legacy trailing slash":       legacyStd + "/",
		"legacy url-safe unpadded":    base64.RawURLEncoding.EncodeToString([]byte(userInfo + "@" + v6)),
		"legacy with plugin query":    base64.RawURLEncoding.EncodeToString([]byte(userInfo+"@"+v6)) + "/?plugin=none",
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			out, tag, err := GetOutbound("ss://"+body+"#node", 0)
			if err != nil {
				t.Fatalf("GetOutbound: %v", err)
			}
			o := *out
			if tag != "node" || o["method"] != method || o["password"] != password ||
				o["server"] != "240e:1:2::1234" || o["server_port"] != 22851 {
				t.Errorf("tag %q, got %v", tag, o)
			}
		})
	}

	// A 2022 key is standard base64, and SIP002 wants it percent-encoded in a
	// plain userinfo; some generators leave its '/' as it is, which url.Parse
	// reads as an invalid port.
	const key = "abc/def+ghijklmnopqrstuvwxyz0123456789ABCDE="
	out, _, err := GetOutbound("ss://2022-blake3-aes-256-gcm:"+key+"@1.2.3.4:443#n", 0)
	if err != nil {
		t.Fatalf("plain 2022 userinfo with a slash: %v", err)
	}
	if o := *out; o["method"] != "2022-blake3-aes-256-gcm" || o["password"] != key || o["server"] != "1.2.3.4" {
		t.Errorf("plain 2022 userinfo with a slash: got %v", o)
	}

	for _, bad := range []string{
		"ss://" + base64.StdEncoding.EncodeToString([]byte("no-at-sign:1.2.3.4:443")),
		"ss://" + base64.StdEncoding.EncodeToString([]byte("nocolon@1.2.3.4:443")),
		"ss://1.2.3.4:443",
	} {
		if _, _, err := GetOutbound(bad, 0); err == nil {
			t.Errorf("%q: want it refused", bad)
		}
	}
}

// Providers serve unpadded and URL-safe subscription bodies, and CRLF line
// ends: standard-only decoding read the first as one line of base64, and the
// '\r' made url.Parse refuse every line of the second (#1281).
func TestGetExternalSubBodyForms(t *testing.T) {
	links := "trojan://pw@e.com:443#one\r\nvless://11111111-1111-1111-1111-111111111111@e.com:443#two\r\n"
	bodies := map[string]string{
		"plain crlf":        links,
		"url-safe unpadded": base64.RawURLEncoding.EncodeToString([]byte(links)),
		"standard padded":   base64.StdEncoding.EncodeToString([]byte(links)) + "\n",
		"json with newline": `{"outbounds":[{"type":"trojan","tag":"one"},{"type":"vless","tag":"two"}]}` + "\n",
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()

			got, err := GetExternalSub(srv.URL)
			if err != nil {
				t.Fatalf("GetExternalSub: %v", err)
			}
			if len(got) != 2 || got[0]["tag"] != "one" || got[1]["tag"] != "two" {
				t.Errorf("got %v, want the two nodes", got)
			}
		})
	}
}
