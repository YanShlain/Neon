package memory

import (
	"fmt"
	"sort"
	"time"

	"neon/domain"
)

const (
	defaultRows    = 10
	defaultColumns = 6
)

var columnLetters = []string{"A", "B", "C", "D", "E", "F"}

// SeedConfig controls flight seed data for development and tests.
type SeedConfig struct {
	FlightIDs   []string
	Rows        int
	Columns     int
	BaseDeparture time.Time
}

// DefaultSeedConfig returns seed settings with flights 101 and 102.
func DefaultSeedConfig() SeedConfig {
	return SeedConfig{
		FlightIDs:     []string{"101", "102"},
		Rows:          defaultRows,
		Columns:       defaultColumns,
		BaseDeparture: time.Now().Add(24 * time.Hour).Truncate(time.Minute),
	}
}

// Seed initializes flight and seat repositories with at least two flights.
func Seed(flights domain.FlightRepository, seats domain.SeatRepository, cfg SeedConfig) error {
	if len(cfg.FlightIDs) < 2 {
		return fmt.Errorf("seed requires at least 2 flights, got %d", len(cfg.FlightIDs))
	}
	if cfg.Rows <= 0 || cfg.Columns <= 0 || cfg.Columns > len(columnLetters) {
		return fmt.Errorf("invalid seat grid: rows=%d columns=%d", cfg.Rows, cfg.Columns)
	}

	flightRepo, ok := flights.(*FlightRepository)
	if !ok {
		return fmt.Errorf("Seed requires *memory.FlightRepository")
	}
	seatRepo, ok := seats.(*SeatRepository)
	if !ok {
		return fmt.Errorf("Seed requires *memory.SeatRepository")
	}

	capacity := cfg.Rows * cfg.Columns
	for i, flightID := range cfg.FlightIDs {
		departure := cfg.BaseDeparture.Add(time.Duration(i+1) * time.Hour)
		flightRepo.add(&domain.Flight{
			ID:          flightID,
			DepartureAt: departure,
			Capacity:    capacity,
		})
		if err := seatRepo.initFlight(flightID, cfg.Rows, cfg.Columns); err != nil {
			return err
		}
	}
	return nil
}

// GenerateSeatIDs builds seat identifiers for a rows-by-columns grid.
func GenerateSeatIDs(rows, columns int) []string {
	ids := make([]string, 0, rows*columns)
	for row := 1; row <= rows; row++ {
		for col := 0; col < columns; col++ {
			ids = append(ids, fmt.Sprintf("%d%s", row, columnLetters[col]))
		}
	}
	sort.Strings(ids)
	return ids
}
