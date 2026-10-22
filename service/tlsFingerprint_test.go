package service

import (
	"encoding/json"
	"testing"

	"github.com/shenaba/2s-ui/database/model"
)

// A self-signed server certificate gets its pin on the client side. That used
// to take client.certificate out with it, which is where an OpenConnect or
// OpenVPN endpoint keeps the CA of the server it connects to -- so saving a
// template that serves an inbound too silently dropped the endpoint's CA.
func TestSetCertFingerprintKeepsTheEndpointCA(t *testing.T) {
	lines := (&ServerService{}).GenKeypair("tls", "example.com")
	server, _ := json.Marshal(map[string]interface{}{"enabled": true, "certificate": lines})
	row := &model.Tls{
		Server: server,
		Client: json.RawMessage(`{"certificate":"VPN-CA"}`),
	}
	setCertFingerprint(row)

	var client map[string]interface{}
	if err := json.Unmarshal(row.Client, &client); err != nil {
		t.Fatal(err)
	}
	if client["certificate"] != "VPN-CA" {
		t.Errorf("certificate = %v, want the endpoint's CA kept", client["certificate"])
	}
	if pins, _ := client["certificate_public_key_sha256"].([]interface{}); len(pins) != 1 {
		t.Errorf("want the self-signed certificate pinned, got %v", client)
	}
}
