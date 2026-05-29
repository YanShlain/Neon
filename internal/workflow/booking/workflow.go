package booking

import (
	"errors"
	"fmt"
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
	OrderID             string
	FlightID            string
	HoldDuration        time.Duration
	Status              domain.OrderStatus
	HeldSeatIDs         []string
	TimerDeadline       time.Time
	CurrentCode         string
	CurrentCodeFailures int
	MethodsUsed         int
	PaymentFailures     int
	PaymentEvents       []PaymentEvent
	LastError           string
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

	notifyTimerReset := func() {
		resetCh.SendAsync(true)
	}

	if err := workflow.SetQueryHandler(ctx, QueryGetStatus, func() (StatusResponse, error) {
		return state.toResponse(workflow.Now(ctx)), nil
	}); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandler(ctx, UpdateUpdateSeats,
		func(updateCtx workflow.Context, req UpdateSeatsRequest) (StatusResponse, error) {
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
		},
	); err != nil {
		return err
	}

	if err := workflow.SetUpdateHandler(ctx, UpdateCancelOrder,
		func(updateCtx workflow.Context) (StatusResponse, error) {
			if state.Status.IsTerminal() {
				return state.toResponse(workflow.Now(ctx)), nil
			}
			if state.Status == domain.OrderStatusAwaitingPayment {
				return StatusResponse{}, temporal.NewApplicationError("payment in progress", "payment_in_progress")
			}
			if err := releaseHeldSeats(activityCtx(updateCtx), &state); err != nil {
				return StatusResponse{}, err
			}
			state.Status = domain.OrderStatusCancelled
			state.TimerDeadline = time.Time{}
			notifyTimerReset()
			return state.toResponse(workflow.Now(ctx)), nil
		},
	); err != nil {
		return err
	}

	// Validator runs before the workflow task is created, allowing cheap early rejection.
	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateStartNewPaymentMethod,
		func(updateCtx workflow.Context) (StatusResponse, error) {
			state.MethodsUsed++
			if state.MethodsUsed >= maxPaymentMethods {
				if failErr := failOrderPaymentExhausted(activityCtx(updateCtx), &state, state.CurrentCode); failErr != nil {
					return StatusResponse{}, failErr
				}
				notifyTimerReset()
				return state.toResponse(workflow.Now(ctx)), nil
			}
			code := state.CurrentCode
			state.CurrentCode = ""
			state.CurrentCodeFailures = 0
			state.LastError = ""
			state.appendPaymentEvent(PaymentEvent{
				Type:    PaymentEventNewMethodStarted,
				Code:    code,
				Message: "switched to a new payment method",
			})
			notifyTimerReset()
			return state.toResponse(workflow.Now(ctx)), nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(_ workflow.Context) error {
				if state.Status.IsTerminal() {
					return temporal.NewApplicationError("order terminal", "terminal_order")
				}
				if state.Status != domain.OrderStatusSeatsHeld {
					return temporal.NewApplicationError("payment not allowed", "payment_not_allowed")
				}
				if state.CurrentCode == "" {
					return temporal.NewApplicationError("no active payment method", "new_method_not_needed")
				}
				return nil
			},
		},
	); err != nil {
		return err
	}

	// Validator rejects invalid state and bad codes before a workflow task is created.
	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateSubmitPayment,
		func(updateCtx workflow.Context, req SubmitPaymentRequest) (StatusResponse, error) {
			state.CurrentCode = req.Code
			state.Status = domain.OrderStatusAwaitingPayment
			state.LastError = ""

			err := workflow.ExecuteActivity(paymentActivityCtx(updateCtx),
				(*Activities).ValidatePayment,
				PaymentValidationInput{Code: req.Code},
			).Get(updateCtx, nil)

			// Timer may have fired and set a terminal status while the activity ran.
			if state.Status.IsTerminal() {
				notifyTimerReset()
				return state.toResponse(workflow.Now(ctx)), nil
			}

			if err == nil {
				confirmErr := workflow.ExecuteActivity(activityCtx(updateCtx), (*Activities).ConfirmSeats, SeatMutationInput{
					FlightID: state.FlightID,
					SeatIDs:  cloneStrings(state.HeldSeatIDs),
					OrderID:  state.OrderID,
				}).Get(updateCtx, nil)
				if confirmErr != nil {
					// CancelOrder may have fired concurrently; never overwrite a terminal state.
					if !state.Status.IsTerminal() {
						state.Status = domain.OrderStatusSeatsHeld
					}
					state.appendPaymentEvent(PaymentEvent{
						Type:    PaymentEventValidationFailed,
						Code:    state.CurrentCode,
						Message: "seat confirmation failed",
					})
					state.LastError = "seat confirmation failed"
					notifyTimerReset()
					return state.toResponse(workflow.Now(ctx)), confirmErr
				}
				if state.Status.IsTerminal() {
					notifyTimerReset()
					return state.toResponse(workflow.Now(ctx)), nil
				}
				state.Status = domain.OrderStatusConfirmed
				state.TimerDeadline = time.Time{}
				state.appendPaymentEvent(PaymentEvent{
					Type: PaymentEventValidationSuccess,
					Code: state.CurrentCode,
				})
				state.LastError = ""
				notifyTimerReset()
				return state.toResponse(workflow.Now(ctx)), nil
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
				notifyTimerReset()
				return state.toResponse(workflow.Now(ctx)), nil
			}

			state.PaymentFailures++
			state.CurrentCodeFailures++
			state.appendPaymentEvent(PaymentEvent{
				Type:    PaymentEventValidationFailed,
				Code:    state.CurrentCode,
				Message: "payment validation failed",
			})

			if state.CurrentCodeFailures >= maxAttemptsPerMethod {
				code := state.CurrentCode
				state.MethodsUsed++
				state.CurrentCodeFailures = 0
				state.CurrentCode = ""
				if state.MethodsUsed >= maxPaymentMethods {
					if failErr := failOrderPaymentExhausted(activityCtx(updateCtx), &state, code); failErr != nil {
						return StatusResponse{}, failErr
					}
					notifyTimerReset()
					return state.toResponse(workflow.Now(ctx)), nil
				}
				state.appendPaymentEvent(PaymentEvent{
					Type:    PaymentEventAttemptsExhausted,
					Code:    code,
					Message: fmt.Sprintf("code %s exhausted (%d/%d methods used)", code, state.MethodsUsed, maxPaymentMethods),
				})
			}

			state.LastError = "payment validation failed"
			notifyTimerReset()
			return state.toResponse(workflow.Now(ctx)), nil
		},
		workflow.UpdateHandlerOptions{
			Validator: func(_ workflow.Context, req SubmitPaymentRequest) error {
				if state.Status.IsTerminal() {
					if state.Status == domain.OrderStatusConfirmed {
						return temporal.NewApplicationError("payment not allowed", "payment_not_allowed")
					}
					return temporal.NewApplicationError("order terminal", "terminal_order")
				}
				if state.Status != domain.OrderStatusSeatsHeld {
					return temporal.NewApplicationError("payment not allowed", "payment_not_allowed")
				}
				if !domain.IsValidPaymentCode(req.Code) {
					return temporal.NewApplicationError("invalid code", "invalid_payment_code")
				}
				if state.CurrentCode != "" && req.Code != state.CurrentCode {
					return temporal.NewApplicationError("new payment method required", "new_method_required")
				}
				return nil
			},
		},
	); err != nil {
		return err
	}

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
				if err := handleTimerExpiry(actCtx, &state); err != nil {
					return err
				}
				continue
			}
			timerFuture = workflow.NewTimer(timerCtx, remaining)
		}

		expired := false
		reset := false

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
		selector.Select(ctx)
		if timerCancel != nil {
			timerCancel()
		}

		if reset {
			continue
		}
		if expired {
			if err := handleTimerExpiry(actCtx, &state); err != nil {
				return err
			}
		}
	}
	return nil
}

