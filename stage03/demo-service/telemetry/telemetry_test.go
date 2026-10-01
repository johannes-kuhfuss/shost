package telemetry

import (
	"context"
	"demo-service/appconfig"
	"demo-service/appstate"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	metricpb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestProbeCounters(t *testing.T) {
	state := appstate.New()
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer provider.Shutdown(context.Background())
	if err := RegisterProbes(provider.Meter("test"), &state.Runtime); err != nil {
		t.Fatal(err)
	}
	collect := func() map[string]int64 {
		t.Helper()
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		got := map[string]int64{}
		for _, scope := range rm.ScopeMetrics {
			for _, m := range scope.Metrics {
				sum, ok := m.Data.(metricdata.Sum[int64])
				if !ok || !sum.IsMonotonic || sum.Temporality != metricdata.CumulativeTemporality {
					t.Fatalf("not a cumulative counter: %+v", m)
				}
				for _, point := range sum.DataPoints {
					probe, _ := point.Attributes.Value("probe")
					outcome, _ := point.Attributes.Value("outcome")
					got[probe.AsString()+"/"+outcome.AsString()] = point.Value
				}
			}
		}
		return got
	}
	if got := collect(); len(got) != 6 {
		t.Fatalf("expected six zero series: %v", got)
	} else {
		for _, n := range got {
			if n != 0 {
				t.Fatal(got)
			}
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			state.Runtime.RecordStartupProbe(true)
			state.Runtime.RecordReadinessProbe(false)
			state.Runtime.RecordLivenessProbe(true)
		}
	})
	for range 10 {
		collect()
	}
	wg.Wait()
	got := collect()
	if got["startup/success"] != 100 || got["readiness/failure"] != 100 || got["liveness/success"] != 100 || got["startup/failure"] != 0 {
		t.Fatal(got)
	}
}

func TestDownstreamPropagation(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	p := &Providers{Traces: sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter)), Metrics: sdkmetric.NewMeterProvider()}
	defer p.Shutdown(time.Second)
	received := make(chan trace.SpanContext, 1)
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		received <- trace.SpanContextFromContext(ctx)
		w.WriteHeader(204)
	}))
	defer downstream.Close()
	ctx, parent := p.Traces.Tracer("test").Start(context.Background(), "incoming")
	defer parent.End()
	request, _ := http.NewRequestWithContext(ctx, "GET", downstream.URL, nil)
	response, err := p.HTTPClient(time.Second).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	remote := <-received
	if remote.TraceID() != parent.SpanContext().TraceID() || remote.SpanID() == parent.SpanContext().SpanID() || !remote.IsRemote() {
		t.Fatalf("bad propagation: %v", remote)
	}
	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].SpanKind != trace.SpanKindClient || spans[0].Parent.SpanID() != parent.SpanContext().SpanID() {
		t.Fatalf("bad client span: %+v", spans)
	}
}

func TestOTLPExportOnShutdownAfterCancellation(t *testing.T) {
	var mu sync.Mutex
	var traces tracepb.ExportTraceServiceRequest
	var metrics metricpb.ExportMetricsServiceRequest
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		mu.Lock()
		defer mu.Unlock()
		var err error
		switch r.URL.Path {
		case "/v1/traces":
			err = proto.Unmarshal(data, &traces)
		case "/v1/metrics":
			err = proto.Unmarshal(data, &metrics)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	var cfg appconfig.AppConfig
	cfg.Telemetry.Enabled = true
	cfg.Telemetry.MetricIntervalMS = 3600000
	cfg.Kubernetes.PodUID = "test-pod-uid"
	state := appstate.New()
	ctx, cancel := context.WithCancel(context.Background())
	p, err := New(ctx, cfg, &state.Runtime)
	if err != nil {
		t.Fatal(err)
	}
	_, span := p.Traces.Tracer("test").Start(ctx, "request")
	span.End()
	state.Runtime.RecordReadinessProbe(false)
	// Pulling the second reader must not consume/reset the OTLP reader's data.
	scrape := httptest.NewRecorder()
	p.MetricsHandler.ServeHTTP(scrape, httptest.NewRequest("GET", "/metrics", nil))
	if scrape.Code != http.StatusOK {
		t.Fatalf("scrape failed: %s", scrape.Body.String())
	}
	cancel()
	if err := p.Shutdown(2 * time.Second); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(traces.ResourceSpans) != 1 || len(metrics.ResourceMetrics) != 1 {
		t.Fatalf("missing final export: %v %v", &traces, &metrics)
	}
	for _, res := range metrics.ResourceMetrics {
		attrs := map[string]string{}
		for _, a := range res.Resource.Attributes {
			attrs[a.Key] = a.Value.GetStringValue()
		}
		if attrs["service.name"] != ServiceName || attrs["service.instance.id"] != "test-pod-uid" || attrs["k8s.pod.uid"] != "test-pod-uid" {
			t.Fatal(attrs)
		}
		if len(res.ScopeMetrics) == 0 || len(res.ScopeMetrics[0].Metrics[0].GetSum().DataPoints) != 6 {
			t.Fatal("missing probe series")
		}
		var total int64
		for _, point := range res.ScopeMetrics[0].Metrics[0].GetSum().DataPoints {
			total += point.GetAsInt()
		}
		if total != 1 {
			t.Fatalf("scrape changed OTLP counters: %d", total)
		}
	}
}

func TestCollectorFailureDoesNotBlockRequestsOrShutdown(t *testing.T) {
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(503) }))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	var cfg appconfig.AppConfig
	cfg.Telemetry.Enabled = true
	cfg.Telemetry.MetricIntervalMS = 3600000
	p, err := New(context.Background(), cfg, &appstate.New().Runtime)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, span := p.Traces.Tracer("test").Start(context.Background(), "request")
	span.End()
	if time.Since(start) > time.Second {
		t.Fatal("span end blocked on export")
	}
	start = time.Now()
	if err := p.Shutdown(100 * time.Millisecond); err == nil {
		t.Fatal("expected export failure")
	}
	if time.Since(start) > time.Second {
		t.Fatal("shutdown exceeded bounded window")
	}
}
