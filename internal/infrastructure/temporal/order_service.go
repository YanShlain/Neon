package temporal

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

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
		slog.Error("StartWorkflow failed", "order_id", orderID, "error", err)
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
		slog.Error("UpdateWorkflow failed", "order_id", orderID, "error", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}

	var resp booking.StatusResponse
	if err := handle.Get(ctx, &resp); err != nil {
		slog.Error("UpdateWorkflow result failed", "order_id", orderID, "error", err)
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
		slog.Error("CancelOrder update failed", "order_id", orderID, "error", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}

	var resp booking.StatusResponse
	if err := handle.Get(ctx, &resp); err != nil {
		slog.Error("CancelOrder result failed", "order_id", orderID, "error", err)
		return booking.StatusResponse{}, mapTemporalError(err)
	}
	return resp, nil
}

// SubmitPayment validates payment synchronously via workflow update.
func (s *OrderService) SubmitPayment(ctx context.Context, orderID string, code string) (booking.StatusResponse, error) {
	slog.Info("outbound temporal UpdateWorkflow",
		"update", booking.UpdateSubmitPayment,
		"order_id", orderID,
	)

	handle, err := s.client.UpdateWorkflow(ctx, client.UpdateWorkflowOptions{
		WorkflowID:   orderID,
		UpdateName:   booking.UpdateSubmitPayment,
		WaitForStage: client.WorkflowUpdateStageCompleted,
		Args:         []interface{}{booking.SubmitPaymentRequest{Code: code}},
	})
	if err != nil {
		return s.paymentErrorWithStatus(ctx, orderID, err)
	}

	var resp booking.StatusResponse
	if err := handle.Get(ctx, &resp); err != nil {
		return s.paymentErrorWithStatus(ctx, orderID, err)
	}
	if resp.Status == domain.OrderStatusPaymentFailed {
		return resp, ErrTerminalOrder
	}
	if resp.Status.IsTerminal() && resp.Status != domain.OrderStatusConfirmed {
		return resp, ErrTerminalOrder
	}
	return resp, nil
}

func (s *OrderService) paymentErrorWithStatus(ctx context.Context, orderID string, err error) (booking.StatusResponse, error) {
	mapped := mapTemporalError(err)
	status, qerr := s.GetStatus(ctx, orderID)
	if qerr != nil {
		return booking.StatusResponse{}, mapped
	}
	if status.Status == domain.OrderStatusConfirmed {
		return status, ErrPaymentNotAllowed
	}
	if status.Status.IsTerminal() {
		return status, ErrTerminalOrder
	}
	return booking.StatusResponse{}, mapped
}

// GetStatus queries workflow state.
func (s *OrderService) GetStatus(ctx context.Context, orderID string) (booking.StatusResponse, error) {
	slog.Info("outbound temporal QueryWorkflow",
		"query", booking.QueryGetStatus,
		"order_id", orderID,
	)

	resp, err := s.client.QueryWorkflow(ctx, orderID, "", booking.QueryGetStatus)
	if err != nil {
		slog.Error("QueryWorkflow failed", "order_id", orderID, "error", err)
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
		case "payment_in_progress":
			return ErrPaymentInProgress
		case "payment_not_allowed":
			return ErrPaymentNotAllowed
		case "invalid_payment_code":
			return ErrInvalidPaymentCode
		}
	}
	var notFound *serviceerror.NotFound
	if errors.As(err, &notFound) {
		return ErrOrderNotFound
	}
	return err
}

// Sentinel errors returned by OrderService methods.
var (
	// ErrHoldConflict indicates a seat is already held by another order.
	ErrHoldConflict = errors.New("seat hold conflict")

	// ErrOrderNotFound indicates the workflow does not exist.
	ErrOrderNotFound = errors.New("order not found")

	// ErrTerminalOrder indicates the order is in a terminal state.
	ErrTerminalOrder = errors.New("order is terminal")

	// ErrInvalidPaymentCode indicates the payment code format is invalid.
	ErrInvalidPaymentCode = errors.New("invalid payment code")

	// ErrPaymentNotAllowed indicates payment cannot be submitted in the current order state.
	ErrPaymentNotAllowed = errors.New("payment not allowed")

	// ErrPaymentInProgress indicates a seat update was rejected because payment is being validated.
	ErrPaymentInProgress = errors.New("payment in progress")
)

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
