package app

import (
	"bytes"
	"demo-service/logging"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestMetricsPageSharesInstrumentsWithoutMeasuringScrapes(t *testing.T) {
	a := newTestApplication(t)
	defer a.telemetry.Shutdown(time.Second)
	var logs bytes.Buffer
	a.log, _ = logging.New(&logs, "json", "info")
	if err := a.initRouter(); err != nil {
		t.Fatal(err)
	}
	if err := a.mapUrls(); err != nil {
		t.Fatal(err)
	}
	exporter := tracetest.NewInMemoryExporter()
	a.telemetry.Traces.RegisterSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter))
	if page := performRequest(a.router, "/"); !strings.Contains(page.Body.String(), `href="/metrics">Metrics</a>`) {
		t.Fatal("missing metrics navigation link")
	}
	performRequest(a.router, "/health/ready")
	a.state.Runtime.DisableReadinessFor(time.Minute)
	performRequest(a.router, "/health/ready")
	performRequest(a.router, "/ping")
	beforeSpans, beforeLogs := len(exporter.GetSpans()), logs.Len()
	first := performRequest(a.router, "/metrics")
	if first.Code != 200 || !strings.HasPrefix(first.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("metrics response: %d %v", first.Code, first.Header())
	}
	if first.Header().Get("Cache-Control") != "no-store" || first.Header().Get("X-Trace-ID") != "" {
		t.Fatal(first.Header())
	}
	body := first.Body.String()
	probes := 0
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "demo_service_probe_checks_total{") {
			continue
		}
		probes++
		expected := " 0"
		if strings.Contains(line, `probe="readiness"`) {
			expected = " 1"
		}
		if !strings.HasSuffix(line, expected) {
			t.Fatalf("incorrect probe count: %s", line)
		}
	}
	if probes != 6 || !strings.Contains(body, "http_server_request_duration_seconds_count{") {
		t.Fatalf("missing metrics: %s", body)
	}
	if strings.Contains(body, `http_route="/metrics"`) {
		t.Fatal("metrics measured itself")
	}
	second := performRequest(a.router, "/metrics")
	if second.Body.String() != body {
		t.Fatal("scraping changed the measurements")
	}
	request := httptest.NewRequest("GET", "/metrics", nil)
	request.Header.Set("Accept", "application/openmetrics-text; version=1.0.0")
	response := httptest.NewRecorder()
	a.router.ServeHTTP(response, request)
	if response.Code != 200 || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/openmetrics-text") || !strings.HasSuffix(response.Body.String(), "# EOF\n") {
		t.Fatalf("invalid OpenMetrics response: %v %s", response.Header(), response.Body.String())
	}
	if len(exporter.GetSpans()) != beforeSpans || logs.Len() != beforeLogs {
		t.Fatal("scraping generated traces or access logs")
	}
}

func TestMetricsPageHasZeroProbeSeriesWithoutOTLP(t *testing.T) {
	a := newTestApplication(t)
	defer a.telemetry.Shutdown(time.Second)
	response := performRequest(a.router, "/metrics")
	count := 0
	for _, line := range strings.Split(response.Body.String(), "\n") {
		if strings.HasPrefix(line, "demo_service_probe_checks_total{") {
			count++
			if !strings.HasSuffix(line, " 0") {
				t.Fatal(line)
			}
		}
	}
	if response.Code != 200 || count != 6 {
		t.Fatalf("missing initial metrics: %d %s", response.Code, response.Body.String())
	}
}
