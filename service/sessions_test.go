package service

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/shenaba/2s-ui/core"
	"github.com/shenaba/2s-ui/database"
	"github.com/shenaba/2s-ui/database/model"
)

// installSessionsDB installs a fresh global DB, which the node and inbound
// lookups here read through database.GetDB(). CloseDBForTest is registered after
// TempDir's cleanup so it runs first: Windows will not delete an open file.
func installSessionsDB(t *testing.T) {
	t.Helper()
	if err := database.InitDB(filepath.Join(t.TempDir(), "sessions.db")); err != nil {
		t.Fatalf("init db: %v", err)
	}
	t.Cleanup(func() {
		if err := database.CloseDBForTest(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
}

// Only Shadowsocks with multiplex on keeps a carrier the core cannot reach;
// the copied protocols cut theirs, and a node replica is not on this core.
func TestMuxCarriers(t *testing.T) {
	installSessionsDB(t)
	db := database.GetDB()
	nodeID := uint(1)
	for _, in := range []model.Inbound{
		{Type: "shadowsocks", Tag: "ss-mux", Options: json.RawMessage(`{"multiplex":{"enabled":true}}`)},
		{Type: "shadowsocks", Tag: "ss-mux-off", Options: json.RawMessage(`{"multiplex":{"enabled":false}}`)},
		{Type: "shadowsocks", Tag: "ss-plain", Options: json.RawMessage(`{"method":"aes-128-gcm"}`)},
		{Type: "vless", Tag: "vless-mux", Options: json.RawMessage(`{"multiplex":{"enabled":true}}`)},
		{Type: "shadowsocks", Tag: "ss-replica", NodeId: &nodeID, Options: json.RawMessage(`{"multiplex":{"enabled":true}}`)},
	} {
		if err := db.Create(&in).Error; err != nil {
			t.Fatalf("create %s: %v", in.Tag, err)
		}
	}

	all := []string{"ss-mux", "ss-mux-off", "ss-plain", "vless-mux", "ss-replica", "missing"}
	if got := muxCarriers(all); !reflect.DeepEqual(got, []string{"ss-mux"}) {
		t.Errorf("muxCarriers = %v, want [ss-mux]", got)
	}
	if got := muxCarriers(nil); got != nil {
		t.Errorf("muxCarriers(nil) = %v", got)
	}
}

// A node's inbounds are labelled with the node. One from before per-inbound
// reports sends only a count, built from the seen set alone (#234); it is
// dropped rather than shown as a warning nobody could tell from a false one.
func TestNodeUnclosable(t *testing.T) {
	current := core.DisconnectResult{}
	current.AddUnclosable("hy2", core.UnclosableQUIC)
	current.AddUnclosable("ss", core.UnclosableMux)

	want := []core.UnclosableInbound{
		{Tag: "hy2", Reason: core.UnclosableQUIC, Node: "n1"},
		{Tag: "ss", Reason: core.UnclosableMux, Node: "n1"},
	}
	if got := nodeUnclosable("n1", current); !reflect.DeepEqual(got, want) {
		t.Errorf("current node: nodeUnclosable = %+v, want %+v", got, want)
	}
	if current.UnclosableInbounds[0].Node != "" {
		t.Error("nodeUnclosable labelled the node's own slice in place")
	}
	if got := nodeUnclosable("n1", core.DisconnectResult{Unclosable: 2}); len(got) != 0 {
		t.Errorf("older node: nodeUnclosable = %+v, want nothing", got)
	}
	if got := nodeUnclosable("n1", core.DisconnectResult{Connections: 4}); len(got) != 0 {
		t.Errorf("nothing left: nodeUnclosable = %+v, want nothing", got)
	}
}

// Nodes answer in any order; the list does not.
func TestSortUnclosable(t *testing.T) {
	list := []core.UnclosableInbound{
		{Tag: "tuic", Node: "b"},
		{Tag: "ss", Node: ""},
		{Tag: "ss", Node: "a"},
		{Tag: "hy2", Node: "b"},
		{Tag: "hy2", Node: ""},
	}
	sortUnclosable(list)
	want := []core.UnclosableInbound{
		{Tag: "hy2", Node: ""},
		{Tag: "ss", Node: ""},
		{Tag: "ss", Node: "a"},
		{Tag: "hy2", Node: "b"},
		{Tag: "tuic", Node: "b"},
	}
	if !reflect.DeepEqual(list, want) {
		t.Errorf("sorted = %+v\nwant     %+v", list, want)
	}
}

// A kick goes to every online node, not only the ones whose online list names
// the client: that list drops a client after 10s without traffic.
func TestOnlineNodes(t *testing.T) {
	installSessionsDB(t)
	db := database.GetDB()
	nodes := map[string]*model.Node{}
	for _, name := range []string{"up", "stopped", "down", "disabled"} {
		n := &model.Node{Name: name}
		if err := db.Create(n).Error; err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		nodes[name] = n
	}
	// Enable defaults to true in the schema, so a false on Create is dropped.
	if err := db.Model(nodes["disabled"]).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}

	nodeStatusMu.Lock()
	saved := nodeStatuses
	nodeStatuses = map[uint]NodeStatus{
		// Online with nobody in its list: still asked.
		nodes["up"].Id:       {State: "online"},
		nodes["stopped"].Id:  {State: "core-stopped"},
		nodes["down"].Id:     {State: "offline"},
		nodes["disabled"].Id: {State: "online"},
	}
	nodeStatusMu.Unlock()
	t.Cleanup(func() {
		nodeStatusMu.Lock()
		nodeStatuses = saved
		nodeStatusMu.Unlock()
	})

	got, err := onlineNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "up" {
		names := []string{}
		for _, n := range got {
			names = append(names, n.Name)
		}
		t.Errorf("onlineNodes = %v, want [up]", names)
	}
}

// With no core here and no node to ask, nothing was disconnected anywhere, and
// a success with zero counts would say otherwise.
func TestDisconnectClusterUserNothingToAsk(t *testing.T) {
	installSessionsDB(t)
	nodeStatusMu.Lock()
	saved := nodeStatuses
	nodeStatuses = map[uint]NodeStatus{}
	nodeStatusMu.Unlock()
	t.Cleanup(func() {
		nodeStatusMu.Lock()
		nodeStatuses = saved
		nodeStatusMu.Unlock()
	})

	if _, err := DisconnectClusterUser("alice"); err == nil {
		t.Error("no core and no nodes: want an error")
	}
}

// The resource is checked with or without a tag: a misspelt one and no tag must
// not fall through to "every user's connections".
func TestLocalSessionsResource(t *testing.T) {
	for _, tc := range []struct {
		resource, tag string
		ok            bool
	}{
		{"", "", true},
		{"user", "", true},
		{"user", "alice", true},
		{"inbound", "vless-in", true},
		{"outbound", "direct", true},
		{"users", "", false},
		{"users", "alice", false},
		{"", "alice", false},
	} {
		_, err := LocalSessions(tc.resource, tc.tag)
		if (err == nil) != tc.ok {
			t.Errorf("LocalSessions(%q, %q) err = %v, want ok=%v", tc.resource, tc.tag, err, tc.ok)
		}
	}
}

func TestEachNode(t *testing.T) {
	nodes := []*model.Node{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	var merged []string
	errs := eachNode(nodes,
		func(n *model.Node) (string, error) {
			if n.Name == "b" {
				return "", errors.New("unreachable")
			}
			return n.Name, nil
		},
		// No lock here on purpose: eachNode promises merge runs under its own.
		func(_ *model.Node, v string) { merged = append(merged, v) })

	sort.Strings(merged)
	if !reflect.DeepEqual(merged, []string{"a", "c"}) {
		t.Errorf("merged = %v, want [a c]", merged)
	}
	if !reflect.DeepEqual(errs, map[string]string{"b": "unreachable"}) {
		t.Errorf("errs = %v", errs)
	}

	// All answered: nil, so the omitempty Errors fields stay out of the JSON.
	if errs := eachNode(nodes[:1],
		func(n *model.Node) (int, error) { return 1, nil },
		func(*model.Node, int) {}); errs != nil {
		t.Errorf("errs = %v, want nil", errs)
	}
}
