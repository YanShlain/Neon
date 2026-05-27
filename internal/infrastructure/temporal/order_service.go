package temporal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/api/serviceerror"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"

	"neon/domain"
	"neon/internal/workflow/booking"
)

// OrderService starts and controls booking workflows from the presentation layer.
type OrderService struct {
	client client.Client
}

// NewOrderService creates an OrderService.
func NewOrderService(c client.Client) *OrderService {
	return &OrderService{client: c}
}

// CreateOrder starts a new booking workflow for a flight.
func (s *OrderService) CreateOrder(ctx context.Context, flightID string) (booking.StatusResponse, error) {
	orderID := uuid.NewString()
	slog.Info("outbound temporal StartWorkflow",
		"workflow", booking.WorkflowName,
		"order_id", orderID,
		"flight_id", flightID,
	)

	_, err := s.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        orderID,
		TaskQueue: booking.TaskQueue,
	}, booking.BookingWorkflow, booking.WorkflowInput{
		OrderID:      orderID,
		FlightID:     flightID,
		HoldDuration: booking.HoldDuration(),
	})
	if err != nil {
		slog.Error("StartWorkflow failed", "order_id", orderID, "error", err, "exc_info", err)
		return booking.StatusResponse{}, fmt.Errorf("start workflow: %w", err)
	}

	return s.GetStatus(ctx, orderID)
}

// UpdateSeats synchronously updates held seats via workflow update.
func (s *OrderService) UpdateSeats(ctx context.Context, orderID string, seatIDs []string) (booking.StatusResponse, error) {
	slog.Info("outbound temporal UpdateWorkflow",
		"update", booking.UpdateUpdateSeats,
		"order_id", orderID,
		"seat_ids", seatIDs,
	)

	handle, err := s.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   orderID,
		UpdateName:   booking.UpdateUpdateSeats,
		WaitForStage: client.WorkflowUpdateStageCompleted,
		Args:         []interface{}{booking.UpdateSeatsRequest{SeatIDs: seatIDs}},
	})
	if err != nil {
		slog.Error("UpdateWorkflow failed", "order_id", orderID, "error", err, "exc_info", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}

	var resp booking.StatusResponse
	if err := handle.Get(ctx, &resp); err != nil {
		slog.Error("UpdateWorkflow result failed", "order_id", orderID, "error", err, "exc_info", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}
	return resp, nil
}

// CancelOrder cancels an active order and releases held seats.
func (s *OrderService) CancelOrder(ctx context.Context, orderID string) (booking.StatusResponse, error) {
	slog.Info("outbound temporal UpdateWorkflow",
		"update", booking.UpdateCancelOrder,
		"order_id", orderID,
	)

	handle, err := s.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   orderID,
		UpdateName:   booking.UpdateCancelOrder,
		WaitForStage: client.WorkflowUpdateStageCompleted,
	})
	if err != nil {
		slog.Error("CancelOrder update failed", "order_id", orderID, "error", err, "exc_info", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}

	var resp booking.StatusResponse
	if err := handle.Get(ctx, &resp); err != nil {
		return booking.StatusResponse{}, mapTemporalError(err)
	}
	return resp, nil
}

