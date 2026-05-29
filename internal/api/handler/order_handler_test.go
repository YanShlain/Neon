package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"neon/domain"
	"neon/internal/api/handler"
	"neon/internal/infrastructure/temporal"
	"neon/internal/workflow/booking"
)

// mockOrderService implements handler.OrderService for unit tests.
type mockOrderService struct {
	createOrder   func(ctx context.Context, flightID string) (booking.StatusResponse, error)
	updateSeats   func(ctx context.Context, orderID string, seatIDs []string) (booking.StatusResponse, error)
	cancelOrder   func(ctx context.Context, orderID string) (booking.StatusResponse, error)
	submitPayment func(ctx context.Context, orderID string, code string) (booking.StatusResponse, error)
	getStatus     func(ctx context.Context, orderID string) (booking.StatusResponse, error)
}

func (m *mockOrderService) CreateOrder(ctx context.Context, flightID string) (booking.StatusResponse, error) {
	return m.createOrder(ctx, flightID)
}
func (m *mockOrderService) UpdateSeats(ctx context.Context, orderID string, seatIDs []string) (booking.StatusResponse, error) {
	return m.updateSeats(ctx, orderID, seatIDs)
}
func (m *mockOrderService) CancelOrder(ctx context.Context, orderID string) (booking.StatusResponse, error) {
	return m.cancelOrder(ctx, orderID)
}
func (m *mockOrderService) SubmitPayment(ctx context.Context, orderID string, code string) (booking.StatusResponse, error) {
	return m.submitPayment(ctx, orderID, code)
}
func (m *mockOrderService) GetStatus(ctx context.Context, orderID string) (booking.StatusResponse, error) {
	return m.getStatus(ctx, orderID)
}

func newTestRouter(svc handler.OrderService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(gin.Recovery())
	h := handler.NewOrderHandler(svc)
	v1 := r.Group("/api/v1")
	v1.POST("/orders", h.CreateOrder)
	v1.PATCH("/orders/:order_id/seats", h.UpdateSeats)
	v1.POST("/orders/:order_id/cancel", h.CancelOrder)
	v1.POST("/orders/:order_id/payment", h.SubmitPayment)
	v1.GET("/orders/:order_id", h.GetOrder)
	return r
}

