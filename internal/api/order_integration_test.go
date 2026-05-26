package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"neon/domain"
	"neon/internal/app"
	"neon/internal/infrastructure/memory"
)

func newTestApp(t *testing.T) *httptest.Server {
	t.Helper()
	t.Setenv("TEMPORAL_AUTO_DEV", "1")
	if os.Getenv("HOLD_DURATION") == "" {
		t.Setenv("HOLD_DURATION", "30s")
	}

	application, err := app.BootstrapApp(context.Background(), memory.DefaultSeedConfig())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	t.Cleanup(application.Close)

	srv := httptest.NewServer(application.NewRouter())
	t.Cleanup(srv.Close)
	return srv
}

func newTestServer(t *testing.T) (*httptest.Server, *memory.SeatRepository) {
	t.Helper()
	t.Setenv("TEMPORAL_AUTO_DEV", "1")
	if os.Getenv("HOLD_DURATION") == "" {
		t.Setenv("HOLD_DURATION", "30s")
	}

	application, err := app.BootstrapApp(context.Background(), memory.DefaultSeedConfig())
	if err != nil {
		t.Fatalf("bootstrap app: %v", err)
	}
	t.Cleanup(application.Close)

	seatRepo, ok := application.Repos.Seats.(*memory.SeatRepository)
	if !ok {
		t.Fatal("expected *memory.SeatRepository")
	}
	srv := httptest.NewServer(application.NewRouter())
	t.Cleanup(srv.Close)
	return srv, seatRepo
}

func postJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func patchJSON(t *testing.T, url string, body any) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPatch, url, bytes.NewReader(data))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PATCH %s: %v", url, err)
	}
	return resp
}

type orderBody struct {
	OrderID               string   `json:"order_id"`
	FlightID              string   `json:"flight_id"`
	Status                string   `json:"status"`
	HeldSeatIDs           []string `json:"held_seat_ids"`
	TimerRemainingSeconds int      `json:"timer_remaining_seconds"`
	PaymentEvents         []struct {
		Type    string `json:"type"`
		Code    string `json:"code,omitempty"`
		Message string `json:"message,omitempty"`
	} `json:"payment_events"`
	PaymentFailures int `json:"payment_failures"`
}

func decodeOrder(t *testing.T, resp *http.Response) orderBody {
	t.Helper()
	defer resp.Body.Close()
	var body orderBody
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode order: %v", err)
	}
	return body
}

func createOrder(t *testing.T, srv *httptest.Server, flightID string) orderBody {
	t.Helper()
	resp := postJSON(t, srv.URL+"/api/v1/orders", map[string]string{"flight_id": flightID})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create order status = %d", resp.StatusCode)
	}
	return decodeOrder(t, resp)
}

