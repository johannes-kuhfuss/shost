package app

import (
	"demo-service/appstate"
	"demo-service/handlers"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gin-gonic/gin"
)

func TestTimedReadinessControl(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := newTestApplication(t)
		router := a.state.Runtime.Router
		post := func(seconds string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(http.MethodPost, "/probes/readiness", strings.NewReader("seconds="+seconds))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			return response
		}
		for _, invalid := range []string{"", "0", "-1", "3601", "1.5", "abc", "999999999999999999999"} {
			if response := post(invalid); response.Code != http.StatusBadRequest || !a.state.Runtime.ReadinessDisabledUntil().IsZero() {
				t.Fatalf("invalid duration %q changed readiness or returned %d", invalid, response.Code)
			}
		}
		response := post("10")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "10 seconds remaining") || response.Header().Get("Location") != "" {
			t.Fatalf("missing direct countdown confirmation: %d %s", response.Code, response.Body.String())
		}
		if got := performRequest(router, "/health/ready"); got.Code != http.StatusServiceUnavailable {
			t.Fatal("readiness should fail during pause")
		}
		if got := performRequest(router, "/health/live"); got.Code != http.StatusOK {
			t.Fatal("readiness pause affected liveness")
		}
		time.Sleep(5 * time.Second)
		post("10") // A new pause replaces the previous deadline.
		time.Sleep(5 * time.Second)
		if got := performRequest(router, "/health/ready"); got.Code != http.StatusServiceUnavailable {
			t.Fatal("previous deadline ended the replacement pause")
		}
		time.Sleep(5 * time.Second)
		if got := performRequest(router, "/health/ready"); got.Code != http.StatusOK {
			t.Fatal("readiness did not recover without UI requests")
		}
		stats := a.state.Runtime.ReadinessProbeStats()
		if stats.SuccessCount != 1 || stats.FailureCount != 2 {
			t.Fatalf("incorrect readiness counters: %+v", stats)
		}
		post("1")
		a.shuttingDown.Store(true)
		time.Sleep(time.Second)
		if got := performRequest(router, "/health/ready"); got.Code != http.StatusServiceUnavailable || !strings.Contains(got.Body.String(), "shutting down") {
			t.Fatal("timer expiration overrode shutdown readiness")
		}
	})
}

func TestLivenessControl(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &Application{state: appstate.New()}
	a.cfg.Gin.TemplatePath = "../templates"
	a.initRouter()
	a.statsUiHandler = handlers.NewStatsUiHandlerWithState(&a.cfg, a.state)
	if err := a.mapUrls(); err != nil {
		t.Fatal(err)
	}
	router := a.state.Runtime.Router
	wantSuccess, wantFailure := 0, 0
	for _, step := range []struct {
		action     string
		wantStatus int
		disabled   bool
	}{
		{"", http.StatusOK, false},
		{"disable", http.StatusSeeOther, true},
		{"disable", http.StatusSeeOther, true},
		{"invalid", http.StatusBadRequest, true},
		{"enable", http.StatusSeeOther, false},
	} {
		if step.action != "" {
			request := httptest.NewRequest(http.MethodPost, "/probes/liveness", strings.NewReader("action="+step.action))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != step.wantStatus {
				t.Fatalf("action %q: status = %d", step.action, response.Code)
			}
			if response.Code == http.StatusSeeOther && response.Header().Get("Location") != "/probes" {
				t.Fatal("control did not redirect to probe page")
			}
		}
		before := a.state.Runtime.LivenessProbeStats().Count
		started := time.Now().UTC()
		live := performRequest(router, "/health/live")
		wantLive := http.StatusOK
		label := "Enabled (HTTP 200)"
		if step.disabled {
			wantFailure++
			wantLive = http.StatusServiceUnavailable
			label = "Disabled (HTTP 503)"
		} else {
			wantSuccess++
		}
		stats := a.state.Runtime.LivenessProbeStats()
		if live.Code != wantLive || stats.LastProbeSuccessful == step.disabled || stats.Count != before+1 || stats.SuccessCount != wantSuccess || stats.FailureCount != wantFailure || stats.LastProbeDate.Before(started) {
			t.Fatalf("action %q: liveness status %d, stats %+v", step.action, live.Code, stats)
		}
		page := performRequest(router, "/probes")
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), label) {
			t.Fatalf("probe page missing state %q", label)
		}
		for _, path := range []string{"/health/startup", "/health/ready"} {
			if response := performRequest(router, path); response.Code != http.StatusOK {
				t.Fatalf("%s affected by liveness control", path)
			}
		}
	}
	if appstate.New().Runtime.LivenessDisabled() {
		t.Fatal("fresh application must have liveness enabled")
	}
}

func TestProbeRequestsUpdateState(t *testing.T) {
	gin.SetMode(gin.TestMode)
	a := &Application{state: appstate.New()}
	router := gin.New()
	router.GET("/health/startup", a.startup)
	router.GET("/health/live", a.live)
	router.GET("/health/ready", a.ready)
	router.LoadHTMLGlob("../templates/*.tmpl")
	ui := handlers.NewStatsUiHandlerWithState(&a.cfg, a.state)
	router.GET("/probes", ui.ProbesPage)
	initialPage := performRequest(router, "/probes")
	if initialPage.Code != http.StatusOK || strings.Count(initialPage.Body.String(), "<td>N/A</td>") != 6 || strings.Count(initialPage.Body.String(), "<td>0</td>") != 9 {
		t.Fatalf("initial probe page = %d: %s", initialPage.Code, initialPage.Body.String())
	}

	for _, probe := range []struct {
		path  string
		stats func() appstate.ProbeStats
	}{
		{"/health/startup", a.state.Runtime.StartupProbeStats},
		{"/health/live", a.state.Runtime.LivenessProbeStats},
		{"/health/ready", a.state.Runtime.ReadinessProbeStats},
	} {
		for count := 1; count <= 2; count++ {
			started := time.Now().UTC()
			response := performRequest(router, probe.path)
			got := probe.stats()
			if response.Code != http.StatusOK || got.Count != count || got.SuccessCount != count || got.FailureCount != 0 || got.LastProbeDate.Before(started) || got.LastProbeDate.After(time.Now()) {
				t.Fatalf("%s: status %d, stats %+v", probe.path, response.Code, got)
			}
		}
	}

	a.shuttingDown.Store(true)
	started := time.Now().UTC()
	response := performRequest(router, "/health/ready")
	got := a.state.Runtime.ReadinessProbeStats()
	if response.Code != http.StatusServiceUnavailable || got.LastProbeSuccessful || got.Count != 3 || got.SuccessCount != 2 || got.FailureCount != 1 || got.LastProbeDate.Before(started) {
		t.Fatalf("readiness during shutdown: status %d, stats %+v", response.Code, got)
	}
	page := performRequest(router, "/probes")
	if page.Code != http.StatusOK {
		t.Fatalf("probe page status = %d", page.Code)
	}
	if strings.Count(page.Body.String(), "<td>Successful</td>") != 2 || strings.Count(page.Body.String(), "<td>Failed</td>") != 1 {
		t.Fatal("probe page should show two successful last probes and one failed last probe")
	}
	for _, want := range []string{"Startup", "Liveness", "Readiness", "Successful", "Failed", `href="/probes"`, "<td>3</td>", "<td>1</td>", got.LastProbeDate.Format(time.RFC3339)} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("probe page missing %q", want)
		}
	}
	if strings.Count(page.Body.String(), "<td>2</td>") != 5 || a.state.Runtime.ReadinessProbeStats().Count != 3 {
		t.Fatal("probe page should display counts without recording a probe")
	}
}
