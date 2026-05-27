package booking

import (
	"time"

	"neon/domain"
)

// WorkflowInput is passed when starting BookingWorkflow.
type WorkflowInput struct {
	OrderID      string
	FlightID     string
	HoldDuration time.Duration
}

// PaymentEventType categorizes payment lifecycle entries exposed via GetStatus.
type PaymentEventType string

const (
	PaymentEventFormatInvalid         PaymentEventType = "format_invalid"
	PaymentEventAttemptsExhausted     PaymentEventType = "attempts_exhausted"
	PaymentEventValidationFailed      PaymentEventType = "validation_failed"
	PaymentEventValidationSuccess     PaymentEventType = "validation_success"
	PaymentEventMethodChangeRequired  PaymentEventType = "method_change_required"
	PaymentEventNewMethodStarted      PaymentEventType = "new_method_started"
	PaymentEventMethodsExhausted      PaymentEventType = "methods_exhausted"
	PaymentEventNewMethodNotAllowed   PaymentEventType = "new_method_not_allowed"
	PaymentEventRejectedByTimer       PaymentEventType = "rejected_by_timer"
)

// PaymentEvent is an append-only audit entry for payment attempts.
type PaymentEvent struct {
	Type    PaymentEventType `json:"type"`
	Code    string           `json:"code,omitempty"`
	Message string           `json:"message,omitempty"`
}

// StatusResponse is returned by GetStatus query and order API responses.
type StatusResponse struct {
	OrderID               string              `json:"order_id"`
	FlightID              string              `json:"flight_id"`
	Status                domain.OrderStatus  `json:"status"`
	HeldSeatIDs           []string            `json:"held_seat_ids"`
	TimerRemainingSeconds int                 `json:"timer_remaining_seconds"`
	PaymentEvents         []PaymentEvent      `json:"payment_events"`
	PaymentFailures       int                 `json:"payment_failures"`
	MethodsUsed           int                 `json:"methods_used"`
	MethodsRemaining      int                 `json:"methods_remaining"`
	LastError             string              `json:"-"`
}

// UpdateSeatsRequest is the payload for UpdateSeats workflow update.
type UpdateSeatsRequest struct {
	SeatIDs []string `json:"seat_ids"`
}

// SubmitPaymentRequest is the payload for SubmitPayment workflow signal.
type SubmitPaymentRequest struct {
	Code string `json:"code"`
}

// PaymentValidationInput is passed to ValidatePayment activity.
type PaymentValidationInput struct {
	Code string `json:"code"`
}

func timerRemaining(deadline time.Time, now time.Time) int {
	if deadline.IsZero() {
		return 0
	}
	remaining := int(deadline.Sub(now).Seconds())
	if remaining < 0 {
		return 0
	}
	return remaining
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}
