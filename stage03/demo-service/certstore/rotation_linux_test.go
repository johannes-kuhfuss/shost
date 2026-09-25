package certstore

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Model a projected Secret: tls.crt/key -> ..data/file, then atomically
// replace ..data to point at a new version directory.
func TestProjectedSecretRotationServesNewCertificate(t *testing.T) {
	dir := t.TempDir()
	first, second := generateCertificatePair(t, 1), generateCertificatePair(t, 2)
	for name, pair := range map[string]certificatePair{"v1": first, "v2": second} {
		version := filepath.Join(dir, name)
		if err := os.Mkdir(version, 0700); err != nil {
			t.Fatal(err)
		}
		writeCertificatePair(t, filepath.Join(version, "tls.crt"), filepath.Join(version, "tls.key"), pair)
	}
	for link, target := range map[string]string{"..data": "v1", "tls.crt": "..data/tls.crt", "tls.key": "..data/tls.key"} {
		if err := os.Symlink(target, filepath.Join(dir, link)); err != nil {
			t.Fatal(err)
		}
	}
	store := New(filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key"), slog.Default())
	if err := store.Reload(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ready, renewed := make(chan struct{}), make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		done <- store.watchCertFolder(ctx, 10*time.Millisecond, ready, func(time.Time) { renewed <- struct{}{} })
	}()
	t.Cleanup(func() {
		cancel()
		if err := waitForWatcherResult(t, done); err != nil {
			t.Error(err)
		}
	})
	waitForSignal(t, ready, "watcher startup")
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13, GetCertificate: store.GetCertificate}
	server.StartTLS()
	defer server.Close()
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(first.certPEM)
	roots.AppendCertsFromPEM(second.certPEM)
	check := func(want string) {
		t.Helper()
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 2 * time.Second}, "tcp", server.Listener.Addr().String(), &tls.Config{
			MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "demo-service.demo-service.svc.cluster.local",
		})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		if got := conn.ConnectionState().PeerCertificates[0].SerialNumber.String(); got != want {
			t.Fatalf("served serial %s, want %s", got, want)
		}
	}
	check("1")
	if err := os.Symlink("v2", filepath.Join(dir, "..data_tmp")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(dir, "..data_tmp"), filepath.Join(dir, "..data")); err != nil {
		t.Fatal(err)
	}
	waitForSignal(t, renewed, "certificate renewal")
	check("2")
}