func doRequest(r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func okStatus() booking.StatusResponse {
	return booking.StatusResponse{
		OrderID:  "order-1",
		FlightID: "NA4821",
		Status:   domain.OrderStatusSeatsHeld,
	}
}

// --- CreateOrder ---

func TestCreateOrder_Success(t *testing.T) {
	svc := &mockOrderService{createOrder: func(_ context.Context, flightID string) (booking.StatusResponse, error) {
		require.Equal(t, "NA4821", flightID)
		return okStatus(), nil
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders", map[string]string{"flight_id": "NA4821"})
	require.Equal(t, http.StatusCreated, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Equal(t, "order-1", body["order_id"])
}

func TestCreateOrder_MissingBody_400(t *testing.T) {
	r := newTestRouter(&mockOrderService{})
	w := doRequest(r, http.MethodPost, "/api/v1/orders", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateOrder_MissingFlightID_400(t *testing.T) {
	r := newTestRouter(&mockOrderService{})
	w := doRequest(r, http.MethodPost, "/api/v1/orders", map[string]string{})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateOrder_ServiceError_500(t *testing.T) {
	svc := &mockOrderService{createOrder: func(_ context.Context, _ string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrOrderNotFound
	}}
	// CreateOrder doesn't call writeOrderError, so any error → 500
	// (no order exists yet; the service error mapping doesn't apply here)
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders", map[string]string{"flight_id": "NA4821"})
	require.Equal(t, http.StatusInternalServerError, w.Code)
}

// --- UpdateSeats ---

func TestUpdateSeats_Success(t *testing.T) {
	svc := &mockOrderService{updateSeats: func(_ context.Context, orderID string, seatIDs []string) (booking.StatusResponse, error) {
		require.Equal(t, "order-1", orderID)
		require.Equal(t, []string{"1A", "1B"}, seatIDs)
		return okStatus(), nil
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPatch, "/api/v1/orders/order-1/seats", map[string]any{"seat_ids": []string{"1A", "1B"}})
	require.Equal(t, http.StatusOK, w.Code)
}

func TestUpdateSeats_MissingBody_400(t *testing.T) {
	r := newTestRouter(&mockOrderService{})
	w := doRequest(r, http.MethodPatch, "/api/v1/orders/order-1/seats", nil)
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateSeats_NotFound_404(t *testing.T) {
	svc := &mockOrderService{updateSeats: func(_ context.Context, _ string, _ []string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrOrderNotFound
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPatch, "/api/v1/orders/order-1/seats", map[string]any{"seat_ids": []string{"1A"}})
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestUpdateSeats_HoldConflict_409(t *testing.T) {
	svc := &mockOrderService{updateSeats: func(_ context.Context, _ string, _ []string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrHoldConflict
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPatch, "/api/v1/orders/order-1/seats", map[string]any{"seat_ids": []string{"1A"}})
	require.Equal(t, http.StatusConflict, w.Code)
}

func TestUpdateSeats_TerminalOrder_410(t *testing.T) {
	svc := &mockOrderService{updateSeats: func(_ context.Context, _ string, _ []string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrTerminalOrder
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPatch, "/api/v1/orders/order-1/seats", map[string]any{"seat_ids": []string{"1A"}})
	require.Equal(t, http.StatusGone, w.Code)
}

func TestUpdateSeats_PaymentInProgress_409(t *testing.T) {
	svc := &mockOrderService{updateSeats: func(_ context.Context, _ string, _ []string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrPaymentInProgress
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPatch, "/api/v1/orders/order-1/seats", map[string]any{"seat_ids": []string{"1A"}})
	require.Equal(t, http.StatusConflict, w.Code)
}

// --- CancelOrder ---

func TestCancelOrder_Success(t *testing.T) {
	svc := &mockOrderService{cancelOrder: func(_ context.Context, orderID string) (booking.StatusResponse, error) {
		require.Equal(t, "order-1", orderID)
		resp := okStatus()
		resp.Status = domain.OrderStatusCancelled
		return resp, nil
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/cancel", nil)
	require.Equal(t, http.StatusOK, w.Code)
}

func TestCancelOrder_NotFound_404(t *testing.T) {
	svc := &mockOrderService{cancelOrder: func(_ context.Context, _ string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrOrderNotFound
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/cancel", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// --- SubmitPayment ---

func TestSubmitPayment_Success(t *testing.T) {
	svc := &mockOrderService{submitPayment: func(_ context.Context, orderID string, code string) (booking.StatusResponse, error) {
		require.Equal(t, "order-1", orderID)
		require.Equal(t, "12345", code)
		resp := okStatus()
		resp.Status = domain.OrderStatusConfirmed
		return resp, nil
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/payment", map[string]string{"code": "12345"})
	require.Equal(t, http.StatusOK, w.Code)
}

func TestSubmitPayment_MissingBody_400(t *testing.T) {
	r := newTestRouter(&mockOrderService{})
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/payment", map[string]string{})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitPayment_ShortCode_400(t *testing.T) {
	r := newTestRouter(&mockOrderService{})
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/payment", map[string]string{"code": "1234"})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitPayment_AlphaCode_400(t *testing.T) {
	r := newTestRouter(&mockOrderService{})
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/payment", map[string]string{"code": "abcde"})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSubmitPayment_NotFound_404(t *testing.T) {
	svc := &mockOrderService{submitPayment: func(_ context.Context, _ string, _ string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrOrderNotFound
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/payment", map[string]string{"code": "12345"})
	require.Equal(t, http.StatusNotFound, w.Code)
}

func TestSubmitPayment_TerminalOrder_410(t *testing.T) {
	svc := &mockOrderService{submitPayment: func(_ context.Context, _ string, _ string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrTerminalOrder
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/payment", map[string]string{"code": "12345"})
	require.Equal(t, http.StatusGone, w.Code)
}

func TestSubmitPayment_PaymentNotAllowed_400(t *testing.T) {
	svc := &mockOrderService{submitPayment: func(_ context.Context, _ string, _ string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrPaymentNotAllowed
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodPost, "/api/v1/orders/order-1/payment", map[string]string{"code": "12345"})
	require.Equal(t, http.StatusBadRequest, w.Code)
}

// --- GetOrder ---

func TestGetOrder_Success(t *testing.T) {
	svc := &mockOrderService{getStatus: func(_ context.Context, orderID string) (booking.StatusResponse, error) {
		require.Equal(t, "order-1", orderID)
		return okStatus(), nil
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodGet, "/api/v1/orders/order-1", nil)
	require.Equal(t, http.StatusOK, w.Code)
	var body map[string]any
	require.NoError(t, json.NewDecoder(w.Body).Decode(&body))
	require.Equal(t, "order-1", body["order_id"])
}

func TestGetOrder_NotFound_404(t *testing.T) {
	svc := &mockOrderService{getStatus: func(_ context.Context, _ string) (booking.StatusResponse, error) {
		return booking.StatusResponse{}, temporal.ErrOrderNotFound
	}}
	r := newTestRouter(svc)
	w := doRequest(r, http.MethodGet, "/api/v1/orders/order-1", nil)
	require.Equal(t, http.StatusNotFound, w.Code)
}
