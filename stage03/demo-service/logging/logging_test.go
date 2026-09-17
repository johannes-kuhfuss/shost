package logging

import (
	"bytes"
	"encoding/json"
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

func TestInvalidConfiguration(t *testing.T) {
	for _, config := range [][2]string{{"xml", "info"}, {"text", "verbose"}} {
		if _, err := New(&bytes.Buffer{}, config[0], config[1]); err == nil {
			t.Fatalf("accepted invalid configuration: %v", config)
		}
	}
}
