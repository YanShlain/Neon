package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"neon/internal/app"
	"neon/internal/infrastructure/memory"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	ctx := context.Background()
	application, err := app.BootstrapApp(ctx, memory.DefaultSeedConfig())
	if err != nil {
		slog.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}
	defer application.Close()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	slog.Info("worker running", "task_queue", "booking-task-queue")
	<-stop
}
