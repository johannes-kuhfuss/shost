package app

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"demo-service/appconfig"
	"demo-service/appstate"
	"demo-service/certstore"
	"demo-service/handlers"

	"github.com/gin-gonic/gin"
)

func TestStatusPageDisplaysKubernetesMetadata(t *testing.T) {
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			values := map[string]string{"POD_NAME": "", "POD_IP": "", "POD_NAMESPACE": "", "NODE_NAME": ""}
			if populated {
				values = map[string]string{"POD_NAME": "demo-service-abc", "POD_IP": "10.42.1.5", "POD_NAMESPACE": "demo-service", "NODE_NAME": "worker-01"}
			}
			for name, value := range values {
				t.Setenv(name, value)
			}
			t.Setenv("USE_TLS", "false")
			a := newTestApplication(t)
			if err := appconfig.InitConfig(filepath.Join(t.TempDir(), "missing.env"), &a.cfg); err != nil {
				t.Fatal(err)
			}
			response := performRequest(a.router, "/")
			if response.Code != http.StatusOK {
				t.Fatalf("status page returned %d", response.Code)
			}
			for label, value := range map[string]string{"Pod Name": values["POD_NAME"], "Pod IP": values["POD_IP"], "Pod Namespace": values["POD_NAMESPACE"], "Node Name": values["NODE_NAME"]} {
				if value == "" {
					value = "N/A"
				}
				row := "<td>" + label + "</td> <td>" + value + "</td>"
				if !strings.Contains(strings.Join(strings.Fields(response.Body.String()), " "), row) {
					t.Errorf("status page missing %s = %s", label, value)
				}
			}
		})
	}
}

func TestReadinessEndpointReflectsShutdownState(t *testing.T) {
	application := newTestApplication(t)

	response := performRequest(application.router, "/health/ready")
	if response.Code != http.StatusOK {
		t.Fatalf("ready status before shutdown = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("ready response before shutdown = %q, want status ok", response.Body.String())
	}

	application.shuttingDown.Store(true)

	response = performRequest(application.router, "/health/ready")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status during shutdown = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(response.Body.String(), `"status":"shutting down"`) {
		t.Fatalf("ready response during shutdown = %q, want shutting down status", response.Body.String())
	}
}

func TestBasicEndpoints(t *testing.T) {
	application := newTestApplication(t)
	tests := []struct {
		path string
		body string
	}{
		{path: "/ping", body: `{"ping":"pong"}`},
		{path: "/health/startup", body: `{"endpoint":"startup probe","status":"ok"}`},
		{path: "/health/live", body: `{"endpoint":"live probe","status":"ok"}`},
	}

	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			response := performRequest(application.router, test.path)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
			}
			if got := strings.TrimSpace(response.Body.String()); got != test.body {
				t.Fatalf("body = %q, want %q", got, test.body)
			}
			if got := response.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
				t.Fatalf("Content-Type = %q, want JSON", got)
			}
		})
	}
}

func TestUnknownEndpointReturnsNotFound(t *testing.T) {
	application := newTestApplication(t)
	response := performRequest(application.router, "/not-found")
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusNotFound)
	}
}

func TestCertificateEndpointWhenTLSIsDisabled(t *testing.T) {
	application := newTestApplication(t)
	response := performRequest(application.router, "/certificate")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Body.String(); !strings.Contains(got, "TLS is disabled") || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("body = %q, want TLS-disabled error", got)
	}
}

func TestCertificateEndpointWhenCertificateIsNotLoaded(t *testing.T) {
	application := newTestApplication(t)
	application.certificateStore = certstore.New("missing.crt", "missing.key", slog.Default())
	response := performRequest(application.router, "/certificate")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if got := response.Body.String(); !strings.Contains(got, "No certificate loaded") || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("body = %q, want no-certificate error", got)
	}
}

