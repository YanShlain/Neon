package booking

import (
	"errors"
	"time"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"

	"neon/domain"
)

const (
	activityTimeout        = 30 * time.Second
	paymentActivityTimeout = 10 * time.Second
)

func activityCtx(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: activityTimeout,
	})
}

func paymentActivityCtx(ctx workflow.Context) workflow.Context {
	return workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: paymentActivityTimeout,
	})
}

type workflowState struct {
	OrderID         string
	FlightID        string
	HoldDuration    time.Duration
	Status          domain.OrderStatus
	HeldSeatIDs     []string
	TimerDeadline   time.Time
	CurrentCode     string
	PaymentFailures int
	PaymentEvents   []PaymentEvent
	LastError       string
}

// BookingWorkflow orchestrates seat holds, payment, and the hold timer.
func BookingWorkflow(ctx workflow.Context, input WorkflowInput) error {
	// --- Init state ---
	state := workflowState{
		OrderID:      input.OrderID,
		FlightID:     input.FlightID,
		HoldDuration: input.HoldDuration,
		Status:       domain.OrderStatusCreated,
	}
	if state.HoldDuration <= 0 {
		state.HoldDuration = 15 * time.Minute
	}
	state.TimerDeadline = workflow.Now(ctx).Add(state.HoldDuration)

	actCtx := activityCtx(ctx)
	resetCh := workflow.NewBufferedChannel(ctx, 1)
	paymentCh := workflow.GetSignalChannel(ctx, SignalSubmitPayment)

	notifyTimerReset := func() {
		resetCh.SendAsync(true)
	}

	if err := workflow.SetQueryHandler(ctx, QueryGetStatus, func() (StatusResponse, error) {
		return state.toResponse(workflow.Now(ctx)), nil
	}); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandler(ctx, UpdateUpdateSeats, func(updateCtx workflow.Context, req UpdateSeatsRequest) (StatusResponse, error) {
		if state.Status.IsTerminal() {
			return StatusResponse{}, temporal.NewApplicationError("order terminal", "terminal_order")
		}
		if state.Status == domain.OrderStatusAwaitingPayment {
			return StatusResponse{}, temporal.NewApplicationError("payment in progress", "payment_in_progress")
		}
		if err := applySeatUpdate(activityCtx(updateCtx), &state, req.SeatIDs); err != nil {
			return StatusResponse{}, err
		}
		notifyTimerReset()
		return state.toResponse(workflow.Now(ctx)), nil
	}); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandler(ctx, UpdateCancelOrder, func(updateCtx workflow.Context) (StatusResponse, error) {
		if state.Status.IsTerminal() {
			return state.toResponse(workflow.Now(ctx)), nil
		}
		if err := releaseHeldSeats(activityCtx(updateCtx), &state); err != nil {
			return StatusResponse{}, err
		}
		state.Status = domain.OrderStatusCancelled
		state.TimerDeadline = time.Time{}
		notifyTimerReset()
		return state.toResponse(workflow.Now(ctx)), nil
	}); err != nil {
		return err
	}

	var paymentFuture workflow.Future

	// --- Selector loop until terminal ---
	for !state.Status.IsTerminal() {
		deadline := state.TimerDeadline
		var timerCtx workflow.Context
		var timerCancel workflow.CancelFunc
		var timerFuture workflow.Future
		if !deadline.IsZero() {
			timerCtx, timerCancel = workflow.WithCancel(ctx)
			remaining := deadline.Sub(workflow.Now(ctx))
			if remaining <= 0 {
				timerCancel()
				if err := handleTimerExpiry(actCtx, ctx, &state, paymentFuture); err != nil {
					return err
				}
				paymentFuture = nil
				continue
			}
			timerFuture = workflow.NewTimer(timerCtx, remaining)
		}

		expired := false
		reset := false
		paymentReceived := false
		var paymentReq SubmitPaymentRequest
		paymentDone := false

		selector := workflow.NewSelector(ctx)
		if timerFuture != nil {
			selector.AddFuture(timerFuture, func(f workflow.Future) {
				_ = f.Get(timerCtx, nil)
				if state.TimerDeadline.Equal(deadline) && !state.Status.IsTerminal() {
					expired = true
				}
			})
		}
		selector.AddReceive(resetCh, func(c workflow.ReceiveChannel, more bool) {
			var ignored bool
			c.Receive(ctx, &ignored)
			reset = true
		})
		selector.AddReceive(paymentCh, func(c workflow.ReceiveChannel, more bool) {
			c.Receive(ctx, &paymentReq)
			paymentReceived = true
		})
		if paymentFuture != nil {
			selector.AddFuture(paymentFuture, func(f workflow.Future) {
				_ = f.Get(ctx, nil)
				paymentDone = true
			})
		}
		selector.Select(ctx)
		if timerCancel != nil {
			timerCancel()
		}

		if reset {
			continue
		}
		if paymentReceived {
			paymentFuture = startPaymentValidation(actCtx, paymentActivityCtx(ctx), &state, paymentReq)
			continue
		}
		if paymentDone {
			if state.Status.IsTerminal() {
				paymentFuture = nil
				continue
			}
			if err := completePaymentValidation(actCtx, &state, paymentFuture); err != nil {
				return err
			}
			paymentFuture = nil
			continue
		}
		if expired {
			if err := handleTimerExpiry(actCtx, ctx, &state, paymentFuture); err != nil {
				return err
			}
			paymentFuture = nil
		}
	}
	return nil
}

