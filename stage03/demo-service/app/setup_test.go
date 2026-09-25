package app

import (
	"demo-service/appstate"
	"demo-service/certstore"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestListenAddresses(t *testing.T) {
	for _, host := range []struct{ value, prefix string }{
		{"", ""}, {"0.0.0.0", "0.0.0.0"}, {"127.0.0.1", "127.0.0.1"}, {"::1", "[::1]"}, {"::", "[::]"},
	} {
		for _, useTLS := range []bool{false, true} {
			a := &Application{state: appstate.New()}
			a.cfg.Server.Host = host.value
			a.cfg.Server.Port, a.cfg.Server.TLSPort = "8080", "8443"
			a.cfg.Server.UseTLS = useTLS
			a.certificateStore = certstore.New("", "", slog.Default())
			a.initServer()
			port := "8080"
			if useTLS {
				port = "8443"
			}
			want := host.prefix + ":" + port
			if a.server.Addr != want || a.state.Runtime.ListenAddr != want {
				t.Errorf("host %q, TLS %v: address %q, want %q", host.value, useTLS, a.server.Addr, want)
			}
		}
	}
}

func TestInitRouterReturnsTemplateErrors(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		dir := t.TempDir()
		if malformed {
			if err := os.WriteFile(filepath.Join(dir, "broken.tmpl"), []byte("{{ if }}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		a := &Application{state: appstate.New()}
		a.cfg.Gin.TemplatePath = dir
		if err := a.initRouter(); err == nil || !strings.Contains(err.Error(), "load HTML templates") || !strings.Contains(err.Error(), strconv.Quote(filepath.Join(dir, "*.tmpl"))) {
			t.Fatalf("initRouter error = %v, want descriptive template error", err)
		}
		if a.router != nil {
			t.Fatal("failed initialization published router")
		}
	}
}
