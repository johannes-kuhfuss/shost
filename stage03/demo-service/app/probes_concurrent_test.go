package app

import (
	"demo-service/appstate"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentProbeControlsAndPages(t *testing.T) {
	a := newTestApplication(t)
	// Set before rebuilding the router so request logging stays quiet.
	a.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := a.initRouter(); err != nil {
		t.Fatal(err)
	}
	if err := a.mapUrls(); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var workers sync.WaitGroup
	for worker := range 6 {
		workers.Go(func() {
			<-start
			for i := range 100 {
				path, method, body := "/probes", http.MethodGet, ""
				switch worker {
				case 0:
					path, method, body = "/probes/liveness", http.MethodPost, "action=enable"
					if i%2 == 0 {
						body = "action=disable"
					}
				case 1:
					path, method, body = "/probes/readiness", http.MethodPost, "seconds=1"
				case 2:
					path = "/health/live"
				case 3:
					path = "/health/ready"
				case 4:
					path = "/health/startup"
				}
				req := httptest.NewRequest(method, path, strings.NewReader(body))
				req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response := httptest.NewRecorder()
				a.router.ServeHTTP(response, req)
				valid := response.Code == http.StatusOK
				if worker == 0 {
					valid = response.Code == http.StatusSeeOther
				}
				if worker == 2 || worker == 3 {
					valid = valid || response.Code == http.StatusServiceUnavailable
				}
				if !valid {
					t.Errorf("%s returned %d", path, response.Code)
				}
				for _, stats := range []appstate.ProbeStats{a.state.Runtime.StartupProbeStats(), a.state.Runtime.LivenessProbeStats(), a.state.Runtime.ReadinessProbeStats()} {
					if stats.Count != stats.SuccessCount+stats.FailureCount {
						t.Error("inconsistent counter snapshot")
					}
				}
			}
		})
	}
	close(start)
	workers.Wait()
	for _, stats := range []int{a.state.Runtime.StartupProbeStats().Count, a.state.Runtime.LivenessProbeStats().Count, a.state.Runtime.ReadinessProbeStats().Count} {
		if stats != 100 {
			t.Errorf("probe count = %d, want 100", stats)
		}
	}
}
