package util

// DirectHTTPClient returns the inline http_client a remote rule-set downloads
// with when it should dial the server directly, through no outbound at all.
//
// sing-box 1.14 has no spelling for "direct" as such. An http_client dials
// through its detour, and with no detour it dials the system network itself
// (dialer.NewWithOptions -> NewDefault) -- which is exactly direct. But an
// inline client with nothing in it counts as absent (HTTPClientOptions.IsEmpty),
// and an absent client falls back to the default one: the implicit client
// riding the default outbound, deprecated in 1.14 and removed in 1.16. A detour
// to an optionless direct outbound is no way out either, since sing-box refuses
// it as pointless (see IsPlainDirectOutbound).
//
// So the client has to say something without saying anything different.
// engine "go" is the documented default and takes the same branch as an unset
// engine (httpclient.NewTransport: case "", C.TLSEngineGo), so it changes
// nothing about how the download is made while keeping the object non-empty.
// plugins/httpClient.ts DIRECT_HTTP_CLIENT writes the same value.
func DirectHTTPClient() map[string]any {
	return map[string]any{"engine": "go"}
}

// IsEmptyHTTPClient reports whether a decoded http_client value is one sing-box
// treats as not set, so the rule-set carrying it uses the default client. That
// is null, an empty tag, or an inline object every field of which is its zero
// value -- the same test as HTTPClientOptions.IsEmpty, which compares the whole
// struct against zero rather than looking for any particular key.
func IsEmptyHTTPClient(value any) bool {
	return isZeroJSON(value)
}

func isZeroJSON(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case bool:
		return !v
	case string:
		return v == ""
	case float64:
		return v == 0
	case []any:
		return len(v) == 0
	case map[string]any:
		for _, field := range v {
			if !isZeroJSON(field) {
				return false
			}
		}
		return true
	}
	return false
}

// HasDefaultHTTPClient reports whether a config names a default HTTP client for
// remote rule-sets that carry none: route.default_http_client, or else the
// first entry of http_clients, which sing-box takes as the default when nothing
// is named (httpclient.NewManager).
func HasDefaultHTTPClient(root map[string]any) bool {
	if clients, _ := root["http_clients"].([]any); len(clients) > 0 {
		return true
	}
	route, _ := root["route"].(map[string]any)
	tag, _ := route["default_http_client"].(string)
	return tag != ""
}

// ImplicitHTTPClientRuleSets lists the remote rule-sets of a decoded config that
// would download through sing-box's implicit default HTTP client -- the one
// riding the default outbound, deprecated in 1.14 and removed in 1.16. A
// rule-set falls back to it when it names no client of its own (and no legacy
// download_detour) and the config declares no default client.
func ImplicitHTTPClientRuleSets(root map[string]any) []string {
	if HasDefaultHTTPClient(root) {
		return nil
	}
	var tags []string
	for _, ruleSet := range remoteRuleSets(root) {
		if !IsEmptyHTTPClient(ruleSet["http_client"]) {
			continue
		}
		if detour, _ := ruleSet["download_detour"].(string); detour != "" {
			continue
		}
		tag, _ := ruleSet["tag"].(string)
		tags = append(tags, tag)
	}
	return tags
}

// LegacyDownloadDetourRuleSets lists the remote rule-sets of a decoded config
// that still carry download_detour, deprecated in 1.14 in favour of http_client
// and removed in 1.16.
func LegacyDownloadDetourRuleSets(root map[string]any) []string {
	var tags []string
	for _, ruleSet := range remoteRuleSets(root) {
		if _, has := ruleSet["download_detour"]; !has {
			continue
		}
		tag, _ := ruleSet["tag"].(string)
		tags = append(tags, tag)
	}
	return tags
}

func remoteRuleSets(root map[string]any) []map[string]any {
	route, _ := root["route"].(map[string]any)
	entries, _ := route["rule_set"].([]any)
	var remote []map[string]any
	for _, entry := range entries {
		ruleSet, isObject := entry.(map[string]any)
		if !isObject {
			continue
		}
		if ruleSetType, _ := ruleSet["type"].(string); ruleSetType != "remote" {
			continue
		}
		remote = append(remote, ruleSet)
	}
	return remote
}