// I-B1: S-2 Timer refresh — timer_remaining_seconds ≈900 after seat change
func TestI_B1_TimerRefreshAfterSeatChange(t *testing.T) {
	t.Setenv("HOLD_DURATION", "15m")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{
		"seat_ids": []string{"1A"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch seats status = %d", resp.StatusCode)
	}
	body := decodeOrder(t, resp)
	if body.Status != "SEATS_HELD" {
		t.Fatalf("status = %q, want SEATS_HELD", body.Status)
	}
	if body.TimerRemainingSeconds < 895 || body.TimerRemainingSeconds > 900 {
		t.Fatalf("timer_remaining_seconds = %d, want ~900", body.TimerRemainingSeconds)
	}
}

// I-B2: S-5 Multi-flight — Isolated holds on 101 vs 102
func TestI_B2_MultiFlightHoldIsolation(t *testing.T) {
	srv := newTestApp(t)

	o1 := createOrder(t, srv, "101")
	resp1 := patchJSON(t, srv.URL+"/api/v1/orders/"+o1.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("order1 patch status = %d", resp1.StatusCode)
	}

	o2 := createOrder(t, srv, "102")
	resp2 := patchJSON(t, srv.URL+"/api/v1/orders/"+o2.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("order2 patch status = %d", resp2.StatusCode)
	}

	seatsResp, err := http.Get(srv.URL + "/api/v1/flights/102/seats")
	if err != nil {
		t.Fatalf("get seats: %v", err)
	}
	defer seatsResp.Body.Close()
	raw, _ := io.ReadAll(seatsResp.Body)
	if !strings.Contains(string(raw), `"seat_id":"1A"`) && !strings.Contains(string(raw), `"seat_id": "1A"`) {
		t.Fatalf("expected seat 1A on flight 102")
	}
}

// I-B3: Cancel — CANCELLED; seats released
func TestI_B3_CancelOrderReleasesSeats(t *testing.T) {
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	cancelResp, err := http.Post(srv.URL+"/api/v1/orders/"+order.OrderID+"/cancel", "application/json", nil)
	if err != nil {
		t.Fatalf("cancel: %v", err)
	}
	body := decodeOrder(t, cancelResp)
	if body.Status != "CANCELLED" {
		t.Fatalf("status = %q, want CANCELLED", body.Status)
	}

	seatsResp, err := http.Get(srv.URL + "/api/v1/flights/101/seats")
	if err != nil {
		t.Fatalf("get seats: %v", err)
	}
	defer seatsResp.Body.Close()
	var seatMap struct {
		Seats []struct {
			SeatID string `json:"seat_id"`
			Status string `json:"status"`
		} `json:"seats"`
	}
	if err := json.NewDecoder(seatsResp.Body).Decode(&seatMap); err != nil {
		t.Fatalf("decode seats: %v", err)
	}
	for _, seat := range seatMap.Seats {
		if seat.SeatID == "1A" && seat.Status != "AVAILABLE" {
			t.Fatalf("1A status = %q, want AVAILABLE", seat.Status)
		}
	}
}

// I-B4: Expiry — EXPIRED after hold duration
func TestI_B4_OrderExpiresAfterHoldDuration(t *testing.T) {
	t.Setenv("HOLD_DURATION", "2s")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	time.Sleep(3 * time.Second)

	getResp, err := http.Get(srv.URL + "/api/v1/orders/" + order.OrderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	body := decodeOrder(t, getResp)
	if body.Status != "EXPIRED" {
		t.Fatalf("status = %q, want EXPIRED", body.Status)
	}
}

// I-B5: Hold conflict — 409 for second holder
func TestI_B5_HoldConflictReturns409(t *testing.T) {
	srv := newTestApp(t)

	o1 := createOrder(t, srv, "101")
	resp1 := patchJSON(t, srv.URL+"/api/v1/orders/"+o1.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("order1 patch status = %d", resp1.StatusCode)
	}
	resp1.Body.Close()

	o2 := createOrder(t, srv, "101")
	resp2 := patchJSON(t, srv.URL+"/api/v1/orders/"+o2.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp2.StatusCode != http.StatusConflict {
		t.Fatalf("order2 patch status = %d, want 409", resp2.StatusCode)
	}
	resp2.Body.Close()
}

func submitPayment(t *testing.T, srv *httptest.Server, orderID, code string) (orderBody, int) {
	t.Helper()
	resp := postJSON(t, srv.URL+"/api/v1/orders/"+orderID+"/payment", map[string]string{"code": code})
	defer resp.Body.Close()
	var body orderBody
	if resp.StatusCode == http.StatusOK {
		body = decodeOrder(t, resp)
	}
	return body, resp.StatusCode
}

func getOrder(t *testing.T, srv *httptest.Server, orderID string) orderBody {
	t.Helper()
	resp, err := http.Get(srv.URL + "/api/v1/orders/" + orderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	return decodeOrder(t, resp)
}

// I-C1: S-1 Happy path — CONFIRMED; seat BOOKED
func TestI_C1_PaymentHappyPath(t *testing.T) {
	t.Setenv("PAYMENT_NEVER_FAIL", "1")
	srv, seats := newTestServer(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	body, statusCode := submitPayment(t, srv, order.OrderID, "12345")
	if statusCode != http.StatusOK {
		t.Fatalf("payment status = %d", statusCode)
	}
	if body.Status != "CONFIRMED" {
		t.Fatalf("status = %q, want CONFIRMED", body.Status)
	}

	list, err := seats.ListByFlight(t.Context(), "101")
	if err != nil {
		t.Fatalf("list seats: %v", err)
	}
	for _, seat := range list {
		if seat.SeatID == "1A" {
			if seat.Status != domain.SeatStatusBooked {
				t.Fatalf("1A status = %q, want BOOKED", seat.Status)
			}
		}
	}
}

// I-C2: Retry then succeed — 3 events; CONFIRMED
func TestI_C2_PaymentRetryThenSucceed(t *testing.T) {
	t.Setenv("PAYMENT_FAIL_UNTIL", "2")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	for i := 0; i < 2; i++ {
		body, _ := submitPayment(t, srv, order.OrderID, "12345")
		if body.Status != "SEATS_HELD" {
			t.Fatalf("payment %d status = %q, want SEATS_HELD", i+1, body.Status)
		}
	}

	body, _ := submitPayment(t, srv, order.OrderID, "12345")
	if body.Status != "CONFIRMED" {
		t.Fatalf("final payment status = %q, want CONFIRMED", body.Status)
	}
	if len(body.PaymentEvents) < 3 {
		t.Fatalf("payment_events = %d, want at least 3", len(body.PaymentEvents))
	}
}

// I-C3: Timer during payment — Timer > 0 while AWAITING_PAYMENT
func TestI_C3_TimerDuringPayment(t *testing.T) {
	t.Setenv("PAYMENT_VALIDATION_DELAY", "2s")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	done := make(chan orderBody, 1)
	go func() {
		body, _ := submitPayment(t, srv, order.OrderID, "12345")
		done <- body
	}()

	time.Sleep(300 * time.Millisecond)
	getResp, err := http.Get(srv.URL + "/api/v1/orders/" + order.OrderID)
	if err != nil {
		t.Fatalf("get order: %v", err)
	}
	mid := decodeOrder(t, getResp)
	if mid.Status != "AWAITING_PAYMENT" {
		t.Fatalf("mid status = %q, want AWAITING_PAYMENT", mid.Status)
	}
	if mid.TimerRemainingSeconds <= 0 {
		t.Fatalf("timer_remaining_seconds = %d, want > 0", mid.TimerRemainingSeconds)
	}

	final := <-done
	if final.Status != "CONFIRMED" {
		t.Fatalf("final status = %q, want CONFIRMED", final.Status)
	}
}

// I-C4: Invalid code 1234 — HTTP 400; order stays SEATS_HELD
func TestI_C4_InvalidPaymentCodeLengthAPI(t *testing.T) {
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	_, statusCode := submitPayment(t, srv, order.OrderID, "1234")
	if statusCode != http.StatusBadRequest {
		t.Fatalf("payment status = %d, want 400", statusCode)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.Status != "SEATS_HELD" {
		t.Fatalf("status = %q, want SEATS_HELD", got.Status)
	}
}

// I-C5: Invalid code abcde — HTTP 400; order stays SEATS_HELD
func TestI_C5_InvalidPaymentCodeLettersAPI(t *testing.T) {
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	_, statusCode := submitPayment(t, srv, order.OrderID, "abcde")
	if statusCode != http.StatusBadRequest {
		t.Fatalf("payment status = %d, want 400", statusCode)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.Status != "SEATS_HELD" {
		t.Fatalf("status = %q, want SEATS_HELD", got.Status)
	}
}

// I-C6: Three failures then fourth rejected — HTTP 400 exhausted
func TestI_C6_PaymentAttemptsExhaustedAPI(t *testing.T) {
	t.Setenv("PAYMENT_ALWAYS_FAIL", "1")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	for i := 0; i < 3; i++ {
		body, code := submitPayment(t, srv, order.OrderID, "12345")
		if code != http.StatusOK {
			t.Fatalf("attempt %d status = %d, want 200", i+1, code)
		}
		if body.Status != "SEATS_HELD" {
			t.Fatalf("attempt %d order status = %q, want SEATS_HELD", i+1, body.Status)
		}
	}

	_, code := submitPayment(t, srv, order.OrderID, "12345")
	if code != http.StatusBadRequest {
		t.Fatalf("fourth payment status = %d, want 400", code)
	}
}

// I-C7: Payment on CONFIRMED order — HTTP 400 not allowed
func TestI_C7_PaymentOnConfirmedOrderRejected(t *testing.T) {
	t.Setenv("PAYMENT_NEVER_FAIL", "1")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	body, code := submitPayment(t, srv, order.OrderID, "12345")
	if code != http.StatusOK || body.Status != "CONFIRMED" {
		t.Fatalf("first payment status=%d body=%+v", code, body)
	}

	_, code = submitPayment(t, srv, order.OrderID, "12345")
	if code != http.StatusBadRequest {
		t.Fatalf("second payment status = %d, want 400", code)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.Status != "CONFIRMED" {
		t.Fatalf("status = %q, want CONFIRMED", got.Status)
	}
}

// I-C8: Unknown order — HTTP 404
func TestI_C8_PaymentUnknownOrder404(t *testing.T) {
	srv := newTestApp(t)

	_, code := submitPayment(t, srv, "00000000-0000-0000-0000-000000000099", "12345")
	if code != http.StatusNotFound {
		t.Fatalf("payment status = %d, want 404", code)
	}
}

// I-C9: Payment without held seats — HTTP 400 not allowed
func TestI_C9_PaymentWithoutSeatsHeldRejected(t *testing.T) {
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	_, code := submitPayment(t, srv, order.OrderID, "12345")
	if code != http.StatusBadRequest {
		t.Fatalf("payment status = %d, want 400", code)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.Status != "CREATED" {
		t.Fatalf("status = %q, want CREATED", got.Status)
	}
}

// I-C10: Missing payment body — HTTP 400
func TestI_C10_PaymentMissingBody400(t *testing.T) {
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+order.OrderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	emptyResp, err := http.Post(srv.URL+"/api/v1/orders/"+order.OrderID+"/payment", "application/json", bytes.NewReader([]byte("{}")))
	if err != nil {
		t.Fatalf("post payment: %v", err)
	}
	defer emptyResp.Body.Close()
	if emptyResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("payment status = %d, want 400", emptyResp.StatusCode)
	}
}
