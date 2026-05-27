package api_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"neon/internal/infrastructure/memory"
)

// I-A1: Server with seed — GET /flights — ≥2 flights
func TestI_A1_GetFlightsReturnsAtLeastTwo(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/api/v1/flights")
	if err != nil {
		t.Fatalf("GET /flights: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Flights []struct {
			ID string `json:"id"`
		} `json:"flights"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Flights) < 2 {
		t.Fatalf("flights count = %d, want >= 2", len(body.Flights))
	}
}

// I-A2: Server with seed — GET /flights/101/seats — Full grid, all AVAILABLE
func TestI_A2_GetSeatMapAllAvailable(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/api/v1/flights/101/seats")
	if err != nil {
		t.Fatalf("GET seats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		FlightID string `json:"flight_id"`
		Seats    []struct {
			SeatID string `json:"seat_id"`
			Status string `json:"status"`
		} `json:"seats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.FlightID != "101" {
		t.Fatalf("flight_id = %q, want 101", body.FlightID)
	}

	expectedCount := len(memory.GenerateSeatIDs(10, 6))
	if len(body.Seats) != expectedCount {
		t.Fatalf("seats count = %d, want %d", len(body.Seats), expectedCount)
	}
	for _, seat := range body.Seats {
		if seat.Status != "AVAILABLE" {
			t.Fatalf("seat %s status = %q, want AVAILABLE", seat.SeatID, seat.Status)
		}
	}
}

// I-A3: 1A HELD on 101 in repo — GET /flights/101/seats — 1A HELD
func TestI_A3_GetSeatMapShowsHeldSeat(t *testing.T) {
	srv, seats := newTestServer(t)
	ctx := context.Background()

	if err := seats.TryHold(ctx, "101", []string{"1A"}, "O1"); err != nil {
		t.Fatalf("TryHold: %v", err)
	}

	resp, err := http.Get(srv.URL + "/api/v1/flights/101/seats")
	if err != nil {
		t.Fatalf("GET seats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	var body struct {
		Seats []struct {
			SeatID  string `json:"seat_id"`
			Status  string `json:"status"`
			OrderID string `json:"order_id"`
		} `json:"seats"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var seat1A *struct {
		SeatID  string `json:"seat_id"`
		Status  string `json:"status"`
		OrderID string `json:"order_id"`
	}
	for i := range body.Seats {
		if body.Seats[i].SeatID == "1A" {
			seat1A = &body.Seats[i]
			break
		}
	}
	if seat1A == nil {
		t.Fatal("seat 1A not found in response")
	}
	if seat1A.Status != "HELD" {
		t.Fatalf("1A status = %q, want HELD", seat1A.Status)
	}
	if seat1A.OrderID != "O1" {
		t.Fatalf("1A order_id = %q, want O1", seat1A.OrderID)
	}
}

// I-A4: Server with UI embedded — GET / — 200; flight list HTML served
func TestI_A4_GetRootServesFlightListUI(t *testing.T) {
	srv, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if ct != "text/html; charset=utf-8" {
		t.Fatalf("content-type = %q, want text/html; charset=utf-8", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	html := string(body)
	if !containsAll(html, "Neon Air", "flights.js", "flight-grid") {
		t.Fatalf("unexpected HTML body: %q", html)
	}
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}
