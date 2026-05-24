package dto

import "time"

// FlightResponse is the API representation of a flight.
type FlightResponse struct {
	ID          string    `json:"id"`
	DepartureAt time.Time `json:"departure_at"`
	Capacity    int       `json:"capacity"`
}

// SeatResponse is the API representation of a seat on the map.
type SeatResponse struct {
	SeatID  string `json:"seat_id"`
	Status  string `json:"status"`
	OrderID string `json:"order_id,omitempty"`
	IsMine  bool   `json:"is_mine"`
}

// SeatMapResponse is returned by GET /flights/{id}/seats.
type SeatMapResponse struct {
	FlightID string         `json:"flight_id"`
	Seats    []SeatResponse `json:"seats"`
}
