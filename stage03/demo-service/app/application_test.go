package app

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"demo-service/appstate"

	"github.com/gin-gonic/gin"
)

func TestReadinessEndpointReflectsShutdownState(t *testing.T) {
	application := newTestApplication(t)

	response := performRequest(application.state.Runtime.Router, "/health/ready")
	if response.Code != http.StatusOK {
		t.Fatalf("ready status before shutdown = %d, want %d", response.Code, http.StatusOK)
	}
	if !strings.Contains(response.Body.String(), `"status":"ok"`) {
		t.Fatalf("ready response before shutdown = %q, want status ok", response.Body.String())
	}

	application.shuttingDown.Store(true)

	response = performRequest(application.state.Runtime.Router, "/health/ready")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status during shutdown = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
	if !strings.Contains(response.Body.String(), `"status":"shutting down"`) {
		t.Fatalf("ready response during shutdown = %q, want shutting down status", response.Body.String())
	}
}

func TestRunServerDrainsTrafficAndCompletesActiveRequest(t *testing.T) {
	application := newTestApplication(t)

	requestStarted := make(chan struct{})
	releaseRequest := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseRequest) }) }
	t.Cleanup(release)

	application.state.Runtime.Router.GET("/slow", func(c *gin.Context) {
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
	application.initRouter()
	application.initServer()
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
