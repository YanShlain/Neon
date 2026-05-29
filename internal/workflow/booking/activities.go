package booking

import (
	"context"
	"errors"
	"os"
	"time"

	"go.temporal.io/sdk/temporal"

	"neon/domain"
)

// Activities perform seat mutations and payment simulation for the booking workflow.
type Activities struct {
	Seats      domain.SeatRepository
	PaymentRNG PaymentRNG
}

func (a *Activities) paymentRNG() PaymentRNG {
	if a.PaymentRNG != nil {
		return a.PaymentRNG
	}
	return defaultPaymentRNG{}
}

// SeatMutationInput identifies seats to change for an order on a flight.
type SeatMutationInput struct {
	FlightID string
	SeatIDs  []string
	OrderID  string
}

// HoldSeats marks seats as held for an order.
func (a *Activities) HoldSeats(ctx context.Context, in SeatMutationInput) error {
	if err := a.Seats.TryHold(ctx, in.FlightID, in.SeatIDs, in.OrderID); err != nil {
		if errors.Is(err, domain.ErrHoldConflict) {
			return temporal.NewNonRetryableApplicationError("seat hold conflict", "hold_conflict", err)
		}
		return err
	}
	return nil
}

// ReleaseSeats releases held seats for an order.
func (a *Activities) ReleaseSeats(ctx context.Context, in SeatMutationInput) error {
	if len(in.SeatIDs) == 0 {
		return nil
	}
	return a.Seats.Release(ctx, in.FlightID, in.SeatIDs, in.OrderID)
}

// ValidatePayment checks a 5-digit code and simulates gateway validation (10s, 15% failure).
func (a *Activities) ValidatePayment(ctx context.Context, in PaymentValidationInput) error {
	if !isValidPaymentCode(in.Code) {
		return temporal.NewNonRetryableApplicationError("invalid payment code format", "invalid_payment_code", nil)
	}
	if raw := os.Getenv("PAYMENT_VALIDATION_DELAY"); raw != "" {
		if delay, err := time.ParseDuration(raw); err == nil && delay > 0 {
			time.Sleep(delay)
		}
	}
	if simulatePaymentFailure(a.paymentRNG()) {
		return temporal.NewNonRetryableApplicationError("payment validation failed", "payment_validation_failed", nil)
	}
	return nil
}

// RejectInFlightPayment simulates refund when the hold timer wins over in-flight validation (S-4).
func (a *Activities) RejectInFlightPayment(ctx context.Context, in PaymentValidationInput) error {
	if !isValidPaymentCode(in.Code) {
		return temporal.NewNonRetryableApplicationError("invalid payment code format", "invalid_payment_code", nil)
	}
	return nil
}

// ConfirmSeats transitions held seats to BOOKED for an order.
func (a *Activities) ConfirmSeats(ctx context.Context, in SeatMutationInput) error {
	if len(in.SeatIDs) == 0 {
		return nil
	}
	if err := a.Seats.Confirm(ctx, in.FlightID, in.SeatIDs, in.OrderID); err != nil {
		if errors.Is(err, domain.ErrInvalidConfirm) {
			return temporal.NewNonRetryableApplicationError("seat confirm failed", "seat_confirm_failed", err)
		}
		return err
	}
	return nil
}
