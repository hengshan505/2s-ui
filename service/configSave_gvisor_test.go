//go:build with_gvisor

package service

import (
	"strconv"
	"strings"
	"testing"

	"github.com/shenaba/2s-ui/database"
	"github.com/shenaba/2s-ui/database/model"
)

const testWireguard = `{"type":"wireguard","tag":"wg","address":["10.9.0.1/32"],"private_key":"yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk=","peers":[]}`

// A TLS edit reloads the endpoints using it in place -- except one a group
// depends on, which cannot be swapped under the group. Refusing it would have
// refused the certificate change; it asks for a restart after the commit
// instead.
func TestATLSReloadRestartsForAnEndpointAGroupUses(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "endpoints", "new", testWireguard); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}
	var id uint
	if err := database.GetDB().Model(model.Endpoint{}).Select("id").Where("tag = ?", "wg").Scan(&id).Error; err != nil {
		t.Fatalf("find endpoint: %v", err)
	}

	before, _ := corePtr.GetInstance().Endpoint().Get("wg")
	restart, err := s.EndpointService.RestartEndpoints(database.GetDB(), []uint{id})
	if err != nil || restart {
		t.Fatalf("RestartEndpoints with nothing depending = (%v, %v), want an in-place reload", restart, err)
	}
	if now, _ := corePtr.GetInstance().Endpoint().Get("wg"); now == before {
		t.Error("the endpoint was not reloaded")
	}

	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["wg"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	restart, err = s.EndpointService.RestartEndpoints(database.GetDB(), []uint{id})
	if err != nil || !restart {
		t.Fatalf("RestartEndpoints for an endpoint in a group = (%v, %v), want a restart asked for", restart, err)
	}
}

// An endpoint a group uses is edited like such an outbound: committed, then
// applied by a restart.
func TestEditingAnEndpointAGroupUsesRestartsTheCore(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "endpoints", "new", testWireguard); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["wg"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Endpoint{}, "wg")), 10)
	before := corePtr.GetInstance()

	edited := strings.Replace(testWireguard, `"10.9.0.1/32"`, `"10.9.0.2/32"`, 1)
	edited = strings.Replace(edited, `{`, `{"id":`+id+`,`, 1)
	if err := save(t, s, "endpoints", "edit", edited); err != nil {
		t.Fatalf("editing a selector's endpoint = %v, want it saved", err)
	}
	waitForRestart(t, before)
}

// The TLS path through the whole save: an edit reaching an endpoint a group
// uses restarts the core once committed.
func TestATLSEditRestartsTheCoreForAnEndpointAGroupUses(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "endpoints", "new", testWireguard); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["wg"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	if err := save(t, s, "tls", "new", `{"name":"t","server":{},"client":{}}`); err != nil {
		t.Fatalf("save tls: %v", err)
	}
	var tlsId uint
	if err := database.GetDB().Model(model.Tls{}).Select("id").Where("name = ?", "t").Scan(&tlsId).Error; err != nil || tlsId == 0 {
		t.Fatalf("find tls: %d, %v", tlsId, err)
	}
	// A wireguard endpoint takes nothing from a TLS config, which is what lets
	// it stand in here for the endpoint types that do.
	if err := database.GetDB().Model(model.Endpoint{}).Where("tag = ?", "wg").Update("tls_id", tlsId).Error; err != nil {
		t.Fatalf("attach tls: %v", err)
	}
	before := corePtr.GetInstance()

	if err := save(t, s, "tls", "edit", `{"id":`+strconv.FormatUint(uint64(tlsId), 10)+`,"name":"t2","server":{},"client":{}}`); err != nil {
		t.Fatalf("edit tls: %v", err)
	}
	waitForRestart(t, before)
}

// An edit to an endpoint a group uses is vetted like an outbound's: a detour
// that does not exist is refused before the commit.
func TestAnEndpointEditTheCoreCannotStartIsRefused(t *testing.T) {
	s := runningPanel(t)
	if err := save(t, s, "endpoints", "new", testWireguard); err != nil {
		t.Fatalf("save endpoint: %v", err)
	}
	if err := save(t, s, "outbounds", "new", `{"type":"selector","tag":"sel","outbounds":["wg"]}`); err != nil {
		t.Fatalf("save selector: %v", err)
	}
	id := strconv.FormatUint(uint64(rowId(t, model.Endpoint{}, "wg")), 10)

	edited := strings.Replace(testWireguard, `"peers":[]`, `"peers":[],"detour":"gone"`, 1)
	edited = strings.Replace(edited, `{`, `{"id":`+id+`,`, 1)
	if err := save(t, s, "endpoints", "edit", edited); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("an edit with a missing detour = %v, want it refused", err)
	}
}

// A node replica sits in the inbounds table but runs on its node, so a local
// endpoint may share its tag: they collide with nothing in this core.
func TestAnEndpointMayShareATagWithANodeReplica(t *testing.T) {
	s := runningPanel(t)
	node := uint(1)
	if err := database.GetDB().Create(&model.Inbound{Type: "mixed", Tag: "wg", NodeId: &node, Options: []byte(`{}`)}).Error; err != nil {
		t.Fatalf("seed replica: %v", err)
	}
	if err := save(t, s, "endpoints", "new", testWireguard); err != nil {
		t.Fatalf("an endpoint named like a node replica = %v, want it saved", err)
	}

	// A local inbound of that name is still refused.
	if err := save(t, s, "inbounds", "new", `{"type":"mixed","tag":"local","listen":"127.0.0.1","listen_port":0}`); err != nil {
		t.Fatalf("save inbound: %v", err)
	}
	clash := strings.Replace(testWireguard, `"tag":"wg"`, `"tag":"local"`, 1)
	if err := save(t, s, "endpoints", "new", clash); err == nil || !strings.Contains(err.Error(), "inbound tag already in use") {
		t.Errorf("an endpoint named like a local inbound = %v, want it refused", err)
	}
}
