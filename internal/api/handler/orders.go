package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"neon/domain"
	"neon/internal/api/dto"
	"neon/internal/infrastructure/temporal"
	"neon/internal/workflow/booking"
)

// OrderService is the contract the presentation layer requires of the booking backend.
// The concrete implementation lives in internal/infrastructure/temporal.
type OrderService interface {
	CreateOrder(ctx context.Context, flightID string) (booking.StatusResponse, error)
	UpdateSeats(ctx context.Context, orderID string, seatIDs []string) (booking.StatusResponse, error)
	CancelOrder(ctx context.Context, orderID string) (booking.StatusResponse, error)
	SubmitPayment(ctx context.Context, orderID string, code string) (booking.StatusResponse, error)
	GetStatus(ctx context.Context, orderID string) (booking.StatusResponse, error)
}

// OrderHandler serves booking order endpoints.
type OrderHandler struct {
	orders OrderService
}

// NewOrderHandler creates an OrderHandler.
func NewOrderHandler(orders OrderService) *OrderHandler {
	return &OrderHandler{orders: orders}
}

// CreateOrder handles POST /api/v1/orders.
func (h *OrderHandler) CreateOrder(c *gin.Context) {
	ctx := c.Request.Context()

	var req dto.CreateOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	status, err := h.orders.CreateOrder(ctx, req.FlightID)
	if err != nil {
		slog.Error("create order failed", "flight_id", req.FlightID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}
	c.JSON(http.StatusCreated, toOrderResponse(status))
}

// UpdateSeats handles PATCH /api/v1/orders/:order_id/seats.
func (h *OrderHandler) UpdateSeats(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")

	var req dto.UpdateSeatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	status, err := h.orders.UpdateSeats(ctx, orderID, req.SeatIDs)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
}

// CancelOrder handles POST /api/v1/orders/:order_id/cancel.
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")

	status, err := h.orders.CancelOrder(ctx, orderID)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
}

// SubmitPayment handles POST /api/v1/orders/:order_id/payment.
func (h *OrderHandler) SubmitPayment(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")

	var req dto.SubmitPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}

	if !domain.IsValidPaymentCode(req.Code) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payment code"})
		return
	}

	status, err := h.orders.SubmitPayment(ctx, orderID, req.Code)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
}

// GetOrder handles GET /api/v1/orders/:order_id.
func (h *OrderHandler) GetOrder(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")

	status, err := h.orders.GetStatus(ctx, orderID)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
}

// StreamOrder handles GET /api/v1/orders/:order_id/stream as Server-Sent Events.
// The stream closes automatically when the order reaches a terminal state.
func (h *OrderHandler) StreamOrder(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "stream not supported"})
		return
	}

	sendStatus := func(status booking.StatusResponse) bool {
		payload, err := json.Marshal(toOrderResponse(status))
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(c.Writer, "event: status\ndata: %s\n\n", payload); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}

	status, err := h.orders.GetStatus(ctx, orderID)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	if !sendStatus(status) {
		return
	}
	if status.Status.IsTerminal() {
		return
	}

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			status, err = h.orders.GetStatus(ctx, orderID)
			if err != nil {
				return
			}
			if !sendStatus(status) {
				return
			}
			if status.Status.IsTerminal() {
				return
			}
		}
	}
}

func toOrderResponse(status booking.StatusResponse) dto.OrderResponse {
	events := make([]dto.PaymentEventResponse, 0, len(status.PaymentEvents))
	for _, ev := range status.PaymentEvents {
		events = append(events, dto.PaymentEventResponse{
			Type:    string(ev.Type),
			Code:    ev.Code,
			Message: ev.Message,
		})
	}
	return dto.OrderResponse{
		OrderID:               status.OrderID,
		FlightID:              status.FlightID,
		Status:                string(status.Status),
		HeldSeatIDs:           status.HeldSeatIDs,
		TimerRemainingSeconds: status.TimerRemainingSeconds,
		PaymentEvents:         events,
		PaymentFailures:       status.PaymentFailures,
		MethodsUsed:           status.MethodsUsed,
		MethodsRemaining:      status.MethodsRemaining,
	}
}

func writeOrderError(c *gin.Context, orderID string, err error) {
	slog.Error("order request failed", "order_id", orderID, "error", err)
	switch {
	case errors.Is(err, temporal.ErrOrderNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "order not found"})
	case errors.Is(err, temporal.ErrHoldConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "seat hold conflict"})
	case errors.Is(err, temporal.ErrPaymentInProgress):
		c.JSON(http.StatusConflict, gin.H{"error": "payment in progress"})
	case errors.Is(err, temporal.ErrTerminalOrder):
		c.JSON(http.StatusGone, gin.H{"error": "order is terminal"})
	case errors.Is(err, temporal.ErrInvalidPaymentCode):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payment code"})
	case errors.Is(err, temporal.ErrPaymentNotAllowed):
		c.JSON(http.StatusBadRequest, gin.H{"error": "payment not allowed"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
	}
}
