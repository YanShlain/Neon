package booking

import (
	"os"
	"time"
)

const (
	WorkflowName         = "BookingWorkflow"
	TaskQueue            = "booking-task-queue"
	Namespace            = "flight-booking"
	QueryGetStatus       = "GetStatus"
	UpdateUpdateSeats   = "UpdateSeats"
	UpdateCancelOrder   = "CancelOrder"
	UpdateSubmitPayment = "SubmitPayment"
)

// HoldDuration returns hold timer length (15m default, overridable via HOLD_DURATION).
func HoldDuration() time.Duration {
	if raw := os.Getenv("HOLD_DURATION"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return 15 * time.Minute
}
