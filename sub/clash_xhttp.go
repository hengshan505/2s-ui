package sub

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

// xhttpOptsFromTransport converts Xray VLESS URI parameters into Mihomo's
// xhttp-opts. Omitted and null values retain Mihomo defaults.
func xhttpOptsFromTransport(transport map[string]interface{}) map[string]interface{} {
	opts := make(map[string]interface{})
	for _, name := range []string{"host", "path", "mode"} {
		if s, ok := transport[name].(string); ok && s != "" {
			opts[name] = s
		}
	}
	if extra, ok := transport["extra"].(map[string]interface{}); ok {
		// An explicitly supplied URI parameter wins even when it is empty.
		for _, name := range []string{"host", "path", "mode"} {
			if _, hasTopLevel := transport[name]; !hasTopLevel {
				xhttpPutString(opts, name, extra[name])
			}
		}
		xhttpMapExtra(opts, extra)
	}
	xhttpKeepLoadable(opts)
	return opts
}

// xhttpKeepLoadable drops what mihomo would refuse while building the proxy.
// mihomo fails the whole profile on one proxy it cannot build, so a value it
// rejects here would take every other node in the subscription down with it,
// the panel's own included; without the value the node still loads, on
// mihomo's default for it. Only construction-time checks matter: a value
// mihomo rejects per request costs that one node, not the profile.
func xhttpKeepLoadable(opts map[string]interface{}) {
	// The modes mihomo implements (transport/xhttp NewClient); "auto" is what
	// an omitted mode means. Matched exactly, as mihomo and Xray both do.
	switch mode, _ := opts["mode"].(string); mode {
	case "", "auto", "packet-up", "stream-up", "stream-one":
	default:
		delete(opts, "mode")
	}
	// mihomo refuses download-settings in stream-one mode outright, and parses
	// its REALITY key while building the proxy. A bad key cannot be dropped on
	// its own: without reality-opts the download side inherits the uplink's.
	if download, ok := opts["download-settings"].(map[string]interface{}); ok {
		reality, _ := download["reality-opts"].(map[string]interface{})
		if opts["mode"] == "stream-one" || !xhttpRealityUsable(reality) {
			delete(opts, "download-settings")
		}
	}
	if table, ok := opts["session-table"].(string); ok && !xhttpSessionTableUsable(table, opts["session-length"]) {
		delete(opts, "session-table")
	}
}

// xhttpRealityUsable mirrors mihomo's RealityOptions.Parse: no public key
// means no REALITY at all; otherwise the key is 32 bytes of unpadded base64url
// and the short ID at most 8 bytes of hex.
func xhttpRealityUsable(reality map[string]interface{}) bool {
	publicKey, _ := reality["public-key"].(string)
	if publicKey == "" {
		return true
	}
	if key, err := base64.RawURLEncoding.DecodeString(publicKey); err != nil || len(key) != 32 {
		return false
	}
	shortID, _ := reality["short-id"].(string)
	if id, err := hex.DecodeString(shortID); err != nil || len(id) > 8 {
		return false
	}
	return true
}

// xhttpPredefinedTables are the session-table names mihomo expands.
var xhttpPredefinedTables = map[string]int{
	"ALPHABET": 26, "Alphabet": 52, "BASE36": 36, "Base62": 62, "HEX": 16,
	"alphabet": 26, "base36": 36, "hex": 16, "number": 10,
}

// xhttpSessionTableUsable mirrors mihomo's GetGenerateSessionID: a custom
// table must be ASCII and, at the session length (default 16-32), leave at
// least 2^31 possible IDs. "uuid" is a format of its own and always passes.
func xhttpSessionTableUsable(table string, rawLength interface{}) bool {
	if table == "uuid" {
		return true
	}
	size, predefined := xhttpPredefinedTables[table]
	if !predefined {
		for i := 0; i < len(table); i++ {
			if table[i] >= 0x80 {
				return false
			}
		}
		size = len(table)
	}
	length, _ := rawLength.(string)
	if length == "" {
		length = "16-32"
	}
	// session-length has been through xhttpPutRange, so it is one or two
	// positive int32s.
	parts := strings.SplitN(length, "-", 2)
	minimum, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return false
	}
	maximum := minimum
	if len(parts) == 2 {
		if maximum, err = strconv.ParseInt(parts[1], 10, 64); err != nil {
			return false
		}
	}
	// Sum of size^k over the lengths, stopping as soon as it is large enough:
	// with size >= 2 that is within 31 terms, and with size <= 1 the sum is at
	// most the number of lengths, which an int32 range keeps under 2^31.
	if size <= 1 {
		return int64(size)*(maximum-minimum+1) >= 1<<31
	}
	const enough = 1 << 31
	var room uint64
	for k := minimum; k <= maximum; k++ {
		term := uint64(1)
		for i := int64(0); i < k && term < enough; i++ {
			term *= uint64(size)
		}
		if room += term; room >= enough {
			return true
		}
	}
	return false
}

