// Package logging configures output; application code uses log/slog directly.
package logging

import (
	"context"
	"fmt"
	"go.opentelemetry.io/otel/trace"
	"io"
	"log/slog"
	"slices"
	"strings"
)

func New(w io.Writer, format, level string) (*slog.Logger, error) {
	var severity slog.Level
	if err := severity.UnmarshalText([]byte(level)); err != nil {
		return nil, fmt.Errorf("invalid LOG_LEVEL %q: %w", level, err)
	}
	opts := &slog.HandlerOptions{Level: severity}
	var handler slog.Handler
	switch strings.ToLower(format) {
	case "text":
		handler = slog.NewTextHandler(w, opts)
	case "json":
		handler = slog.NewJSONHandler(w, opts)
	default:
		return nil, fmt.Errorf("invalid LOG_FORMAT %q: use text or json", format)
	}
	return slog.New(&traceHandler{Handler: handler}).With("service.name", "demo-service"), nil
}

// Keep correlation fields at the root even when application code uses WithGroup.
type traceHandler struct {
	slog.Handler
	ops []func(slog.Handler) slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, record slog.Record) error {
	inner := h.Handler
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		inner = inner.WithAttrs([]slog.Attr{slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()), slog.Bool("trace_sampled", sc.IsSampled())})
	}
	for _, op := range h.ops {
		inner = op(inner)
	}
	return inner.Handle(ctx, record)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	attrs = slices.Clone(attrs)
	return &traceHandler{Handler: h.Handler, ops: append(slices.Clone(h.ops), func(inner slog.Handler) slog.Handler { return inner.WithAttrs(attrs) })}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler, ops: append(slices.Clone(h.ops), func(inner slog.Handler) slog.Handler { return inner.WithGroup(name) })}
}