func TestCertificateEndpointReturnsLoadedMetadata(t *testing.T) {
	directory := t.TempDir()
	certFile := filepath.Join(directory, "tls.crt")
	keyFile := filepath.Join(directory, "tls.key")
	writeTestCertificate(t, certFile, keyFile)

	application := newTestApplication(t)
	application.certificateStore = certstore.New(certFile, keyFile, slog.Default())
	if err := application.certificateStore.Reload(); err != nil {
		t.Fatalf("Reload() error = %v", err)
	}
	response := performRequest(application.router, "/certificate")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.HasPrefix(response.Header().Get("Content-Type"), "text/html") {
		t.Fatal("certificate page should return HTML")
	}
	info := application.certificateStore.Info()
	for _, want := range []string{"<td>demo-service</td>", `<td class="text-break">7</td>`, "DNS Names", "N/A", info.SHA256Fingerprint, info.NotBefore.UTC().Format(time.RFC3339), info.NotAfter.UTC().Format(time.RFC3339), `href="/certificate"`} {
		if !strings.Contains(response.Body.String(), want) {
			t.Errorf("certificate page missing %q", want)
		}
	}
}

func TestInitServerConfiguresHTTPAndTLS(t *testing.T) {
	httpApplication := &Application{state: appstate.New()}
	httpApplication.cfg.Server.Host = "127.0.0.1"
	httpApplication.cfg.Server.Port = "8081"
	httpApplication.initServer()
	if httpApplication.server.Addr != "127.0.0.1:8081" || httpApplication.server.TLSConfig != nil {
		t.Fatalf("HTTP server = addr %q, TLS config %v", httpApplication.server.Addr, httpApplication.server.TLSConfig)
	}

	tlsApplication := &Application{state: appstate.New()}
	tlsApplication.cfg.Server.Host = "127.0.0.1"
	tlsApplication.cfg.Server.TLSPort = "8444"
	tlsApplication.cfg.Server.UseTLS = true
	tlsApplication.certificateStore = certstore.New("missing.crt", "missing.key", slog.Default())
	tlsApplication.initServer()
	if tlsApplication.server.Addr != "127.0.0.1:8444" {
		t.Fatalf("TLS server address = %q, want 127.0.0.1:8444", tlsApplication.server.Addr)
	}
	if tlsApplication.server.TLSConfig == nil || tlsApplication.server.TLSConfig.MinVersion != tls.VersionTLS13 {
		t.Fatal("TLS server was not configured for TLS 1.3")
	}
	if tlsApplication.server.TLSConfig.GetCertificate == nil {
		t.Fatal("TLS server has no dynamic certificate callback")
	}
}

func TestRunServerDrainsTrafficAndCompletesActiveRequest(t *testing.T) {
	application := newTestApplication(t)

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	t.Cleanup(release)

	application.router.GET("/slow", func(c *gin.Context) {
		close(requestStarted)
		<-releaseRequest
		c.String(http.StatusOK, "completed")
	})

	listener, serve := startTestServer(t, application)
	shutdownStarted := make(chan struct{})
	application.server.RegisterOnShutdown(func() { close(shutdownStarted) })

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- application.runServer(ctx, serve, nil, 500*time.Millisecond, time.Second)
	}()

	requestResult := make(chan httpResult, 1)
	go func() {
		response, err := testHTTPClient().Get("http://" + listener.Addr().String() + "/slow")
		if err != nil {
			requestResult <- httpResult{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		requestResult <- httpResult{statusCode: response.StatusCode, body: string(body), err: err}
	}()

	waitForSignal(t, requestStarted, "slow request to start")
	cancel()
	waitForCondition(t, time.Second, application.shuttingDown.Load, "application to enter draining state")

	readyResponse := getURL(t, "http://"+listener.Addr().String()+"/health/ready")
	if readyResponse.statusCode != http.StatusServiceUnavailable {
		t.Fatalf("ready status while draining = %d, want %d", readyResponse.statusCode, http.StatusServiceUnavailable)
	}

	regularResponse := getURL(t, "http://"+listener.Addr().String()+"/")
	if regularResponse.statusCode != http.StatusOK {
		t.Fatalf("regular request status while draining = %d, want %d", regularResponse.statusCode, http.StatusOK)
	}

	waitForSignal(t, shutdownStarted, "HTTP shutdown to start")
	select {
	case err := <-result:
		t.Fatalf("runServer returned before active request completed: %v", err)
	default:
	}

	release()

	slowResponse := waitForHTTPResult(t, requestResult)
	if slowResponse.err != nil {
		t.Fatalf("active request failed during shutdown: %v", slowResponse.err)
	}
	if slowResponse.statusCode != http.StatusOK || slowResponse.body != "completed" {
		t.Fatalf("active request response = (%d, %q), want (%d, %q)", slowResponse.statusCode, slowResponse.body, http.StatusOK, "completed")
	}
	if err := waitForResult(t, result); err != nil {
		t.Fatalf("runServer() error = %v", err)
	}
}

func TestRunServerReturnsFailureDuringDrain(t *testing.T) {
	application := &Application{}
	serveStarted := make(chan struct{})
	failServe := make(chan struct{})
	wantErr := errors.New("serve failed")

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- application.runServer(ctx, func() error {
			close(serveStarted)
			<-failServe
			return wantErr
		}, nil, time.Hour, time.Second)
	}()

	waitForSignal(t, serveStarted, "serve function to start")
	cancel()
	waitForCondition(t, time.Second, application.shuttingDown.Load, "application to enter draining state")
	close(failServe)

	if err := waitForResult(t, result); !errors.Is(err, wantErr) {
		t.Fatalf("runServer() error = %v, want %v", err, wantErr)
	}
}