func handleTimerExpiry(actCtx, ctx workflow.Context, state *workflowState, paymentFuture workflow.Future) error {
	if paymentFuture != nil && state.Status == domain.OrderStatusAwaitingPayment {
		if err := rejectInFlightPayment(actCtx, state); err != nil {
			return err
		}
	}
	return expireOrder(actCtx, state)
}

func rejectInFlightPayment(ctx workflow.Context, state *workflowState) error {
	code := state.CurrentCode
	if err := workflow.ExecuteActivity(ctx, (*Activities).RejectInFlightPayment, PaymentValidationInput{
		Code: code,
	}).Get(ctx, nil); err != nil {
		return err
	}
	state.Status = domain.OrderStatusSeatsHeld
	state.appendPaymentEvent(PaymentEvent{
		Type:    PaymentEventRejectedByTimer,
		Code:    code,
		Message: "payment rejected because hold timer expired",
	})
	state.LastError = ""
	return nil
}

func startPaymentValidation(actCtx, payCtx workflow.Context, state *workflowState, req SubmitPaymentRequest) workflow.Future {
	state.LastError = ""

	if state.Status.IsTerminal() {
		state.LastError = "order terminal"
		return nil
	}

	if state.Status != domain.OrderStatusSeatsHeld {
		state.appendPaymentEvent(PaymentEvent{
			Type:    PaymentEventFormatInvalid,
			Code:    req.Code,
			Message: "payment not allowed in current status",
		})
		state.LastError = "payment not allowed"
		return nil
	}

	if !isValidPaymentCode(req.Code) {
		state.appendPaymentEvent(PaymentEvent{
			Type:    PaymentEventFormatInvalid,
			Code:    req.Code,
			Message: "payment code must be exactly 5 digits",
		})
		state.LastError = "invalid payment code format"
		return nil
	}

	state.CurrentCode = req.Code
	state.Status = domain.OrderStatusAwaitingPayment
	return workflow.ExecuteActivity(payCtx, (*Activities).ValidatePayment, PaymentValidationInput{Code: req.Code})
}

func completePaymentValidation(ctx workflow.Context, state *workflowState, paymentFuture workflow.Future) error {
	if paymentFuture == nil || state.Status.IsTerminal() {
		return nil
	}

	err := paymentFuture.Get(ctx, nil)
	if err == nil {
		if err := workflow.ExecuteActivity(ctx, (*Activities).ConfirmSeats, SeatMutationInput{
			FlightID: state.FlightID,
			SeatIDs:  cloneStrings(state.HeldSeatIDs),
			OrderID:  state.OrderID,
		}).Get(ctx, nil); err != nil {
			state.Status = domain.OrderStatusSeatsHeld
			state.appendPaymentEvent(PaymentEvent{
				Type:    PaymentEventValidationFailed,
				Code:    state.CurrentCode,
				Message: "seat confirmation failed",
			})
			state.LastError = "seat confirmation failed"
			return err
		}
		state.Status = domain.OrderStatusConfirmed
		state.TimerDeadline = time.Time{}
		state.appendPaymentEvent(PaymentEvent{
			Type: PaymentEventValidationSuccess,
			Code: state.CurrentCode,
		})
		state.LastError = ""
		return nil
	}

	state.Status = domain.OrderStatusSeatsHeld
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) && appErr.Type() == "invalid_payment_code" {
		state.appendPaymentEvent(PaymentEvent{
			Type:    PaymentEventFormatInvalid,
			Code:    state.CurrentCode,
			Message: "invalid payment code format",
		})
		state.LastError = "invalid payment code format"
		return nil
	}

	state.PaymentFailures++
	state.appendPaymentEvent(PaymentEvent{
		Type:    PaymentEventValidationFailed,
		Code:    state.CurrentCode,
		Message: "payment validation failed",
	})
	if state.PaymentFailures >= maxFailuresPerCode {
		return failOrderPaymentExhausted(ctx, state, state.CurrentCode)
	}
	state.LastError = "payment validation failed"
	return nil
}

