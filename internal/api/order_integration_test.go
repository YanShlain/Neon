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
	MethodsUsed     int `json:"methods_used"`
	MethodsRemaining int `json:"methods_remaining"`
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

// I-B0: Timer starts on POST /orders (flight selected)
func TestI_B0_TimerStartsOnOrderCreate(t *testing.T) {
	t.Setenv("HOLD_DURATION", "15m")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	if order.Status != "CREATED" {
		t.Fatalf("status = %q, want CREATED", order.Status)
	}
	if order.TimerRemainingSeconds < 895 || order.TimerRemainingSeconds > 900 {
		t.Fatalf("timer_remaining_seconds = %d, want ~900", order.TimerRemainingSeconds)
	}
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
	t.Setenv("PAYMENT_NEVER_FAIL", "1")
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

func startNewPaymentMethod(t *testing.T, srv *httptest.Server, orderID string) (orderBody, int) {
	t.Helper()
	resp, err := http.Post(srv.URL+"/api/v1/orders/"+orderID+"/payment/new-method", "application/json", nil)
	if err != nil {
		t.Fatalf("new payment method: %v", err)
	}
	defer resp.Body.Close()
	var body orderBody
	if resp.StatusCode == http.StatusOK {
		body = decodeOrder(t, resp)
	}
	return body, resp.StatusCode
}

func holdSeat(t *testing.T, srv *httptest.Server, orderID string) {
	t.Helper()
	resp := patchJSON(t, srv.URL+"/api/v1/orders/"+orderID+"/seats", map[string]any{"seat_ids": []string{"1A"}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// I-D1: S-3 Method exhaustion — Failed order; seats released
func TestI_D1_MethodExhaustionReleasesSeats(t *testing.T) {
	t.Setenv("PAYMENT_ALWAYS_FAIL", "1")
	srv, seats := newTestServer(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	codes := []string{"11111", "22222", "33333"}
	for i, code := range codes {
		for attempt := 0; attempt < 3; attempt++ {
			body, codeHTTP := submitPayment(t, srv, order.OrderID, code)
			if codeHTTP != http.StatusOK {
				t.Fatalf("payment %s attempt %d status = %d", code, attempt+1, codeHTTP)
			}
			if body.Status != "SEATS_HELD" {
				t.Fatalf("payment %s attempt %d order status = %q", code, attempt+1, body.Status)
			}
		}
		if i < len(codes)-1 {
			body, codeHTTP := startNewPaymentMethod(t, srv, order.OrderID)
			if codeHTTP != http.StatusOK {
				t.Fatalf("new method after %s status = %d", code, codeHTTP)
			}
			if body.MethodsUsed != i+2 {
				t.Fatalf("methods_used = %d, want %d", body.MethodsUsed, i+2)
			}
		}
	}

	_, codeHTTP := submitPayment(t, srv, order.OrderID, "33333")
	if codeHTTP != http.StatusGone {
		t.Fatalf("terminal payment status = %d, want 410", codeHTTP)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.Status != "PAYMENT_FAILED" {
		t.Fatalf("status = %q, want PAYMENT_FAILED", got.Status)
	}

	list, err := seats.ListByFlight(t.Context(), "101")
	if err != nil {
		t.Fatalf("list seats: %v", err)
	}
	for _, seat := range list {
		if seat.SeatID == "1A" && seat.Status != domain.SeatStatusAvailable {
			t.Fatalf("1A status = %q, want AVAILABLE", seat.Status)
		}
	}
}

// I-D2: S-4 Late payment — EXPIRED; payment rejected
func TestI_D2_LatePaymentRejectedOnExpiry(t *testing.T) {
	t.Setenv("HOLD_DURATION", "2s")
	t.Setenv("PAYMENT_VALIDATION_DELAY", "5s")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	done := make(chan struct {
		body orderBody
		code int
	}, 1)
	go func() {
		body, code := submitPayment(t, srv, order.OrderID, "12345")
		done <- struct {
			body orderBody
			code int
		}{body, code}
	}()

	time.Sleep(3 * time.Second)
	got := getOrder(t, srv, order.OrderID)
	if got.Status != "EXPIRED" {
		t.Fatalf("status after expiry = %q, want EXPIRED", got.Status)
	}
	foundRejection := false
	for _, ev := range got.PaymentEvents {
		if ev.Type == "rejected_by_timer" {
			foundRejection = true
		}
	}
	if !foundRejection {
		t.Fatal("expected rejected_by_timer payment event")
	}

	result := <-done
	if result.code != http.StatusGone && result.code != http.StatusOK {
		t.Fatalf("payment response status = %d", result.code)
	}
}

// I-D3: New method flow — Fail 2× → new method → success
func TestI_D3_NewMethodFlowRetrySuccess(t *testing.T) {
	t.Setenv("PAYMENT_FAIL_UNTIL", "2")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	for i := 0; i < 2; i++ {
		body, code := submitPayment(t, srv, order.OrderID, "11111")
		if code != http.StatusOK || body.Status != "SEATS_HELD" {
			t.Fatalf("attempt %d status=%d body=%+v", i+1, code, body)
		}
	}

	body, code := startNewPaymentMethod(t, srv, order.OrderID)
	if code != http.StatusOK {
		t.Fatalf("new method status = %d", code)
	}
	if body.MethodsUsed != 2 || body.PaymentFailures != 0 {
		t.Fatalf("after new method methods_used=%d failures=%d", body.MethodsUsed, body.PaymentFailures)
	}

	body, code = submitPayment(t, srv, order.OrderID, "22222")
	if code != http.StatusOK || body.Status != "CONFIRMED" {
		t.Fatalf("success payment status=%d body=%+v", code, body)
	}
}

// I-D4: Timer never pauses — Timer decrements during payment
func TestI_D4_TimerDecrementsDuringPayment(t *testing.T) {
	t.Setenv("PAYMENT_NEVER_FAIL", "1")
	t.Setenv("PAYMENT_VALIDATION_DELAY", "2s")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	before := getOrder(t, srv, order.OrderID)
	if before.TimerRemainingSeconds <= 0 {
		t.Fatalf("timer before payment = %d, want > 0", before.TimerRemainingSeconds)
	}

	done := make(chan orderBody, 1)
	go func() {
		body, _ := submitPayment(t, srv, order.OrderID, "12345")
		done <- body
	}()

	time.Sleep(500 * time.Millisecond)
	mid := getOrder(t, srv, order.OrderID)
	if mid.Status != "AWAITING_PAYMENT" {
		t.Fatalf("mid status = %q, want AWAITING_PAYMENT", mid.Status)
	}
	if mid.TimerRemainingSeconds >= before.TimerRemainingSeconds {
		t.Fatalf("timer did not decrement during payment: before=%d mid=%d", before.TimerRemainingSeconds, mid.TimerRemainingSeconds)
	}

	final := <-done
	if final.Status != "CONFIRMED" {
		t.Fatalf("final status = %q, want CONFIRMED", final.Status)
	}
}

// I-D5: GET order exposes methods_used, methods_remaining, payment_failures for UI counters.
func TestI_D5_GetOrderExposesPaymentCounters(t *testing.T) {
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	got := getOrder(t, srv, order.OrderID)
	if got.MethodsUsed != 0 {
		t.Fatalf("methods_used = %d, want 0 before payment", got.MethodsUsed)
	}
	if got.MethodsRemaining != 3 {
		t.Fatalf("methods_remaining = %d, want 3", got.MethodsRemaining)
	}
	if got.PaymentFailures != 0 {
		t.Fatalf("payment_failures = %d, want 0", got.PaymentFailures)
	}
}

// I-D6: Fourth payment on same code — HTTP 400; failures stay at 3 (UI disables submit).
func TestI_D6_FourthPaymentAttemptRejected(t *testing.T) {
	t.Setenv("PAYMENT_ALWAYS_FAIL", "1")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	for i := 0; i < 3; i++ {
		body, code := submitPayment(t, srv, order.OrderID, "12345")
		if code != http.StatusOK || body.Status != "SEATS_HELD" {
			t.Fatalf("attempt %d status=%d body=%+v", i+1, code, body)
		}
		if body.PaymentFailures != i+1 {
			t.Fatalf("attempt %d payment_failures=%d, want %d", i+1, body.PaymentFailures, i+1)
		}
	}

	_, code := submitPayment(t, srv, order.OrderID, "12345")
	if code != http.StatusBadRequest {
		t.Fatalf("fourth payment status = %d, want 400", code)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.PaymentFailures != 3 {
		t.Fatalf("payment_failures = %d, want 3", got.PaymentFailures)
	}
	if got.MethodsUsed != 1 {
		t.Fatalf("methods_used = %d, want 1", got.MethodsUsed)
	}
	if got.MethodsRemaining != 2 {
		t.Fatalf("methods_remaining = %d, want 2", got.MethodsRemaining)
	}
	if got.Status != "SEATS_HELD" {
		t.Fatalf("status = %q, want SEATS_HELD", got.Status)
	}
	if len(got.PaymentEvents) == 0 {
		t.Fatal("expected payment_events")
	}
	last := got.PaymentEvents[len(got.PaymentEvents)-1]
	if last.Type != "attempts_exhausted" {
		t.Fatalf("last event type = %q, want attempts_exhausted", last.Type)
	}
}

// I-D7: New method after 3 methods used — HTTP 400 (no further method slots).
func TestI_D7_NewMethodRejectedWhenAllSlotsUsed(t *testing.T) {
	t.Setenv("PAYMENT_ALWAYS_FAIL", "1")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	for _, code := range []string{"11111", "22222"} {
		for i := 0; i < 3; i++ {
			_, codeHTTP := submitPayment(t, srv, order.OrderID, code)
			if codeHTTP != http.StatusOK {
				t.Fatalf("pay %s attempt %d status = %d", code, i+1, codeHTTP)
			}
		}
		_, codeHTTP := startNewPaymentMethod(t, srv, order.OrderID)
		if codeHTTP != http.StatusOK {
			t.Fatalf("new method after %s status = %d", code, codeHTTP)
		}
	}
	_, codeHTTP := submitPayment(t, srv, order.OrderID, "33333")
	if codeHTTP != http.StatusOK {
		t.Fatalf("first pay on third code status = %d", codeHTTP)
	}

	_, codeHTTP = startNewPaymentMethod(t, srv, order.OrderID)
	if codeHTTP != http.StatusBadRequest {
		t.Fatalf("fourth new-method status = %d, want 400", codeHTTP)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.MethodsUsed != 3 {
		t.Fatalf("methods_used = %d, want 3", got.MethodsUsed)
	}
	if got.MethodsRemaining != 0 {
		t.Fatalf("methods_remaining = %d, want 0", got.MethodsRemaining)
	}
}

// I-D8: Fail 3× → new method — failures reset, methods_used=2 (manual test 6.1 API contract).
func TestI_D8_NewMethodResetsFailuresAfterThreeAttempts(t *testing.T) {
	t.Setenv("PAYMENT_ALWAYS_FAIL", "1")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	for i := 0; i < 3; i++ {
		body, code := submitPayment(t, srv, order.OrderID, "11111")
		if code != http.StatusOK {
			t.Fatalf("attempt %d status = %d", i+1, code)
		}
		if body.PaymentFailures != i+1 {
			t.Fatalf("attempt %d payment_failures = %d, want %d", i+1, body.PaymentFailures, i+1)
		}
	}

	body, code := startNewPaymentMethod(t, srv, order.OrderID)
	if code != http.StatusOK {
		t.Fatalf("new method status = %d", code)
	}
	if body.MethodsUsed != 2 {
		t.Fatalf("methods_used = %d, want 2", body.MethodsUsed)
	}
	if body.PaymentFailures != 0 {
		t.Fatalf("payment_failures = %d, want 0 after new method", body.PaymentFailures)
	}
	if body.MethodsRemaining != 1 {
		t.Fatalf("methods_remaining = %d, want 1", body.MethodsRemaining)
	}
}

// I-D9: Different code without new-method — HTTP 400.
func TestI_D9_DifferentCodeWithoutNewMethodRejected(t *testing.T) {
	t.Setenv("PAYMENT_ALWAYS_FAIL", "1")
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	body, code := submitPayment(t, srv, order.OrderID, "11111")
	if code != http.StatusOK || body.Status != "SEATS_HELD" {
		t.Fatalf("first payment status=%d body=%+v", code, body)
	}

	_, code = submitPayment(t, srv, order.OrderID, "22222")
	if code != http.StatusBadRequest {
		t.Fatalf("different code status = %d, want 400", code)
	}

	got := getOrder(t, srv, order.OrderID)
	if got.Status != "SEATS_HELD" {
		t.Fatalf("status = %q, want SEATS_HELD", got.Status)
	}
	if len(got.PaymentEvents) == 0 {
		t.Fatal("expected payment_events")
	}
	last := got.PaymentEvents[len(got.PaymentEvents)-1]
	if last.Type != "method_change_required" {
		t.Fatalf("last event type = %q, want method_change_required", last.Type)
	}
}

// I-D10: New method before any payment — HTTP 400.
func TestI_D10_NewMethodBeforeFirstPaymentRejected(t *testing.T) {
	srv := newTestApp(t)

	order := createOrder(t, srv, "101")
	holdSeat(t, srv, order.OrderID)

	_, code := startNewPaymentMethod(t, srv, order.OrderID)
	if code != http.StatusBadRequest {
		t.Fatalf("new method before payment status = %d, want 400", code)
	}
}