func xhttpPutString(dst map[string]interface{}, key string, raw interface{}) {
	if val, ok := raw.(string); ok && val != "" {
		dst[key] = val
	}
}

func xhttpPutBool(dst map[string]interface{}, key string, raw interface{}) {
	if val, ok := raw.(bool); ok {
		dst[key] = val
	}
}

func xhttpInteger(raw interface{}) (int64, bool) {
	var value string
	switch n := raw.(type) {
	case string:
		value = strings.TrimSpace(n)
	case json.Number:
		value = string(n)
	case float64:
		value = strconv.FormatFloat(n, 'f', -1, 64)
	case int:
		return int64(n), true
	case int64:
		return n, true
	default:
		return 0, false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil
}

// Mihomo's ranges are strings, including scalar values. Match Xray's int32
// bounds so malformed large ranges cannot overflow the client's random sampler.
func xhttpPutRange(dst map[string]interface{}, key string, raw interface{}, positive bool) {
	value, ok := raw.(string)
	if !ok {
		if number, valid := xhttpInteger(raw); valid {
			value = strconv.FormatInt(number, 10)
		} else {
			return
		}
	}
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) < 1 || len(parts) > 2 {
		return
	}
	minimum, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil || minimum < 0 || minimum > 2147483647 || (positive && minimum == 0) {
		return
	}
	maximum := minimum
	if len(parts) == 2 {
		maximum, err = strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil || maximum < minimum || maximum > 2147483647 {
			return
		}
	}
	value = strconv.FormatInt(minimum, 10)
	if len(parts) == 2 {
		value += "-" + strconv.FormatInt(maximum, 10)
	}
	dst[key] = value
}

func xhttpPutHeaders(dst map[string]interface{}, raw interface{}) {
	headers, ok := raw.(map[string]interface{})
	if !ok {
		return
	}
	valid := make(map[string]interface{})
	for key, rawValue := range headers {
		if value, ok := rawValue.(string); ok {
			valid[key] = value
		}
	}
	if len(valid) > 0 {
		dst["headers"] = valid
	}
}

// xhttpMapReuse is the Xray xmux -> Mihomo reuse-settings mapping.
func xhttpMapReuse(input map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	for from, to := range map[string]string{
		"maxConcurrency":   "max-concurrency",
		"maxConnections":   "max-connections",
		"cMaxReuseTimes":   "c-max-reuse-times",
		"hMaxRequestTimes": "h-max-request-times",
		"hMaxReusableSecs": "h-max-reusable-secs",
	} {
		xhttpPutRange(out, to, input[from], false)
	}
	if value, ok := xhttpInteger(input["hKeepAlivePeriod"]); ok {
		out["h-keep-alive-period"] = value
	}
	return out
}

