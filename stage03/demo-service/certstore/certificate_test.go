package certstore

import (
	"path/filepath"
	"testing"
)

func TestCertificateStoreReload(t *testing.T) {
	store := New(
		filepath.Join("..", "test-cert", "cert.pem"),
		filepath.Join("..", "test-cert", "key.pem"),
	)

	if err := store.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if store.Info() == nil {
		t.Fatal("Info() = nil after Reload()")
	}
	if _, err := store.GetCertificate(nil); err != nil {
		t.Fatalf("GetCertificate() error = %v", err)
	}
}
