package memory

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"neon/domain"
)

var (
	ErrFlightNotFound   = errors.New("flight not found")
	ErrSeatNotFound     = errors.New("seat not found")
	ErrHoldConflict     = errors.New("seat hold conflict")
	ErrInvalidRelease   = errors.New("seat not held by order")
	ErrInvalidConfirm   = errors.New("seat not held by order for confirm")
)

// SeatRepository is an in-memory SeatRepository with per-flight locking.
type SeatRepository struct {
	mu       sync.RWMutex
	flights  map[string]map[string]domain.Seat
	flightMu map[string]*sync.Mutex
}

// NewSeatRepository creates an empty seat repository.
func NewSeatRepository() *SeatRepository {
	return &SeatRepository{
		flights:  make(map[string]map[string]domain.Seat),
		flightMu: make(map[string]*sync.Mutex),
	}
}

func (r *SeatRepository) initFlight(flightID string, rows, columns int) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.flights[flightID]; exists {
		return fmt.Errorf("flight %q already initialized", flightID)
	}

	seats := make(map[string]domain.Seat, rows*columns)
	for _, seatID := range GenerateSeatIDs(rows, columns) {
		seats[seatID] = domain.Seat{
			FlightID: flightID,
			SeatID:   seatID,
			Status:   domain.SeatStatusAvailable,
		}
	}
	r.flights[flightID] = seats
	r.flightMu[flightID] = &sync.Mutex{}
	return nil
}

func (r *SeatRepository) flightLock(flightID string) (*sync.Mutex, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	mu, ok := r.flightMu[flightID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFlightNotFound, flightID)
	}
	return mu, nil
}

// ListByFlight returns all seats for a flight sorted by seat ID.
func (r *SeatRepository) ListByFlight(_ context.Context, flightID string) ([]domain.Seat, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	seats, ok := r.flights[flightID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFlightNotFound, flightID)
	}

	out := make([]domain.Seat, 0, len(seats))
	for _, seat := range seats {
		out = append(out, seat)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SeatID < out[j].SeatID
	})
	return out, nil
}

// TryHold marks seats as HELD for the given order when all are available.
func (r *SeatRepository) TryHold(ctx context.Context, flightID string, seatIDs []string, orderID string) error {
	mu, err := r.flightLock(flightID)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()

	r.mu.RLock()
	seats, ok := r.flights[flightID]
	if !ok {
		r.mu.RUnlock()
		return fmt.Errorf("%w: %s", ErrFlightNotFound, flightID)
	}

	for _, seatID := range seatIDs {
		seat, found := seats[seatID]
		if !found {
			r.mu.RUnlock()
			return fmt.Errorf("%w: %s", ErrSeatNotFound, seatID)
		}
		if seat.Status != domain.SeatStatusAvailable {
			r.mu.RUnlock()
			return ErrHoldConflict
		}
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	for _, seatID := range seatIDs {
		seat := r.flights[flightID][seatID]
		seat.Status = domain.SeatStatusHeld
		seat.OrderID = orderID
		r.flights[flightID][seatID] = seat
	}
	return nil
}

// Release returns held seats to AVAILABLE for the given order.
func (r *SeatRepository) Release(ctx context.Context, flightID string, seatIDs []string, orderID string) error {
	mu, err := r.flightLock(flightID)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	seats, ok := r.flights[flightID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrFlightNotFound, flightID)
	}

	for _, seatID := range seatIDs {
		seat, found := seats[seatID]
		if !found {
			return fmt.Errorf("%w: %s", ErrSeatNotFound, seatID)
		}
		if seat.Status != domain.SeatStatusHeld || seat.OrderID != orderID {
			return ErrInvalidRelease
		}
		seat.Status = domain.SeatStatusAvailable
		seat.OrderID = ""
		seats[seatID] = seat
	}
	return nil
}

// Confirm transitions held seats to BOOKED for the given order.
func (r *SeatRepository) Confirm(ctx context.Context, flightID string, seatIDs []string, orderID string) error {
	mu, err := r.flightLock(flightID)
	if err != nil {
		return err
	}
	mu.Lock()
	defer mu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	seats, ok := r.flights[flightID]
	if !ok {
		return fmt.Errorf("%w: %s", ErrFlightNotFound, flightID)
	}

	for _, seatID := range seatIDs {
		seat, found := seats[seatID]
		if !found {
			return fmt.Errorf("%w: %s", ErrSeatNotFound, seatID)
		}
		if seat.Status != domain.SeatStatusHeld || seat.OrderID != orderID {
			return ErrInvalidConfirm
		}
		seat.Status = domain.SeatStatusBooked
		seats[seatID] = seat
	}
	return nil
}
