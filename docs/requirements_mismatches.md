# QA Review — Requirements Compliance

Source: `docs/initial_requirements.md`

All requirements are now met. The three mismatches identified during the initial review have been resolved.

---

## Requirements Met

| # | Requirement | Evidence |
|---|---|---|
| R-1 | User can create a flight order | `POST /api/v1/orders` → starts `BookingWorkflow` |
| R-2 | User can select N seats | `PATCH /api/v1/orders/:id/seats` → `UpdateSeats` workflow update |
| R-3 | 15-minute seat reservation hold | `HoldDuration = 15 * time.Minute`; workflow timer fires `expireOrder` |
| R-4 | Seats are automatically released when hold expires | `expireOrder` calls `releaseHeldSeats`; confirmed by test U-B5 |
| R-5 | Hold timer resets when user modifies seat selection | `applySeatUpdate` resets `TimerDeadline`; confirmed by test U-B2 |
| R-6 | User can review order with live timer countdown | Seats and payment pages render `timer_remaining_seconds`; JS counts down every second |
| R-7 | Real-time order status updates in the browser | SSE endpoint `GET /api/v1/orders/:id/stream` pushes status every 1 s; JS `EventSource` with 2 s polling fallback |
| R-8 | Payment uses a 5-digit numeric code | `isValidPaymentCode` validates in both workflow and handler; invalid code → `format_invalid` event |
| R-9 | Payment validation completes within 10 seconds | `paymentActivityTimeout = 10 * time.Second` as `StartToCloseTimeout` on `ValidatePayment` activity |
| R-10 | 15% simulated payment failure rate | `paymentFailureRate = 0.15`; `simulatePaymentFailure` in `payment.go` |
| R-11 | Successful payment confirms booking and sends confirmation | `ConfirmSeats` activity sets seats to `BOOKED`; status → `CONFIRMED`; UI shows confirmation panel |
| R-12 | **After 3 failures the order fails with a clear message** | `completePaymentValidation` calls `failOrderPaymentExhausted` when `PaymentFailures >= 3`; status → `PAYMENT_FAILED`; seats released; confirmed by test U-C3 |
| R-13 | Seat inventory managed internally (not delegated externally) | In-memory `SeatRepository` with per-flight locking in `internal/infrastructure/memory/` |
| R-14 | Graceful failure handling with user-facing messages | Terminal states produce descriptive UI messages; `LastError` propagated through workflow → API → frontend |
| R-15 | Single Temporal workflow orchestrates the entire order | `BookingWorkflow` handles seat holds, timer, payment, and confirmation end-to-end |
| R-16 | Temporal worker executes booking activities | `cmd/worker/main.go`; `Activities` struct registers `HoldSeats`, `ReleaseSeats`, `ValidatePayment`, `ConfirmSeats` |
| R-17 | Go-based RESTful server | Gin router in `internal/api/router.go`; all handlers in Go |

---

## Fixes Applied

### Fix 1 — Order now transitions to `PAYMENT_FAILED` after 3 failed attempts

**File:** `internal/workflow/booking/workflow.go`

After each payment validation failure `completePaymentValidation` increments `PaymentFailures`. When that counter reaches `maxFailuresPerCode` (3), `failOrderPaymentExhausted` is called immediately: seats are released, `TimerDeadline` is cleared, and the workflow status moves to `PAYMENT_FAILED`. The workflow then exits the selector loop as a terminal state.

### Fix 2 — Dead multi-method payment code removed

The following dead code from a prior design iteration was removed in full:

| Removed artifact | File |
|---|---|
| `maxPaymentMethods` constant | `internal/workflow/booking/payment.go` |
| `SignalStartNewPaymentMethod` constant | `internal/workflow/booking/config.go` |
| `PaymentEventMethodChangeRequired`, `PaymentEventNewMethodStarted`, `PaymentEventMethodsExhausted`, `PaymentEventNewMethodNotAllowed` event types | `internal/workflow/booking/types.go` |
| `MethodsUsed`, `MethodsRemaining` fields | `internal/workflow/booking/types.go`, `internal/api/dto/orders.go` |
| `handleStartNewPaymentMethod`, `rejectNewPaymentMethod`, `paymentMethodsExhausted` functions | `internal/workflow/booking/workflow.go` |
| `StartNewPaymentMethod` service method + helpers | `internal/infrastructure/temporal/order_service.go` |
| `ErrDifferentPaymentMethodRequired`, `ErrNewMethodNotAllowed`, `ErrMethodsExhausted`, `ErrPaymentAttemptsExhausted` error vars | `internal/infrastructure/temporal/order_service.go` |
| `StartNewPaymentMethod` handler | `internal/api/handler/orders.go` |
| `POST /api/v1/orders/:id/payment/new-method` route | `internal/api/router.go` |
| Tests U-D1, U-D2, U-D3 and `scheduleNewPaymentMethod` helper | `internal/workflow/booking/workflow_test.go` |

### Fix 3 — Test U-D5 corrected

`TestU_D5_DifferentCodeAllowedWithinRetryLimit` had an incorrect assertion (`PaymentEventValidationFailed`) and described behaviour that was not implemented. The test has been rewritten as `TestU_D5_DifferentCodeSucceedsOnRetry`: it verifies that the user can submit a different 5-digit code on a subsequent attempt and the booking confirms successfully.

---

## Test results after fixes

```
ok  neon/internal/workflow/booking  (17 tests, all PASS)
```
