package database

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/shenaba/2s-ui/database/model"
)

func seedOutbound(t *testing.T, outboundType, tag, options string) {
	t.Helper()
	if err := db.Create(&model.Outbound{Type: outboundType, Tag: tag, Options: json.RawMessage(options)}).Error; err != nil {
		t.Fatal(err)
	}
}

func seedConfig(t *testing.T, config string) {
	t.Helper()
	if err := db.Create(&model.Setting{Key: "config", Value: config}).Error; err != nil {
		t.Fatal(err)
	}
}

func clientOf(t *testing.T, sets []map[string]any, tag string) any {
	t.Helper()
	for _, set := range sets {
		if set["tag"] == tag {
			if _, legacy := set["download_detour"]; legacy {
				t.Errorf("%s: download_detour left behind: %v", tag, set)
			}
			return set["http_client"]
		}
	}
	t.Fatalf("rule-set %q not found in %v", tag, sets)
	return nil
}

var directClient = map[string]any{"engine": "go"}

func TestMigrateExplicitHTTPClientsConvertsDownloadDetour(t *testing.T) {
	openTestDB(t)
	seedOutbound(t, "direct", "direct", `{}`)
	seedOutbound(t, "direct", "eth1", `{"bind_interface":"eth1"}`)
	seedOutbound(t, "vless", "proxy", `{"server":"e.com"}`)
	seedConfig(t, `{
		"route": {
			"final": "proxy",
			"rule_set": [
				{"type": "remote", "tag": "plain", "url": "https://e.com/a.srs", "download_detour": "direct"},
				{"type": "remote", "tag": "bound", "url": "https://e.com/b.srs", "download_detour": "eth1"},
				{"type": "remote", "tag": "proxied", "url": "https://e.com/c.srs", "download_detour": "proxy"},
				{"type": "remote", "tag": "both", "url": "https://e.com/d.srs", "download_detour": "proxy", "http_client": "shared"},
				{"type": "local", "tag": "local", "path": "/x.srs"}
			]
		}
	}`)

	if err := migrateExplicitRuleSetHTTPClients(); err != nil {
		t.Fatal(err)
	}
	sets := ruleSets(t, readConfig(t))
	// The legacy field skipped the empty-direct check, so this one really
	// downloaded directly; a detour cannot say that.
	if got := clientOf(t, sets, "plain"); !reflect.DeepEqual(got, directClient) {
		t.Errorf("plain direct: got %v", got)
	}
	// A direct outbound with options of its own is a routing choice, kept.
	if got := clientOf(t, sets, "bound"); !reflect.DeepEqual(got, map[string]any{"detour": "eth1"}) {
		t.Errorf("bound direct: got %v", got)
	}
	if got := clientOf(t, sets, "proxied"); !reflect.DeepEqual(got, map[string]any{"detour": "proxy"}) {
		t.Errorf("proxied: got %v", got)
	}
	if got := clientOf(t, sets, "both"); got != "shared" {
		t.Errorf("a set client wins over the legacy field: got %v", got)
	}
	if _, has := sets[4]["http_client"]; has {
		t.Errorf("a local rule-set downloads nothing: %v", sets[4])
	}
}

