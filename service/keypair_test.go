package service

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"
)

// A server that checks client certificates refuses one marked only for server
// auth, which is all sing-box's own generator makes. The pair also has to load
// as one: the frontend splits the lines at the PEM markers, key first.
func TestTLSClientKeypairIsForClientAuth(t *testing.T) {
	lines := (&ServerService{}).GenKeypair("tls-client", "vpn-user")
	joined := strings.Join(lines, "\n")
	if !strings.HasPrefix(joined, "-----BEGIN PRIVATE KEY-----") {
		t.Fatalf("want the key first, got %q", lines[0])
	}

	keyBlock, rest := pem.Decode([]byte(joined))
	certBlock, _ := pem.Decode(rest)
	if keyBlock == nil || certBlock == nil || certBlock.Type != "CERTIFICATE" {
		t.Fatalf("want a key then a certificate, got %q", joined)
	}
	if _, err := tls.X509KeyPair(pem.EncodeToMemory(certBlock), pem.EncodeToMemory(keyBlock)); err != nil {
		t.Fatalf("key and certificate do not pair: %v", err)
	}

	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(cert.ExtKeyUsage) != 1 || cert.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("ext key usage = %v, want client auth only", cert.ExtKeyUsage)
	}
	if cert.Subject.CommonName != "vpn-user" {
		t.Errorf("common name = %q, want vpn-user", cert.Subject.CommonName)
	}
	if len(cert.DNSNames) != 0 {
		t.Errorf("a client certificate names no host, got SANs %v", cert.DNSNames)
	}
}
