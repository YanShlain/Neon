package app

import (
	"fmt"
	"log/slog"

	"neon/domain"
	"neon/internal/infrastructure/memory"
)

// Repos holds wired repository instances for the application.
type Repos struct {
	Flights domain.FlightRepository
	Seats   domain.SeatRepository
}

// Bootstrap validates configuration, seeds inventory, and returns repositories.
func Bootstrap(seedCfg memory.SeedConfig) (*Repos, error) {
	flights := memory.NewFlightRepository()
	seats := memory.NewSeatRepository()
	if err := memory.Seed(flights, seats, seedCfg); err != nil {
		return nil, fmt.Errorf("seed inventory: %w", err)
	}
	slog.Info("inventory seeded", "flights", len(seedCfg.FlightIDs))
	return &Repos{Flights: flights, Seats: seats}, nil
}
