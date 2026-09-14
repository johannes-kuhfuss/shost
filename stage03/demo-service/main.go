package main

import (
	"context"
	"demo-service/app"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := app.StartApp(ctx); err != nil {
		log.Fatal(err)
	}
}
