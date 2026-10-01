package app

import (
	"bytes"
	"context"
	"demo-service/logging"
	"demo-service/telemetry"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/codes"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestRequestTelemetry(t *testing.T) {
	const parentTrace = "0123456789abcdef0123456789abcdef"
	const parentSpan = "0123456789abcdef"
	for _, tc := range []struct {
		name, header, path string
		sampled, continued bool
		status             int
	}{
		{"new", "", "/ping", true, false, 200},
		{"invalid", "invalid", "/ping", true, false, 200},
		{"zero", "00-00000000000000000000000000000000-" + parentSpan + "-01", "/ping", true, false, 200},
		{"continued", "00-" + parentTrace + "-" + parentSpan + "-01", "/ping", true, true, 200},
		{"unsampled", "00-" + parentTrace + "-" + parentSpan + "-00", "/ping", false, true, 200},
		{"probe", "", "/health/ready", true, false, 200},
		{"not found", "", "/missing", true, false, 404},
		{"route template", "", "/items/123", true, false, 204},
		{"panic", "", "/panic", true, false, 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestApplication(t)
			exporter := tracetest.NewInMemoryExporter()
			reader := sdkmetric.NewManualReader()
			a.telemetry = &telemetry.Providers{
				Traces:  sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample()))),
				Metrics: sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader)),
			}
			defer a.telemetry.Shutdown(time.Second)
			var output bytes.Buffer
			a.log, _ = logging.New(&output, "json", "info")
			if err := a.initRouter(); err != nil {
				t.Fatal(err)
			}
			if err := a.mapUrls(); err != nil {
				t.Fatal(err)
			}
			a.router.GET("/panic", func(c *gin.Context) { panic("test failure") })
			a.router.GET("/items/:id", func(c *gin.Context) { c.Status(204) })
			request := httptest.NewRequest("GET", tc.path, nil)
			request.Header.Set("traceparent", tc.header)
			response := httptest.NewRecorder()
			a.router.ServeHTTP(response, request)
			id := response.Header().Get("X-Trace-ID")
			parsed, err := trace.TraceIDFromHex(id)
			if err != nil || !parsed.IsValid() || response.Code != tc.status {
				t.Fatalf("response: %d %q", response.Code, id)
			}
			if tc.continued && id != parentTrace {
				t.Fatalf("lost parent trace: %s", id)
			}
			for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
				var record map[string]any
				if err := json.Unmarshal([]byte(line), &record); err != nil {
					t.Fatal(err)
				}
				if record["trace_id"] != id || record["trace_sampled"] != tc.sampled || record["span_id"] == parentSpan {
					t.Fatalf("bad correlation: %v", record)
				}
			}
			spans := exporter.GetSpans()
			if !tc.sampled {
				if len(spans) != 0 {
					t.Fatal("exported unsampled request")
				}
			}
			if tc.sampled && (len(spans) != 1 || spans[0].SpanContext.TraceID().String() != id) {
				t.Fatalf("unexpected spans: %+v", spans)
			}
			if tc.sampled && tc.continued && spans[0].Parent.SpanID().String() != parentSpan {
				t.Fatal("incorrect span parent")
			}
			if tc.status == 500 && (spans[0].Status.Code != codes.Error || len(spans[0].Events) == 0) {
				t.Fatal("panic missing error status/event")
			}
			var rm metricdata.ResourceMetrics
			if err := reader.Collect(context.Background(), &rm); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, scope := range rm.ScopeMetrics {
				for _, m := range scope.Metrics {
					if h, ok := m.Data.(metricdata.Histogram[float64]); ok {
						for _, point := range h.DataPoints {
							if _, present := point.Attributes.Value("trace_id"); present {
								t.Fatal("trace ID used as metric label")
							}
							if tc.name == "route template" {
								route, _ := point.Attributes.Value("http.route")
								if route.AsString() != "/items/:id" {
									t.Fatal("metric route is not templated")
								}
							}
							if m.Name == "http.server.request.duration" && tc.sampled && len(point.Exemplars) == 0 {
								t.Fatal("missing sampled trace exemplar")
							}
							if point.Count > 0 {
								found = true
							}
						}
					}
				}
			}
			if !found {
				t.Fatal("missing HTTP histogram measurement")
			}
		})
	}
}