// SubmitPayment signals payment validation and waits for workflow processing.
func (s *OrderService) SubmitPayment(ctx context.Context, orderID string, code string) (booking.StatusResponse, error) {
	before, err := s.GetStatus(ctx, orderID)
	if err != nil {
		return booking.StatusResponse{}, err
	}
	if before.Status.IsTerminal() {
		if before.Status == domain.OrderStatusConfirmed {
			return before, ErrPaymentNotAllowed
		}
		return before, ErrTerminalOrder
	}
	if before.Status != domain.OrderStatusSeatsHeld {
		return before, ErrPaymentNotAllowed
	}
	beforeEvents := len(before.PaymentEvents)

	slog.Info("outbound temporal SignalWorkflow",
		"signal", booking.SignalSubmitPayment,
		"order_id", orderID,
	)

	if err := s.client.SignalWorkflow(ctx, orderID, "", booking.SignalSubmitPayment, booking.SubmitPaymentRequest{
		Code: code,
	}); err != nil {
		slog.Error("SignalWorkflow failed", "order_id", orderID, "error", err, "exc_info", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}

	deadline := time.Now().Add(12 * time.Second)
	var last booking.StatusResponse
	for time.Now().Before(deadline) {
		last, err = s.GetStatus(ctx, orderID)
		if err != nil {
			return booking.StatusResponse{}, err
		}
		if paymentProcessingSettled(before, last, beforeEvents) {
			return last, mapPaymentResultError(last)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return last, fmt.Errorf("payment processing timeout")
}

// StartNewPaymentMethod signals the workflow to begin a new payment method slot.
func (s *OrderService) StartNewPaymentMethod(ctx context.Context, orderID string) (booking.StatusResponse, error) {
	before, err := s.GetStatus(ctx, orderID)
	if err != nil {
		return booking.StatusResponse{}, err
	}
	if before.Status.IsTerminal() {
		return before, ErrTerminalOrder
	}
	if before.Status != domain.OrderStatusSeatsHeld {
		return before, ErrNewMethodNotAllowed
	}
	beforeEvents := len(before.PaymentEvents)
	beforeMethods := before.MethodsUsed

	slog.Info("outbound temporal SignalWorkflow",
		"signal", booking.SignalStartNewPaymentMethod,
		"order_id", orderID,
	)

	if err := s.client.SignalWorkflow(ctx, orderID, "", booking.SignalStartNewPaymentMethod, struct{}{}); err != nil {
		slog.Error("SignalWorkflow failed", "order_id", orderID, "error", err, "exc_info", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	var last booking.StatusResponse
	for time.Now().Before(deadline) {
		last, err = s.GetStatus(ctx, orderID)
		if err != nil {
			return booking.StatusResponse{}, err
		}
		if newMethodProcessingSettled(before, last, beforeEvents, beforeMethods) {
			return last, mapNewMethodResultError(last)
		}
		time.Sleep(25 * time.Millisecond)
	}
	return last, fmt.Errorf("new payment method processing timeout")
}

func newMethodProcessingSettled(before, after booking.StatusResponse, beforeEvents, beforeMethods int) bool {
	if after.MethodsUsed > beforeMethods {
		return true
	}
	if len(after.PaymentEvents) > beforeEvents {
		return true
	}
	if after.LastError != "" && after.LastError != before.LastError {
		return true
	}
	return false
}

func mapNewMethodResultError(status booking.StatusResponse) error {
	if status.Status.IsTerminal() {
		return ErrTerminalOrder
	}
	if err := mapNewMethodStatusError(status.LastError); err != nil {
		return err
	}
	if len(status.PaymentEvents) == 0 {
		return nil
	}
	switch status.PaymentEvents[len(status.PaymentEvents)-1].Type {
	case booking.PaymentEventMethodsExhausted:
		return ErrMethodsExhausted
	case booking.PaymentEventNewMethodNotAllowed:
		return ErrNewMethodNotAllowed
	default:
		return nil
	}
}

func mapNewMethodStatusError(lastError string) error {
	switch lastError {
	case "payment methods exhausted":
		return ErrMethodsExhausted
	case "new payment method not allowed", "payment in progress", "order terminal":
		return ErrNewMethodNotAllowed
	default:
		return nil
	}
}

func paymentProcessingSettled(before, after booking.StatusResponse, beforeEvents int) bool {
	if after.Status == domain.OrderStatusConfirmed {
		return true
	}
	if after.Status.IsTerminal() {
		return true
	}
	if after.Status == domain.OrderStatusAwaitingPayment {
		return false
	}
	if len(after.PaymentEvents) > beforeEvents {
		return true
	}
	if after.Status != before.Status {
		return true
	}
	return false
}

func mapPaymentResultError(status booking.StatusResponse) error {
	if status.Status == domain.OrderStatusPaymentFailed {
		return ErrTerminalOrder
	}
	if status.Status.IsTerminal() && status.Status != domain.OrderStatusConfirmed {
		return ErrTerminalOrder
	}
	if err := mapPaymentStatusError(status.LastError); err != nil {
		return err
	}
	if len(status.PaymentEvents) == 0 {
		return nil
	}
	switch status.PaymentEvents[len(status.PaymentEvents)-1].Type {
	case booking.PaymentEventFormatInvalid:
		return ErrInvalidPaymentCode
	case booking.PaymentEventAttemptsExhausted:
		return ErrPaymentAttemptsExhausted
	case booking.PaymentEventMethodsExhausted:
		if status.Status == domain.OrderStatusPaymentFailed {
			return ErrTerminalOrder
		}
		return ErrMethodsExhausted
	case booking.PaymentEventMethodChangeRequired:
		return ErrDifferentPaymentMethodRequired
	default:
		return nil
	}
}

func mapPaymentStatusError(lastError string) error {
	switch lastError {
	case "invalid payment code format":
		return ErrInvalidPaymentCode
	case "payment attempts exhausted":
		return ErrPaymentAttemptsExhausted
	case "payment not allowed":
		return ErrPaymentNotAllowed
	case "different payment method required":
		return ErrDifferentPaymentMethodRequired
	case "payment methods exhausted":
		return ErrMethodsExhausted
	default:
		return nil
	}
}

// GetStatus queries workflow state.
func (s *OrderService) GetStatus(ctx context.Context, orderID string) (booking.StatusResponse, error) {
	slog.Info("outbound temporal QueryWorkflow",
		"query", booking.QueryGetStatus,
		"order_id", orderID,
	)

	resp, err := s.client.QueryWorkflow(ctx, orderID, "", booking.QueryGetStatus)
	if err != nil {
		slog.Error("QueryWorkflow failed", "order_id", orderID, "error", err, "exc_info", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}

	var status booking.StatusResponse
	if err := resp.Get(&status); err != nil {
		return booking.StatusResponse{}, fmt.Errorf("decode query: %w", err)
	}
	return status, nil
}

func mapTemporalError(err error) error {
	var appErr *temporal.ApplicationError
	if errors.As(err, &appErr) {
		switch appErr.Type() {
		case "hold_conflict":
			return ErrHoldConflict
		case "terminal_order":
			return ErrTerminalOrder
		}
	}
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return ErrOrderNotFound
	}
	return err
}

// ErrHoldConflict indicates a seat is already held by another order.
var ErrHoldConflict = errors.New("seat hold conflict")

// ErrOrderNotFound indicates the workflow does not exist.
var ErrOrderNotFound = errors.New("order not found")

// ErrTerminalOrder indicates the order is in a terminal state.
var ErrTerminalOrder = errors.New("order is terminal")

// ErrInvalidPaymentCode indicates the payment code format is invalid.
var ErrInvalidPaymentCode = errors.New("invalid payment code")

// ErrPaymentAttemptsExhausted indicates too many failures for the current code.
var ErrPaymentAttemptsExhausted = errors.New("payment attempts exhausted")

// ErrPaymentNotAllowed indicates payment cannot be submitted in the current order state.
var ErrPaymentNotAllowed = errors.New("payment not allowed")

// ErrDifferentPaymentMethodRequired indicates a different code was submitted without starting a new method.
var ErrDifferentPaymentMethodRequired = errors.New("different payment method required")

// ErrNewMethodNotAllowed indicates StartNewPaymentMethod cannot be applied in the current state.
var ErrNewMethodNotAllowed = errors.New("new payment method not allowed")

// ErrMethodsExhausted indicates all payment method slots are used or the order failed payment.
var ErrMethodsExhausted = errors.New("payment methods exhausted")

// DescribeOrderStatus is a helper for HTTP mapping.
func DescribeOrderStatus(status domain.OrderStatus) string {
	return string(status)
}

// WorkflowExecutionRunning checks whether a workflow exists and is running.
func WorkflowExecutionRunning(ctx context.Context, c client.Client, orderID string) (bool, error) {
	desc, err := c.DescribeWorkflowExecution(ctx, orderID, "")
	if err != nil {
		var notFound *serviceerror.NotFound
		if errors.As(err, &notFound) {
			return false, ErrOrderNotFound
		}
		return false, err
	}
	return desc.WorkflowExecutionInfo.Status == enumspb.WORKFLOW_EXECUTION_STATUS_RUNNING, nil
}
