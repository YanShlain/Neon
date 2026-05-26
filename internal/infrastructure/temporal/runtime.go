package temporal

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/worker"

	"neon/domain"
	"neon/internal/workflow/booking"
)

// Runtime holds Temporal client and worker wired to repositories.
type Runtime struct {
	Client     client.Client
	Worker     worker.Worker
	stopDevSrv func() error
}

// ConnectOptions configures Temporal connectivity.
type ConnectOptions struct {
	HostPort  string
	Namespace string
}

// Connect dials Temporal or starts an embedded dev server when TEMPORAL_AUTO_DEV=1.
func Connect(ctx context.Context, opts ConnectOptions) (*Runtime, error) {
	if opts.HostPort == "" {
		opts.HostPort = envOrDefault("TEMPORAL_HOST", "127.0.0.1:7233")
	}
	if opts.Namespace == "" {
		opts.Namespace = booking.Namespace
	}

	c, err := client.Dial(client.Options{
		HostPort:  opts.HostPort,
		Namespace: opts.Namespace,
	})
	if err == nil {
		return &Runtime{Client: c}, nil
	}

	if os.Getenv("TEMPORAL_AUTO_DEV") != "1" {
		return nil, fmt.Errorf("dial temporal at %s: %w (set TEMPORAL_AUTO_DEV=1 to embed dev server)", opts.HostPort, err)
	}

	slog.Info("starting embedded temporal dev server")
	dev, stopDev, err := startDevServer(ctx, opts.Namespace)
	if err != nil {
		return nil, err
	}
	return &Runtime{Client: dev, stopDevSrv: stopDev}, nil
}

// StartWorker registers workflows and activities against shared repositories.
func (r *Runtime) StartWorker(seats domain.SeatRepository) worker.Worker {
	w := worker.New(r.Client, booking.TaskQueue, worker.Options{})
	w.RegisterWorkflow(booking.BookingWorkflow)
	w.RegisterActivity(&booking.Activities{
		Seats:      seats,
		PaymentRNG: booking.PaymentRNGFromEnv(),
	})
	r.Worker = w
	return w
}

// Close shuts down Temporal resources.
func (r *Runtime) Close() {
	if r.Worker != nil {
		r.Worker.Stop()
	}
	if r.Client != nil {
		r.Client.Close()
	}
	if r.stopDevSrv != nil {
		_ = r.stopDevSrv()
	}
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
