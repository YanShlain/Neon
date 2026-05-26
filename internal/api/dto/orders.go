package dto

// CreateOrderRequest starts a booking for a flight.
type CreateOrderRequest struct {
	FlightID string `json:"flight_id" binding:"required"`
}

// UpdateSeatsRequest changes held seats on an order.
type UpdateSeatsRequest struct {
	SeatIDs []string `json:"seat_ids" binding:"required"`
}

// SubmitPaymentRequest submits a 5-digit payment code.
type SubmitPaymentRequest struct {
	Code string `json:"code" binding:"required"`
}

// PaymentEventResponse mirrors workflow payment audit entries.
type PaymentEventResponse struct {
	Type    string `json:"type"`
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// OrderResponse is returned by order endpoints.
type OrderResponse struct {
	OrderID               string                 `json:"order_id"`
	FlightID              string                 `json:"flight_id"`
	Status                string                 `json:"status"`
	HeldSeatIDs           []string               `json:"held_seat_ids"`
	TimerRemainingSeconds int                    `json:"timer_remaining_seconds"`
	PaymentEvents         []PaymentEventResponse `json:"payment_events,omitempty"`
	PaymentFailures       int                    `json:"payment_failures,omitempty"`
}
