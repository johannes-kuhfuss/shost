package app

import (
	"demo-service/appstate"
	"demo-service/handlers"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

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
		if live.Code != wantLive || stats.Count != before+1 || stats.SuccessCount != wantSuccess || stats.FailureCount != wantFailure || stats.LastProbeDate.Before(started) {
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
	if initialPage.Code != http.StatusOK || strings.Count(initialPage.Body.String(), "<td>N/A</td>") != 3 || strings.Count(initialPage.Body.String(), "<td>0</td>") != 9 {
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
	if response.Code != http.StatusServiceUnavailable || got.Count != 3 || got.SuccessCount != 2 || got.FailureCount != 1 || got.LastProbeDate.Before(started) {
		t.Fatalf("readiness during shutdown: status %d, stats %+v", response.Code, got)
	}
	page := performRequest(router, "/probes")
	if page.Code != http.StatusOK {
		t.Fatalf("probe page status = %d", page.Code)
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