func failOrderPaymentExhausted(ctx workflow.Context, state *workflowState, code string) error {
	if err := releaseHeldSeats(ctx, state); err != nil {
		return err
	}
	state.Status = domain.OrderStatusPaymentFailed
	state.TimerDeadline = time.Time{}
	state.LastError = "payment attempts exhausted"
	state.appendPaymentEvent(PaymentEvent{
		Type:    PaymentEventAttemptsExhausted,
		Code:    code,
		Message: "all payment attempts exhausted",
	})
	return nil
}

func (s *workflowState) appendPaymentEvent(ev PaymentEvent) {
	s.PaymentEvents = append(s.PaymentEvents, ev)
}

func expireOrder(ctx workflow.Context, state *workflowState) error {
	if err := releaseHeldSeats(ctx, state); err != nil {
		return err
	}
	state.Status = domain.OrderStatusExpired
	state.TimerDeadline = time.Time{}
	return nil
}

func applySeatUpdate(ctx workflow.Context, state *workflowState, seatIDs []string) error {
	state.LastError = ""

	if len(state.HeldSeatIDs) > 0 {
		if err := workflow.ExecuteActivity(ctx, (*Activities).ReleaseSeats, SeatMutationInput{
			FlightID: state.FlightID,
			SeatIDs:  cloneStrings(state.HeldSeatIDs),
			OrderID:  state.OrderID,
		}).Get(ctx, nil); err != nil {
			return err
		}
	}

	if len(seatIDs) > 0 {
		if err := workflow.ExecuteActivity(ctx, (*Activities).HoldSeats, SeatMutationInput{
			FlightID: state.FlightID,
			SeatIDs:  seatIDs,
			OrderID:  state.OrderID,
		}).Get(ctx, nil); err != nil {
			return err
		}
	}

	state.HeldSeatIDs = cloneStrings(seatIDs)
	state.Status = domain.OrderStatusSeatsHeld
	state.TimerDeadline = workflow.Now(ctx).Add(state.HoldDuration)
	return nil
}

func releaseHeldSeats(ctx workflow.Context, state *workflowState) error {
	if len(state.HeldSeatIDs) == 0 {
		return nil
	}
	err := workflow.ExecuteActivity(ctx, (*Activities).ReleaseSeats, SeatMutationInput{
		FlightID: state.FlightID,
		SeatIDs:  cloneStrings(state.HeldSeatIDs),
		OrderID:  state.OrderID,
	}).Get(ctx, nil)
	if err != nil {
		return err
	}
	state.HeldSeatIDs = nil
	return nil
}

func (s workflowState) toResponse(now time.Time) StatusResponse {
	return StatusResponse{
		OrderID:               s.OrderID,
		FlightID:              s.FlightID,
		Status:                s.Status,
		HeldSeatIDs:           cloneStrings(s.HeldSeatIDs),
		TimerRemainingSeconds: timerRemaining(s.TimerDeadline, now),
		PaymentEvents:         clonePaymentEvents(s.PaymentEvents),
		PaymentFailures:       s.PaymentFailures,
		LastError:             s.LastError,
	}
}

func clonePaymentEvents(in []PaymentEvent) []PaymentEvent {
	if len(in) == 0 {
		return nil
	}
	out := make([]PaymentEvent, len(in))
	copy(out, in)
	return out
}
