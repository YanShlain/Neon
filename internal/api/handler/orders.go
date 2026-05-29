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
		slog.Info("inbound request",
			"method", c.Request.Method, "path", c.Request.URL.Path,
			"bind_error", err,
		)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusBadRequest)
		return
	}
	slog.Info("inbound request",
		"method", c.Request.Method, "path", c.Request.URL.Path,
		"body", req,
	)

	status, err := h.orders.CreateOrder(ctx, req.FlightID)
	if err != nil {
		slog.Error("create order failed", "flight_id", req.FlightID, "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusInternalServerError)
		return
	}
	c.JSON(http.StatusCreated, toOrderResponse(status))
	slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusCreated)
}

// UpdateSeats handles PATCH /api/v1/orders/:order_id/seats.
func (h *OrderHandler) UpdateSeats(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")

	var req dto.UpdateSeatsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Info("inbound request",
			"method", c.Request.Method, "path", c.Request.URL.Path, "order_id", orderID,
			"bind_error", err,
		)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusBadRequest)
		return
	}
	slog.Info("inbound request",
		"method", c.Request.Method, "path", c.Request.URL.Path, "order_id", orderID,
		"body", req,
	)

	status, err := h.orders.UpdateSeats(ctx, orderID, req.SeatIDs)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
	slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusOK)
}

// CancelOrder handles POST /api/v1/orders/:order_id/cancel.
func (h *OrderHandler) CancelOrder(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")
	slog.Info("inbound request", "method", c.Request.Method, "path", c.Request.URL.Path, "order_id", orderID)

	status, err := h.orders.CancelOrder(ctx, orderID)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
	slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusOK)
}

// SubmitPayment handles POST /api/v1/orders/:order_id/payment.
func (h *OrderHandler) SubmitPayment(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")

	var req dto.SubmitPaymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		slog.Info("inbound request",
			"method", c.Request.Method, "path", c.Request.URL.Path, "order_id", orderID,
			"bind_error", err,
		)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusBadRequest)
		return
	}
	slog.Info("inbound request",
		"method", c.Request.Method, "path", c.Request.URL.Path, "order_id", orderID,
		"body", req,
	)

	if !isValidPaymentCode(req.Code) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid payment code"})
		slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusBadRequest)
		return
	}

	status, err := h.orders.SubmitPayment(ctx, orderID, req.Code)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
	slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusOK)
}

// GetOrder handles GET /api/v1/orders/:order_id.
func (h *OrderHandler) GetOrder(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")
	slog.Info("inbound request", "method", c.Request.Method, "path", c.Request.URL.Path, "order_id", orderID)

	status, err := h.orders.GetStatus(ctx, orderID)
	if err != nil {
		writeOrderError(c, orderID, err)
		return
	}
	c.JSON(http.StatusOK, toOrderResponse(status))
	slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", http.StatusOK)
}

// StreamOrder handles GET /api/v1/orders/:order_id/stream as Server-Sent Events.
// The stream closes automatically when the order reaches a terminal state.
func (h *OrderHandler) StreamOrder(c *gin.Context) {
	ctx := c.Request.Context()
	orderID := c.Param("order_id")
	slog.Info("inbound request", "method", c.Request.Method, "path", c.Request.URL.Path, "order_id", orderID)

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
	}
}

func writeOrderError(c *gin.Context, orderID string, err error) {
	slog.Error("order request failed", "order_id", orderID, "error", err)
	var statusCode int
	switch {
	case errors.Is(err, temporal.ErrOrderNotFound):
		statusCode = http.StatusNotFound
		c.JSON(statusCode, gin.H{"error": "order not found"})
	case errors.Is(err, temporal.ErrHoldConflict):
		statusCode = http.StatusConflict
		c.JSON(statusCode, gin.H{"error": "seat hold conflict"})
	case errors.Is(err, temporal.ErrPaymentInProgress):
		statusCode = http.StatusConflict
		c.JSON(statusCode, gin.H{"error": "payment in progress"})
	case errors.Is(err, temporal.ErrTerminalOrder):
		statusCode = http.StatusGone
		c.JSON(statusCode, gin.H{"error": "order is terminal"})
	case errors.Is(err, temporal.ErrInvalidPaymentCode):
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": "invalid payment code"})
	case errors.Is(err, temporal.ErrPaymentNotAllowed):
		statusCode = http.StatusBadRequest
		c.JSON(statusCode, gin.H{"error": "payment not allowed"})
	default:
		statusCode = http.StatusInternalServerError
		c.JSON(statusCode, gin.H{"error": "internal error"})
	}
	slog.Info("inbound response", "method", c.Request.Method, "path", c.Request.URL.Path, "status", statusCode)
}

func isValidPaymentCode(code string) bool {
	if len(code) != 5 {
		return false
	}
	for _, ch := range code {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