func TestRunServerForcesCloseAfterShutdownTimeout(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	t.Cleanup(release)

	application := &Application{}
	application.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-releaseRequest
		w.WriteHeader(http.StatusOK)
	})

	listener, serve := startTestServer(t, application)
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- application.runServer(ctx, serve, nil, 0, 100*time.Millisecond)
	}()

	requestDone := make(chan struct{})
	go func() {
		response, err := testHTTPClient().Get("http://" + listener.Addr().String())
		if err == nil {
			response.Body.Close()
		}
		close(requestDone)
	}()

	waitForSignal(t, requestStarted, "request to start")
	cancel()

	err := waitForResult(t, result)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runServer() error = %v, want context deadline exceeded", err)
	}
	waitForSignal(t, requestDone, "client request to be disconnected")
}

func TestRunServerShutsDownOnWatcherFailure(t *testing.T) {
	application := &Application{}
	application.server.Handler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	_, serve := startTestServer(t, application)
	wantErr := errors.New("watcher failed")
	watchErr := make(chan error, 1)
	result := make(chan error, 1)

	go func() {
		result <- application.runServer(context.Background(), serve, watchErr, 0, time.Second)
	}()
	watchErr <- wantErr

	err := waitForResult(t, result)
	if !errors.Is(err, wantErr) {
		t.Fatalf("runServer() error = %v, want %v", err, wantErr)
	}
	if !application.shuttingDown.Load() {
		t.Fatal("application did not enter shutdown state after watcher failure")
	}
}

type httpResult struct {
	statusCode int
	body       string
	err        error
}

func newTestApplication(t *testing.T) *Application {
	t.Helper()
	gin.SetMode(gin.TestMode)

	application := &Application{state: appstate.New()}
	application.cfg.Gin.TemplatePath = "../templates"
	if err := application.initRouter(); err != nil {
		t.Fatal(err)
	}
	application.initServer()
	application.statsUiHandler = handlers.NewStatsUiHandler(&application.cfg, application.state)
	if err := application.mapUrls(); err != nil {
		t.Fatalf("mapUrls() error = %v", err)
	}
	return application
}

func startTestServer(t *testing.T, application *Application) (net.Listener, func() error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() {
		application.server.Close()
		listener.Close()
	})

	serve := func() error {
		err := application.server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
	return listener, serve
}

func performRequest(handler http.Handler, path string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	return recorder
}

func getURL(t *testing.T, url string) httpResult {
	t.Helper()
	response, err := testHTTPClient().Get(url)
	if err != nil {
		t.Fatalf("GET %s failed: %v", url, err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("reading GET %s response failed: %v", url, err)
	}
	return httpResult{statusCode: response.StatusCode, body: string(body)}
}

func testHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 2 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
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

func waitForCondition(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", description)
		}
		time.Sleep(time.Millisecond)
	}
}

func waitForHTTPResult(t *testing.T, result <-chan httpResult) httpResult {
	t.Helper()
	select {
	case response := <-result:
		return response
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for HTTP response")
		return httpResult{}
	}
}

func waitForResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for runServer result")
		return nil
	}
}

func writeTestCertificate(t *testing.T, certFile, keyFile string) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "demo-service"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certificateDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	privateKeyDER, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificateDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKeyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
}
