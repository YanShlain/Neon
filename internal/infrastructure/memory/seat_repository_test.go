package memory_test

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"neon/domain"
	"neon/internal/infrastructure/memory"
)

func newTestRepos(t *testing.T) (domain.FlightRepository, domain.SeatRepository) {
	t.Helper()
	flights := memory.NewFlightRepository()
	seats := memory.NewSeatRepository()
	if err := memory.Seed(flights, seats, memory.DefaultSeedConfig()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return flights, seats
}

func findSeat(seats []domain.Seat, seatID string) (domain.Seat, bool) {
	for _, s := range seats {
		if s.SeatID == seatID {
			return s, true
		}
	}
	return domain.Seat{}, false
}

// U-A1: Flight Flight1ID has seat 1A — List seats — 1A AVAILABLE
func TestU_A1_ListSeatsFlight101Seat1AAvailable(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	got, err := seats.ListByFlight(ctx, memory.Flight1ID)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}

	seat, ok := findSeat(got, "1A")
	if !ok {
		t.Fatalf("seat 1A not found on flight %s", memory.Flight1ID)
	}
	if seat.Status != domain.SeatStatusAvailable {
		t.Fatalf("status = %q, want AVAILABLE", seat.Status)
	}
}

// U-A2: 1A held on Flight1ID — List seats for Flight2ID — 1A on Flight2ID AVAILABLE
func TestU_A2_HeldOn101DoesNotAffect102(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A"}, "O1"); err != nil {
		t.Fatalf("TryHold: %v", err)
	}

	got, err := seats.ListByFlight(ctx, memory.Flight2ID)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}

	seat, ok := findSeat(got, "1A")
	if !ok {
		t.Fatalf("seat 1A not found on flight %s", memory.Flight2ID)
	}
	if seat.Status != domain.SeatStatusAvailable {
		t.Fatalf("status = %q, want AVAILABLE", seat.Status)
	}
}

// U-A3: All seats available on Flight1ID — TryHold 1A,1B for O1 — Both HELD under O1
func TestU_A3_TryHoldMultipleSeats(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A", "1B"}, "O1"); err != nil {
		t.Fatalf("TryHold: %v", err)
	}

	got, err := seats.ListByFlight(ctx, memory.Flight1ID)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}

	for _, id := range []string{"1A", "1B"} {
		seat, ok := findSeat(got, id)
		if !ok {
			t.Fatalf("seat %s not found", id)
		}
		if seat.Status != domain.SeatStatusHeld {
			t.Fatalf("seat %s status = %q, want HELD", id, seat.Status)
		}
		if seat.OrderID != "O1" {
			t.Fatalf("seat %s order_id = %q, want O1", id, seat.OrderID)
		}
	}
}

// U-A4: 1A HELD by O1 — TryHold 1A for O2 — Fails
func TestU_A4_TryHoldConflictWhenHeldByAnotherOrder(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A"}, "O1"); err != nil {
		t.Fatalf("TryHold O1: %v", err)
	}

	err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A"}, "O2")
	if err == nil {
		t.Fatal("expected TryHold to fail for O2")
	}
	if err != memory.ErrHoldConflict {
		t.Fatalf("error = %v, want ErrHoldConflict", err)
	}
}

// U-A5: 1A,1B HELD by O1 — Release O1 — AVAILABLE
func TestU_A5_ReleaseHeldSeats(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A", "1B"}, "O1"); err != nil {
		t.Fatalf("TryHold: %v", err)
	}
	if err := seats.Release(ctx, memory.Flight1ID, []string{"1A", "1B"}, "O1"); err != nil {
		t.Fatalf("Release: %v", err)
	}

	got, err := seats.ListByFlight(ctx, memory.Flight1ID)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}

	for _, id := range []string{"1A", "1B"} {
		seat, ok := findSeat(got, id)
		if !ok {
			t.Fatalf("seat %s not found", id)
		}
		if seat.Status != domain.SeatStatusAvailable {
			t.Fatalf("seat %s status = %q, want AVAILABLE", id, seat.Status)
		}
	}
}