func TestMigrateExplicitHTTPClientsKeepsTheCurrentPath(t *testing.T) {
	cases := []struct {
		name  string
		final string
		seed  func(t *testing.T)
		want  any
	}{
		{
			name:  "route.final",
			final: `"final": "proxy",`,
			seed: func(t *testing.T) {
				seedOutbound(t, "direct", "direct", `{}`)
				seedOutbound(t, "vless", "proxy", `{"server":"e.com"}`)
			},
			want: map[string]any{"detour": "proxy"},
		},
		{
			name:  "route.final on a plain direct outbound",
			final: `"final": "direct",`,
			seed: func(t *testing.T) {
				seedOutbound(t, "vless", "proxy", `{"server":"e.com"}`)
				seedOutbound(t, "direct", "direct", `{}`)
			},
			want: directClient,
		},
		{
			name:  "no route.final takes the first outbound",
			final: ``,
			seed: func(t *testing.T) {
				seedOutbound(t, "vless", "proxy", `{"server":"e.com"}`)
				seedOutbound(t, "direct", "direct", `{}`)
			},
			want: map[string]any{"detour": "proxy"},
		},
		{
			name:  "no route.final, first outbound plain direct",
			final: ``,
			seed: func(t *testing.T) {
				seedOutbound(t, "direct", "direct", `{}`)
				seedOutbound(t, "vless", "proxy", `{"server":"e.com"}`)
			},
			want: directClient,
		},
		{
			name:  "route.final on an endpoint",
			final: `"final": "warp",`,
			seed: func(t *testing.T) {
				seedOutbound(t, "direct", "direct", `{}`)
				if err := db.Create(&model.Endpoint{Type: "wireguard", Tag: "warp", Options: json.RawMessage(`{}`)}).Error; err != nil {
					t.Fatal(err)
				}
			},
			want: map[string]any{"detour": "warp"},
		},
		{
			name:  "route.final naming nothing is left for the operator",
			final: `"final": "gone",`,
			seed: func(t *testing.T) {
				seedOutbound(t, "direct", "direct", `{}`)
			},
			want: nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			openTestDB(t)
			tc.seed(t)
			seedConfig(t, `{
				"route": {
					`+tc.final+`
					"rule_set": [
						{"type": "remote", "tag": "none", "url": "https://e.com/a.srs"},
						{"type": "remote", "tag": "empty", "url": "https://e.com/b.srs", "http_client": {"detour": ""}}
					]
				}
			}`)
			if err := migrateExplicitRuleSetHTTPClients(); err != nil {
				t.Fatal(err)
			}
			sets := ruleSets(t, readConfig(t))
			for _, tag := range []string{"none", "empty"} {
				got := clientOf(t, sets, tag)
				if tc.want == nil {
					if got != nil && !reflect.DeepEqual(got, map[string]any{"detour": ""}) {
						t.Errorf("%s: expected it left alone, got %v", tag, got)
					}
					continue
				}
				if !reflect.DeepEqual(got, tc.want) {
					t.Errorf("%s: got %v, want %v", tag, got, tc.want)
				}
			}
		})
	}
}

func TestMigrateExplicitHTTPClientsLeavesADeclaredDefault(t *testing.T) {
	for name, config := range map[string]string{
		"http_clients": `{"http_clients": [{"tag": "dl", "detour": "proxy"}],
		  "route": {"rule_set": [{"type": "remote", "tag": "none", "url": "https://e.com/a.srs"}]}}`,
		"default_http_client": `{"route": {"default_http_client": "dl",
		  "rule_set": [{"type": "remote", "tag": "none", "url": "https://e.com/a.srs"}]}}`,
	} {
		t.Run(name, func(t *testing.T) {
			openTestDB(t)
			seedOutbound(t, "vless", "proxy", `{"server":"e.com"}`)
			seedConfig(t, config)
			if err := migrateExplicitRuleSetHTTPClients(); err != nil {
				t.Fatal(err)
			}
			// Falling back to a declared default client is not the deprecated
			// implicit one, so the rule-set keeps following it.
			if got := clientOf(t, ruleSets(t, readConfig(t)), "none"); got != nil {
				t.Errorf("expected no client, got %v", got)
			}
		})
	}
}

func TestMigrateExplicitHTTPClientsRunsOnce(t *testing.T) {
	openTestDB(t)
	seedOutbound(t, "vless", "proxy", `{"server":"e.com"}`)
	seedConfig(t, `{"route": {"rule_set": [{"type": "remote", "tag": "a", "url": "https://e.com/a.srs"}]}}`)
	if err := migrateExplicitRuleSetHTTPClients(); err != nil {
		t.Fatal(err)
	}
	// Anything written after the first run is the save path's business.
	if err := db.Model(&model.Setting{}).Where("key = ?", "config").
		Update("value", `{"route": {"rule_set": [{"type": "remote", "tag": "b", "url": "https://e.com/b.srs"}]}}`).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateExplicitRuleSetHTTPClients(); err != nil {
		t.Fatal(err)
	}
	if got := clientOf(t, ruleSets(t, readConfig(t)), "b"); got != nil {
		t.Errorf("second run rewrote the config: %v", got)
	}
}
