// Package telemetry owns the application's OpenTelemetry providers.
package telemetry

import (
	"context"
	"demo-service/appconfig"
	"demo-service/appstate"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const ServiceName = "demo-service"

type Providers struct {
	Traces  *sdktrace.TracerProvider
	Metrics *sdkmetric.MeterProvider
}

func New(ctx context.Context, cfg appconfig.AppConfig, state *appstate.RuntimeState) (*Providers, error) {
	instance := cfg.Kubernetes.PodUID
	if instance == "" {
		instance = uuid.NewString()
	}
	attrs := []attribute.KeyValue{attribute.String("service.name", ServiceName), attribute.String("service.instance.id", instance)}
	for key, value := range map[string]string{
		"k8s.pod.uid": cfg.Kubernetes.PodUID, "k8s.pod.name": cfg.Kubernetes.PodName,
		"k8s.namespace.name": cfg.Kubernetes.PodNamespace, "k8s.node.name": cfg.Kubernetes.NodeName,
	} {
		if value != "" {
			attrs = append(attrs, attribute.String(key, value))
		}
	}
	res, err := resource.New(ctx, resource.WithFromEnv(), resource.WithAttributes(attrs...))
	if err != nil {
		return nil, err
	}
	traceOptions := []sdktrace.TracerProviderOption{sdktrace.WithResource(res), sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.AlwaysSample()))}
	metricOptions := []sdkmetric.Option{sdkmetric.WithResource(res)}
	if cfg.Telemetry.Enabled {
		// HTTP exporters use standard OTEL_EXPORTER_OTLP_* environment settings.
		// Creation doesn't require a reachable collector; export happens asynchronously.
		traces, err := otlptracehttp.New(ctx)
		if err != nil {
			return nil, err
		}
		metrics, err := otlpmetrichttp.New(ctx, otlpmetrichttp.WithTemporalitySelector(sdkmetric.CumulativeTemporalitySelector))
		if err != nil {
			cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = traces.Shutdown(cleanup)
			return nil, err
		}
		traceOptions = append(traceOptions, sdktrace.WithBatcher(traces))
		metricOptions = append(metricOptions, sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics,
			sdkmetric.WithInterval(time.Duration(cfg.Telemetry.MetricIntervalMS)*time.Millisecond))))
	}
	p := &Providers{Traces: sdktrace.NewTracerProvider(traceOptions...), Metrics: sdkmetric.NewMeterProvider(metricOptions...)}
	if err := RegisterProbes(p.Metrics.Meter("demo-service/probes"), state); err != nil {
		_ = p.Shutdown(time.Second)
		return nil, err
	}
	return p, nil
}

// Shutdown is called after HTTP draining, with a fresh context even on SIGTERM.
// Both signals get the same bounded flush window; one cannot starve the other.
func (p *Providers) Shutdown(timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	results := make(chan error, 2)
	go func() { results <- p.Traces.Shutdown(ctx) }()
	go func() { results <- p.Metrics.Shutdown(ctx) }()
	return errors.Join(<-results, <-results)
}

// HTTPClient is ready for future downstream calls. Callers must build requests
// with http.NewRequestWithContext using the incoming request's context.
func (p *Providers) HTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: otelhttp.NewTransport(http.DefaultTransport,
		otelhttp.WithTracerProvider(p.Traces), otelhttp.WithMeterProvider(p.Metrics),
		otelhttp.WithPropagators(propagation.TraceContext{}))}
}

// RegisterProbes observes the same mutex-protected totals used by the UI.
func RegisterProbes(meter metric.Meter, state *appstate.RuntimeState) error {
	_, err := meter.Int64ObservableCounter("demo_service.probe_checks",
		metric.WithDescription("Probe requests handled by this process, by probe and outcome"),
		metric.WithInt64Callback(func(_ context.Context, observer metric.Int64Observer) error {
			for name, stats := range map[string]appstate.ProbeStats{
				"startup": state.StartupProbeStats(), "readiness": state.ReadinessProbeStats(), "liveness": state.LivenessProbeStats(),
			} {
				observer.Observe(int64(stats.SuccessCount), metric.WithAttributes(attribute.String("probe", name), attribute.String("outcome", "success")))
				observer.Observe(int64(stats.FailureCount), metric.WithAttributes(attribute.String("probe", name), attribute.String("outcome", "failure")))
			}
			return nil
		}))
	return err
}
