package main

import (
	"context"
	"demo-service/app"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	os.Exit(run())
}

func run() int {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.StartApp(ctx); err != nil {
		slog.ErrorContext(ctx, "Application failed", "error", err)
		return 1
	}
	return 0
}
