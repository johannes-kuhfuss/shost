package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"go.opentelemetry.io/otel/trace"
	"strings"
	"testing"
)

func TestOutputAndLevelFiltering(t *testing.T) {
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			var output bytes.Buffer
			logger, err := New(&output, format, "warn")
			if err != nil {
				t.Fatal(err)
			}
			logger.Info("filtered")
			logger.Warn("Certificate reload failed", "attempt", 2)
			if strings.Contains(output.String(), "filtered") {
				t.Fatal("info record passed warn threshold")
			}
			if format == "json" {
				var record map[string]any
				if err := json.Unmarshal(output.Bytes(), &record); err != nil {
					t.Fatal(err)
				}
				if record["msg"] != "Certificate reload failed" || record["level"] != "WARN" || record["attempt"] != float64(2) || record["service.name"] != "demo-service" {
					t.Fatalf("unexpected record: %v", record)
				}
			} else if !strings.Contains(output.String(), `msg="Certificate reload failed"`) || !strings.Contains(output.String(), "attempt=2") {
				t.Fatalf("unexpected text output: %s", output.String())
			}
		})
	}
}

func TestTraceFieldsStayAtRootAndDoNotLeak(t *testing.T) {
	var output bytes.Buffer
	logger, _ := New(&output, "json", "info")
	id, _ := trace.TraceIDFromHex("0123456789abcdef0123456789abcdef")
	span, _ := trace.SpanIDFromHex("0123456789abcdef")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{TraceID: id, SpanID: span, TraceFlags: trace.FlagsSampled}))
	child := logger.With("version", "test").WithGroup("details").With("item", 7)
	child.InfoContext(ctx, "request")
	child.Info("background")
	decoder := json.NewDecoder(&output)
	var request, background map[string]any
	if err := decoder.Decode(&request); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(&background); err != nil {
		t.Fatal(err)
	}
	if request["trace_id"] != id.String() || request["span_id"] != span.String() || request["trace_sampled"] != true {
		t.Fatal(request)
	}
	if request["details"].(map[string]any)["item"] != float64(7) {
		t.Fatal(request)
	}
	if _, present := background["trace_id"]; present {
		t.Fatal("trace context leaked into background log")
	}
}

func TestInvalidConfiguration(t *testing.T) {
	for _, config := range [][2]string{{"xml", "info"}, {"text", "verbose"}} {
		if _, err := New(&bytes.Buffer{}, config[0], config[1]); err == nil {
			t.Fatalf("accepted invalid configuration: %v", config)
		}
	}
}
