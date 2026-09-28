package app

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestLogPageSubmission(t *testing.T) {
	for _, severity := range []string{"debug", "info", "warn", "error"} {
		t.Run(severity, func(t *testing.T) {
			a := newTestApplication(t)
			var output bytes.Buffer
			a.log = slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
			message := "Demo <script>alert(1)</script>\nsecond line"
			response := submitLog(a, message, severity)
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/logs?sent=written" {
				t.Fatalf("submission: %d %s", response.Code, response.Body.String())
			}
			var record map[string]any
			if err := json.Unmarshal(output.Bytes(), &record); err != nil {
				t.Fatal(err)
			}
			if record["msg"] != message || record["level"] != strings.ToUpper(severity) || record["log.source"] != "web" {
				t.Fatalf("unexpected record: %v", record)
			}
			output.Reset()
			page := performRequest(a.router, response.Header().Get("Location"))
			if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Log message sent.") || output.Len() != 0 {
				t.Fatal("confirmation should render without sending another message")
			}
		})
	}
}

func TestLogPageValidationAndFiltering(t *testing.T) {
	a := newTestApplication(t)
	var output bytes.Buffer
	a.log = slog.New(slog.NewJSONHandler(&output, nil))
	page := performRequest(a.router, "/logs")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `action="/logs"`) {
		t.Fatal("missing log form")
	}
	for _, input := range []struct{ message, severity string }{
		{"", "info"}, {" \n\t", "info"}, {strings.Repeat("a", 4097), "info"},
		{"<script>alert(1)</script>", "invalid"}, {"hello", "INFO+1"},
		{strings.Repeat("a", 65537), "info"},
	} {
		response := submitLog(a, input.message, input.severity)
		if response.Code != http.StatusBadRequest || output.Len() != 0 || strings.Contains(response.Body.String(), "<script>alert(1)</script>") {
			t.Fatalf("invalid input: status %d, logs %s", response.Code, output.String())
		}
	}
	response := submitLog(a, "debug message", "debug")
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/logs?sent=filtered" || output.Len() != 0 {
		t.Fatal("filtered message should be reported without logging")
	}
}

func submitLog(a *Application, message, severity string) *httptest.ResponseRecorder {
	form := url.Values{"message": {message}, "severity": {severity}}
	request := httptest.NewRequest(http.MethodPost, "/logs", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	a.router.ServeHTTP(response, request)
	return response
}
