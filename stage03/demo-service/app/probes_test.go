package app

import (
	"demo-service/appstate"
	"demo-service/handlers"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

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
	if initialPage.Code != http.StatusOK || strings.Count(initialPage.Body.String(), "<td>N/A</td>") != 3 || strings.Count(initialPage.Body.String(), "<td>0</td>") != 3 {
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
			if response.Code != http.StatusOK || got.Count != count || got.LastProbeDate.Before(started) || got.LastProbeDate.After(time.Now()) {
				t.Fatalf("%s: status %d, stats %+v", probe.path, response.Code, got)
			}
		}
	}

	a.shuttingDown.Store(true)
	started := time.Now().UTC()
	response := performRequest(router, "/health/ready")
	got := a.state.Runtime.ReadinessProbeStats()
	if response.Code != http.StatusServiceUnavailable || got.Count != 3 || got.LastProbeDate.Before(started) {
		t.Fatalf("readiness during shutdown: status %d, stats %+v", response.Code, got)
	}
	page := performRequest(router, "/probes")
	if page.Code != http.StatusOK {
		t.Fatalf("probe page status = %d", page.Code)
	}
	for _, want := range []string{"Startup", "Liveness", "Readiness", `href="/probes"`, "<td>3</td>", got.LastProbeDate.Format(time.RFC3339)} {
		if !strings.Contains(page.Body.String(), want) {
			t.Errorf("probe page missing %q", want)
		}
	}
	if strings.Count(page.Body.String(), "<td>2</td>") != 2 || a.state.Runtime.ReadinessProbeStats().Count != 3 {
		t.Fatal("probe page should display counts without recording a probe")
	}
}
