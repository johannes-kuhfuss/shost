package certstore

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCertificateStoreReload(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	pair := generateCertificatePair(t, 42)
	writeCertificatePair(t, certFile, keyFile, pair)
	store := New(certFile, keyFile, slog.Default())

	if err := store.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	if store.Info() == nil {
		t.Fatal("Info() = nil after Reload()")
	}
	if _, err := store.GetCertificate(nil); err != nil {
		t.Fatalf("GetCertificate() error = %v", err)
	}

	info := store.Info()
	if info.Subject != "demo-service" || info.Issuer != "demo-service" || info.SerialNumber != "42" {
		t.Fatalf("certificate info = %+v, want generated certificate metadata", info)
	}
	wantFingerprint := fmt.Sprintf("%X", sha256.Sum256(pair.certDER))
	if info.SHA256Fingerprint != wantFingerprint {
		t.Fatalf("fingerprint = %q, want %q", info.SHA256Fingerprint, wantFingerprint)
	}
}

func TestCertificateStoreBeforeInitialLoad(t *testing.T) {
	store := New("missing.crt", "missing.key", slog.Default())
	if store.Info() != nil {
		t.Fatal("Info() before Reload() is not nil")
	}
	if _, err := store.GetCertificate(nil); err == nil {
		t.Fatal("GetCertificate() before Reload() succeeded")
	}
}

func TestReloadRejectsInvalidPEM(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	if err := os.WriteFile(certFile, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := New(certFile, keyFile, slog.Default())
	if err := store.Reload(); err == nil {
		t.Fatal("Reload() succeeded with invalid PEM")
	}
}

func TestInfoReturnsIndependentDNSNames(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	writeCertificatePair(t, certFile, keyFile, generateCertificatePair(t, 1))
	store := New(certFile, keyFile, slog.Default())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}

	first := store.Info()
	first.DNSNames[0] = "modified.example"
	if got := store.Info().DNSNames[0]; got == "modified.example" {
		t.Fatal("Info() exposed mutable certificate state")
	}
}

func TestConcurrentReloadAndRead(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	writeCertificatePair(t, certFile, keyFile, generateCertificatePair(t, 1))
	store := New(certFile, keyFile, slog.Default())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 100 {
			_ = store.Info()
			_, _ = store.GetCertificate(nil)
		}
	}()
	for range 100 {
		if err := store.Reload(); err != nil {
			t.Fatalf("Reload() error = %v", err)
		}
	}
	<-done
}

func TestWatchCertFolderReloadsCertificateAndStopsWithContext(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	writeCertificatePair(t, certFile, keyFile, generateCertificatePair(t, 1))

	store := New(certFile, keyFile, slog.Default())
	if err := store.Reload(); err != nil {
		t.Fatalf("initial Reload() error = %v", err)
	}
	initialFingerprint := store.Info().SHA256Fingerprint

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- store.watchCertFolder(ctx, 50*time.Millisecond, ready)
	}()

	waitForSignal(t, ready, "certificate watcher to start")
	select {
	case err := <-result:
		t.Fatalf("watcher returned before cancellation: %v", err)
	default:
	}

	writeCertificatePair(t, certFile, keyFile, generateCertificatePair(t, 2))
	waitForFingerprintChange(t, store, initialFingerprint)

	cancel()
	if err := waitForWatcherResult(t, result); err != nil {
		t.Fatalf("watcher returned an error after cancellation: %v", err)
	}
}

func TestWatcherKeepsOldCertificateDuringInvalidRotationAndRecovers(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	firstPair := generateCertificatePair(t, 1)
	secondPair := generateCertificatePair(t, 2)
	writeCertificatePair(t, certFile, keyFile, firstPair)

	store := New(certFile, keyFile, slog.Default())
	if err := store.Reload(); err != nil {
		t.Fatalf("initial Reload() error = %v", err)
	}
	initialFingerprint := store.Info().SHA256Fingerprint

	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	result := make(chan error, 1)
	go func() { result <- store.watchCertFolder(ctx, 30*time.Millisecond, ready) }()
	waitForSignal(t, ready, "certificate watcher to start")

	if err := os.WriteFile(certFile, secondPair.certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := store.Info().SHA256Fingerprint; got != initialFingerprint {
		t.Fatalf("invalid intermediate rotation replaced certificate: got %q, want %q", got, initialFingerprint)
	}

	if err := os.WriteFile(keyFile, secondPair.keyPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	waitForFingerprintChange(t, store, initialFingerprint)
	cancel()
	if err := waitForWatcherResult(t, result); err != nil {
		t.Fatalf("watcher returned an error after recovery: %v", err)
	}
}

func TestReloadKeepsLastSnapshotWhenKeyDoesNotMatch(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	firstPair := generateCertificatePair(t, 1)
	secondPair := generateCertificatePair(t, 2)
	writeCertificatePair(t, certFile, keyFile, firstPair)

	store := New(certFile, keyFile, slog.Default())
	if err := store.Reload(); err != nil {
		t.Fatalf("initial Reload() error = %v", err)
	}
	initialFingerprint := store.Info().SHA256Fingerprint

	if err := os.WriteFile(certFile, secondPair.certPEM, 0o600); err != nil {
		t.Fatalf("write replacement certificate: %v", err)
	}
	if err := store.Reload(); err == nil {
		t.Fatal("Reload() succeeded with a mismatched certificate and key")
	}

	if got := store.Info().SHA256Fingerprint; got != initialFingerprint {
		t.Fatalf("fingerprint after failed reload = %q, want previous fingerprint %q", got, initialFingerprint)
	}
}

type certificatePair struct {
	certPEM []byte
	keyPEM  []byte
	certDER []byte
}

func generateCertificatePair(t *testing.T, serial int64) certificatePair {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate private key: %v", err)
	}

	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "demo-service"},
		DNSNames:     []string{"demo-service.demo-service.svc.cluster.local"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}

	return certificatePair{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}),
		certDER: certificateDER,
	}
}

func writeCertificatePair(t *testing.T, certFile, keyFile string, pair certificatePair) {
	t.Helper()
	if err := os.WriteFile(certFile, pair.certPEM, 0o600); err != nil {
		t.Fatalf("write certificate: %v", err)
	}
	if err := os.WriteFile(keyFile, pair.keyPEM, 0o600); err != nil {
		t.Fatalf("write private key: %v", err)
	}
}

func waitForFingerprintChange(t *testing.T, store *CertificateStore, previous string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		info := store.Info()
		if info != nil && info.SHA256Fingerprint != previous {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("certificate fingerprint did not change from %q", previous)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", description)
	}
}

func waitForWatcherResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for certificate watcher to stop")
		return nil
	}
}
