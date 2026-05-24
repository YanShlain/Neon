package main

import (
	"log/slog"
	"net/http"
	"os"

	"neon/internal/app"
	"neon/internal/infrastructure/memory"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	repos, err := app.Bootstrap(memory.DefaultSeedConfig())
	if err != nil {
		slog.Error("bootstrap failed", "error", err)
		os.Exit(1)
	}

	router := app.NewRouter(repos)
	addr := envOrDefault("API_ADDR", ":8080")
	slog.Info("starting api server", "addr", addr)
	if err := http.ListenAndServe(addr, router); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
