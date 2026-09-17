// Package logging configures output; application code uses log/slog directly.
package logging

import (
	"fmt"
	"io"
	"log/slog"
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
	return slog.New(handler).With("service.name", "demo-service"), nil
}
