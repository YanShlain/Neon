package memory

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"neon/domain"
)

// FlightRepository is an in-memory FlightRepository implementation.
type FlightRepository struct {
	mu      sync.RWMutex
	flights map[string]*domain.Flight
}

// NewFlightRepository creates an empty flight repository.
func NewFlightRepository() *FlightRepository {
	return &FlightRepository{
		flights: make(map[string]*domain.Flight),
	}
}

func (r *FlightRepository) add(flight *domain.Flight) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flights[flight.ID] = flight
}

// Get returns a flight by ID.
func (r *FlightRepository) Get(_ context.Context, flightID string) (*domain.Flight, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	flight, ok := r.flights[flightID]
	if !ok {
		return nil, fmt.Errorf("flight %q not found", flightID)
	}
	copy := *flight
	return &copy, nil
}

// List returns all flights sorted by departure time.
func (r *FlightRepository) List(_ context.Context) ([]domain.Flight, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	out := make([]domain.Flight, 0, len(r.flights))
	for _, f := range r.flights {
		out = append(out, *f)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].DepartureAt.Before(out[j].DepartureAt)
	})
	return out, nil
}
