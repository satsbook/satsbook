package lnd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestMacaroonCredential_RequireTransportSecurity(t *testing.T) {
	cred := &macaroonCredential{macaroon: "deadbeef"}
	if !cred.RequireTransportSecurity() {
		t.Error("expected RequireTransportSecurity to return true")
	}
}

func TestMacaroonCredential_GetRequestMetadata(t *testing.T) {
	const hex = "deadbeef"
	cred := &macaroonCredential{macaroon: hex}
	meta, err := cred.GetRequestMetadata(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := meta["macaroon"]; got != hex {
		t.Errorf("macaroon = %q, want %q", got, hex)
	}
}

func TestLoadMacaroon_MissingFile(t *testing.T) {
	_, err := loadMacaroon("/nonexistent/path/macaroon.macaroon")
	if err == nil {
		t.Error("expected error for missing macaroon file, got nil")
	}
}

func TestLoadTLSCredentials_MissingFile(t *testing.T) {
	_, err := loadTLSCredentials("/nonexistent/path/tls.cert")
	if err == nil {
		t.Error("expected error for missing TLS cert file, got nil")
	}
}

func TestLoadTLSCredentials_InvalidCert(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "tls*.cert")
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("not a valid certificate")
	f.Close()

	_, err = loadTLSCredentials(f.Name())
	if err == nil {
		t.Error("expected error for invalid TLS cert, got nil")
	}
}

func TestLoadMacaroon_ValidFile(t *testing.T) {
	dir := t.TempDir()
	macaroonPath := filepath.Join(dir, "admin.macaroon")
	content := []byte{0xde, 0xad, 0xbe, 0xef}
	if err := os.WriteFile(macaroonPath, content, 0600); err != nil {
		t.Fatal(err)
	}

	cred, err := loadMacaroon(macaroonPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cred.macaroon != "deadbeef" {
		t.Errorf("macaroon hex = %q, want %q", cred.macaroon, "deadbeef")
	}
}

func TestNewClient_InvalidPaths(t *testing.T) {
	_, err := NewClient("localhost", 10009, "/no/macaroon", "/no/cert")
	if err == nil {
		t.Error("expected error for invalid credential paths, got nil")
	}
}