func handleTimerExpiry(actCtx workflow.Context, state *workflowState) error {
	if state.Status == domain.OrderStatusAwaitingPayment {
		state.appendPaymentEvent(PaymentEvent{
			Type:    PaymentEventRejectedByTimer,
			Code:    state.CurrentCode,
			Message: "payment rejected because hold timer expired",
		})
		state.CurrentCode = ""
		state.LastError = ""
	}
	return expireOrder(actCtx, state)
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

	releaseIDs := cloneStrings(state.HeldSeatIDs)
	holdIDs := cloneStrings(seatIDs)

	if len(releaseIDs) > 0 || len(holdIDs) > 0 {
		if err := workflow.ExecuteActivity(ctx, (*Activities).SwapSeats, SeatSwapInput{
			FlightID:   state.FlightID,
			OrderID:    state.OrderID,
			ReleaseIDs: releaseIDs,
			HoldIDs:    holdIDs,
		}).Get(ctx, nil); err != nil {
			return err
		}
	}

	state.HeldSeatIDs = cloneStrings(seatIDs)
	if len(seatIDs) == 0 {
		state.Status = domain.OrderStatusCreated
		state.TimerDeadline = workflow.Now(ctx).Add(state.HoldDuration)
	} else {
		state.Status = domain.OrderStatusSeatsHeld
		state.TimerDeadline = workflow.Now(ctx).Add(state.HoldDuration)
	}
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
	methodsRemaining := maxPaymentMethods - s.MethodsUsed
	if methodsRemaining < 0 {
		methodsRemaining = 0
	}
	return StatusResponse{
		OrderID:               s.OrderID,
		FlightID:              s.FlightID,
		Status:                s.Status,
		HeldSeatIDs:           cloneStrings(s.HeldSeatIDs),
		TimerRemainingSeconds: timerRemaining(s.TimerDeadline, now),
		PaymentEvents:         clonePaymentEvents(s.PaymentEvents),
		PaymentFailures:       s.PaymentFailures,
		MethodsUsed:           s.MethodsUsed,
		MethodsRemaining:      methodsRemaining,
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