func xhttpMapExtra(opts map[string]interface{}, extra map[string]interface{}) {
	xhttpPutHeaders(opts, extra["headers"])
	for from, to := range map[string]string{
		"xPaddingKey":         "x-padding-key",
		"xPaddingHeader":      "x-padding-header",
		"xPaddingPlacement":   "x-padding-placement",
		"xPaddingMethod":      "x-padding-method",
		"uplinkHTTPMethod":    "uplink-http-method",
		"sessionIDPlacement":  "session-placement",
		"sessionIDKey":        "session-key",
		"sessionIDTable":      "session-table",
		"seqPlacement":        "seq-placement",
		"seqKey":              "seq-key",
		"uplinkDataPlacement": "uplink-data-placement",
		"uplinkDataKey":       "uplink-data-key",
	} {
		xhttpPutString(opts, to, extra[from])
	}
	for from, to := range map[string]string{
		"noGRPCHeader":     "no-grpc-header",
		"xPaddingObfsMode": "x-padding-obfs-mode",
	} {
		xhttpPutBool(opts, to, extra[from])
	}
	// Xray fills these when obfuscation is enabled; Mihomo leaves them empty.
	if enabled, _ := opts["x-padding-obfs-mode"].(bool); enabled {
		for key, value := range map[string]string{
			"x-padding-key":       "x_padding",
			"x-padding-header":    "X-Padding",
			"x-padding-placement": "queryInHeader",
			"x-padding-method":    "repeat-x",
		} {
			if _, exists := opts[key]; !exists {
				opts[key] = value
			}
		}
	}
	xhttpPutRange(opts, "x-padding-bytes", extra["xPaddingBytes"], true)
	// Xray-generated zeroes mean defaults. Mihomo rejects explicit sc-* zeroes;
	// omission preserves its defaults of 1000000 bytes and 30 milliseconds.
	for from, to := range map[string]string{
		"scMaxEachPostBytes":   "sc-max-each-post-bytes",
		"scMinPostsIntervalMs": "sc-min-posts-interval-ms",
		"sessionIDLength":      "session-length",
		"uplinkChunkSize":      "uplink-chunk-size",
	} {
		xhttpPutRange(opts, to, extra[from], true)
	}
	if xmux, ok := extra["xmux"].(map[string]interface{}); ok && len(xmux) > 0 {
		if mapped := xhttpMapReuse(xmux); len(mapped) > 0 {
			opts["reuse-settings"] = mapped
		}
	}
	// downloadSettings is an Xray StreamConfig, not a flat XHTTP settings map.
	if download, ok := extra["downloadSettings"].(map[string]interface{}); ok && len(download) > 0 {
		if mapped := xhttpMapDownload(download); len(mapped) > 0 {
			opts["download-settings"] = mapped
		}
	}
}

func xhttpMapDownload(download map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{})
	xhttpPutString(out, "server", download["address"])
	if port, ok := xhttpInteger(download["port"]); ok && port > 0 && port <= 65535 {
		out["port"] = port
	}
	settingsName := ""
	if security, ok := download["security"].(string); ok {
		switch security {
		case "tls":
			out["tls"] = true
			settingsName = "tlsSettings"
		case "reality":
			out["tls"] = true
			settingsName = "realitySettings"
		case "", "none":
			out["tls"] = false
		}
	}
	if _, selected := out["tls"]; selected {
		// Mihomo inherits omitted download TLS fields from the uplink. Xray
		// selects an independent security mode, with verification on by default.
		out["skip-cert-verify"] = false
		out["reality-opts"] = map[string]interface{}{}
	}
	// Xray may retain settings for inactive security modes. Export only the
	// selected mode so stale REALITY settings cannot enable it for a TLS link.
	if settings, ok := download[settingsName].(map[string]interface{}); ok && settingsName != "" {
		xhttpPutString(out, "servername", settings["serverName"])
		xhttpPutString(out, "client-fingerprint", settings["fingerprint"])
		xhttpPutBool(out, "skip-cert-verify", settings["allowInsecure"])
		if rawALPN, ok := settings["alpn"].([]interface{}); ok {
			alpn := make([]string, 0, len(rawALPN))
			for _, value := range rawALPN {
				if protocol, ok := value.(string); ok && protocol != "" {
					alpn = append(alpn, protocol)
				}
			}
			if len(alpn) > 0 {
				out["alpn"] = alpn
			}
		}
		if settingsName == "realitySettings" {
			reality := make(map[string]interface{})
			xhttpPutString(reality, "public-key", settings["publicKey"])
			xhttpPutString(reality, "short-id", settings["shortId"])
			if len(reality) > 0 {
				out["reality-opts"] = reality
			}
		}
	}
	if settings, ok := download["xhttpSettings"].(map[string]interface{}); ok {
		for _, name := range []string{"host", "path"} {
			xhttpPutString(out, name, settings[name])
		}
		xhttpPutHeaders(out, settings["headers"])
		if extra, ok := settings["extra"].(map[string]interface{}); ok {
			if xmux, ok := extra["xmux"].(map[string]interface{}); ok {
				if reuse := xhttpMapReuse(xmux); len(reuse) > 0 {
					out["reuse-settings"] = reuse
				}
			}
		}
	}
	return out
}
