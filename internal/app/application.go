package app

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/gin-gonic/gin"

	"neon/internal/api"
	"neon/internal/infrastructure/memory"
	"neon/internal/infrastructure/temporal"
)

// Application wires repositories, Temporal, worker, and HTTP routes.
type Application struct {
	Repos  *Repos
	Temporal *temporal.Runtime
	Orders *temporal.OrderService
}

// BootstrapApp seeds inventory, connects Temporal, and starts the worker.
func BootstrapApp(ctx context.Context, seedCfg memory.SeedConfig) (*Application, error) {
	repos, err := Bootstrap(seedCfg)
	if err != nil {
		return nil, err
	}

	if os.Getenv("TEMPORAL_AUTO_DEV") == "" {
		os.Setenv("TEMPORAL_AUTO_DEV", "1")
	}

	rt, err := temporal.Connect(ctx, temporal.ConnectOptions{})
	if err != nil {
		return nil, fmt.Errorf("connect temporal: %w", err)
	}

	w := rt.StartWorker(repos.Seats)
	go func() {
		slog.Info("starting temporal worker", "task_queue", "booking-task-queue")
		if err := w.Run(nil); err != nil {
			slog.Error("worker stopped", "error", err)
		}
	}()

	return &Application{
		Repos:    repos,
		Temporal: rt,
		Orders:   temporal.NewOrderService(rt.Client),
	}, nil
}

// NewRouter builds the HTTP router.
func (a *Application) NewRouter() *gin.Engine {
	return api.NewRouter(a.Repos.Flights, a.Repos.Seats, a.Orders)
}

// Close releases runtime resources.
func (a *Application) Close() {
	if a.Temporal != nil {
		a.Temporal.Close()
	}
}
