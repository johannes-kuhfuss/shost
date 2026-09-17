package app

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type contextHandler struct {
	slog.Handler
	contexts []context.Context
}

func (h *contextHandler) Handle(ctx context.Context, record slog.Record) error {
	h.contexts = append(h.contexts, ctx)
	return h.Handler.Handle(ctx, record)
}

func TestRequestLoggingAndRecovery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, panics := range []bool{false, true} {
		var output bytes.Buffer
		handler := &contextHandler{Handler: slog.NewJSONHandler(&output, nil)}
		logger := slog.New(handler)
		router := gin.New()
		router.Use(requestLogger(logger), recoveryLogger(logger))
		router.GET("/items/:id", func(c *gin.Context) {
			if panics {
				panic("test panic")
			}
			c.Status(204)
		})
		type contextKey struct{}
		ctx := context.WithValue(context.Background(), contextKey{}, "request context")
		request := httptest.NewRequest("GET", "/items/private-id?token=secret", nil).WithContext(ctx)
		request.Header.Set("Authorization", "private-credential")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		wantStatus, wantLevel, wantCount := 204, "INFO", 1
		if panics {
			wantStatus, wantLevel, wantCount = 500, "ERROR", 2
		}
		if response.Code != wantStatus {
			t.Fatalf("status = %d, want %d", response.Code, wantStatus)
		}
		if len(handler.contexts) != wantCount {
			t.Fatalf("records = %d, want %d", len(handler.contexts), wantCount)
		}
		for _, got := range handler.contexts {
			if got != ctx {
				t.Fatal("request context lost")
			}
		}
		for _, secret := range []string{"private-id", "token=secret", "private-credential"} {
			if strings.Contains(output.String(), secret) {
				t.Fatalf("logged request data: %s", secret)
			}
		}
		lines := strings.Split(strings.TrimSpace(output.String()), "\n")
		var record map[string]any
		if err := json.Unmarshal([]byte(lines[len(lines)-1]), &record); err != nil {
			t.Fatal(err)
		}
		if record["http.route"] != "/items/:id" || record["http.response.status_code"] != float64(wantStatus) || record["level"] != wantLevel {
			t.Fatalf("unexpected access record: %v", record)
		}
	}
}