// U-A6: Seats HELD by O1 — Confirm O1 — BOOKED
func TestU_A6_ConfirmHeldSeats(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A", "1B"}, "O1"); err != nil {
		t.Fatalf("TryHold: %v", err)
	}
	if err := seats.Confirm(ctx, memory.Flight1ID, []string{"1A", "1B"}, "O1"); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	got, err := seats.ListByFlight(ctx, memory.Flight1ID)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}

	for _, id := range []string{"1A", "1B"} {
		seat, ok := findSeat(got, id)
		if !ok {
			t.Fatalf("seat %s not found", id)
		}
		if seat.Status != domain.SeatStatusBooked {
			t.Fatalf("seat %s status = %q, want BOOKED", id, seat.Status)
		}
		if seat.OrderID != "O1" {
			t.Fatalf("seat %s order_id = %q, want O1", id, seat.OrderID)
		}
	}
}

// U-A8: Release is idempotent when seats are already available.
func TestU_A8_ReleaseIdempotent(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A"}, "O1"); err != nil {
		t.Fatalf("TryHold: %v", err)
	}
	if err := seats.Release(ctx, memory.Flight1ID, []string{"1A"}, "O1"); err != nil {
		t.Fatalf("first Release: %v", err)
	}
	if err := seats.Release(ctx, memory.Flight1ID, []string{"1A"}, "O1"); err != nil {
		t.Fatalf("second Release: %v", err)
	}

	got, err := seats.ListByFlight(ctx, memory.Flight1ID)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}
	seat, ok := findSeat(got, "1A")
	if !ok {
		t.Fatal("seat 1A not found")
	}
	if seat.Status != domain.SeatStatusAvailable {
		t.Fatalf("status = %q, want AVAILABLE", seat.Status)
	}
}

// U-A7: SwapHold rolls back when new hold conflicts — prior holds remain.
func TestU_A7_SwapHoldRollbackOnConflict(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1A"}, "O1"); err != nil {
		t.Fatalf("TryHold O1: %v", err)
	}
	if err := seats.TryHold(ctx, memory.Flight1ID, []string{"1B"}, "O2"); err != nil {
		t.Fatalf("TryHold O2: %v", err)
	}

	err := seats.SwapHold(ctx, memory.Flight1ID, "O1", []string{"1A"}, []string{"1B"})
	if err == nil {
		t.Fatal("expected SwapHold conflict")
	}
	if err != memory.ErrHoldConflict {
		t.Fatalf("error = %v, want ErrHoldConflict", err)
	}

	got, err := seats.ListByFlight(ctx, memory.Flight1ID)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}
	for _, id := range []string{"1A", "1B"} {
		seat, ok := findSeat(got, id)
		if !ok {
			t.Fatalf("seat %s not found", id)
		}
		wantOrder := "O1"
		if id == "1B" {
			wantOrder = "O2"
		}
		if seat.Status != domain.SeatStatusHeld || seat.OrderID != wantOrder {
			t.Fatalf("seat %s = %+v, want HELD by %s", id, seat, wantOrder)
		}
	}
}

// U-A10: Concurrent TryHold on one seat never double-books.
func TestU_A10_ConcurrentTryHoldSingleSeat(t *testing.T) {
	_, seats := newTestRepos(t)
	ctx := context.Background()

	const (
		flight = memory.Flight1ID
		seat   = "1A"
	)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = seats.TryHold(ctx, flight, []string{seat}, fmt.Sprintf("O%d", id))
		}(i)
	}
	wg.Wait()

	got, err := seats.ListByFlight(ctx, flight)
	if err != nil {
		t.Fatalf("ListByFlight: %v", err)
	}
	holders := 0
	for _, s := range got {
		if s.SeatID == seat && s.Status == domain.SeatStatusHeld {
			holders++
		}
	}
	if holders != 1 {
		t.Fatalf("held count = %d, want 1", holders)
	}
}
