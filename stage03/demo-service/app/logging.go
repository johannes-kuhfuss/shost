package app

import (
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"
)

func requestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		status := c.Writer.Status()
		level := slog.LevelInfo
		if status >= 500 {
			level = slog.LevelError
		} else if status >= 400 {
			level = slog.LevelWarn
		}
		// Use the route template, avoiding query strings and user-supplied paths.
		logger.LogAttrs(c.Request.Context(), level, "HTTP request completed",
			slog.String("http.request.method", c.Request.Method),
			slog.String("http.route", c.FullPath()),
			slog.Int("http.response.status_code", status),
			slog.Duration("duration", time.Since(start)),
		)
	}
}

func recoveryLogger(logger *slog.Logger) gin.HandlerFunc {
	// Retain Gin's handling of disconnected clients, but replace its text dump
	// (which includes request headers) with a structured panic record.
	return gin.CustomRecoveryWithWriter(io.Discard, func(c *gin.Context, recovered any) {
		logger.ErrorContext(c.Request.Context(), "HTTP handler panicked",
			"error", recovered,
			"exception.stacktrace", string(debug.Stack()),
			"http.request.method", c.Request.Method,
			"http.route", c.FullPath(),
		)
		c.AbortWithStatus(http.StatusInternalServerError)
	})
}
