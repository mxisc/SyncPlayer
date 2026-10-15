package lanhttps

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProtectedConfigurationAndCachedCertificate(t *testing.T) {
	dir := t.TempDir()
	m, err := Configure(dir, Settings{Domain: "lan.example.com", ID: "test-id", Token: "test-secret-token"})
	if err != nil {
		t.Fatal(err)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"lan.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(60 * 24 * time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	m.settings.Certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	m.settings.PrivateKey = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err = m.save(); err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(filepath.Join(dir, "settings.dpapi"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(stored, []byte("test-secret-token")) || bytes.Contains(stored, []byte("PRIVATE KEY")) {
		t.Fatal("secrets stored without encryption")
	}
	m, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Ensure(); err != nil {
		t.Fatal(err)
	}
	if !m.HasValidCertificate() {
		t.Fatal("cached certificate not loaded")
	}
	c, err := m.TLSConfig().GetCertificate(nil)
	if err != nil || c.Leaf.VerifyHostname("lan.example.com") != nil {
		t.Fatal("TLS certificate unavailable")
	}
	m.settings.Domain = "other.example.com"
	if _, err = m.loadCertificate(); err == nil {
		t.Fatal("certificate hostname mismatch accepted")
	}
}

func TestRejectInvalidDomain(t *testing.T) {
	for _, domain := range []string{"https://example.com", "example.com:8787", "*.example.com", "example.com/path", ""} {
		if _, err := Configure(t.TempDir(), Settings{Domain: domain, ID: "test", Token: "test"}); err == nil {
			t.Fatalf("invalid domain accepted: %q", domain)
		}
	}
}
