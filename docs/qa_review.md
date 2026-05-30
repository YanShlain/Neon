# QA Review — initial_requirements.md

**Last run:** 2026-05-30  
**Commands:**
- `npm run test:e2e` — **17/17 PASS** (67s)
- `go test ./... -count=1 -timeout 120s` — **PASS** (83s)

**Verdict:** **PASS** — all testable requirements in [initial_requirements.md](initial_requirements.md) are covered by automated tests. Open findings: QA-UI-1, QA-UI-3 (Low, optional polish).

**Traceability source:** [docs/initial_requirements.md](initial_requirements.md) User Flow + Business Rules  
**E2E suite:** [tests/e2e/](../tests/e2e/) (Playwright, 4 spec files + helpers)

---

## Traceability matrix

| Req | Requirement (initial_requirements) | E2E test | Go / integration fallback | Status |
|-----|-----------------------------------|----------|---------------------------|--------|
| R-1 | Create flight order | E-E9, E-E1 | `TestI_B0_NoTimerOnOrderCreate` | PASS |
| R-2 | Select N seats → 15-minute hold starts | E-E9, IR-1 | `TestU_B1_FirstSeatUpdateStartsTimer` | PASS |
| R-3 | Timer refresh on seat change | E-E2 | `TestI_B1_TimerRefreshAfterSeatChange`, `TestU_B2_SeatChangeResetsTimer` | PASS |
| R-4 | Pay with 5-digit code | E-E1, IR-2 | `TestU_C5`, `TestI_C4` | PASS |
| R-5 | 10-second payment validation timeout | IR-4 (API poll) | `TestU_C4`, `workflow.go` `paymentActivityTimeout` | PASS |
| R-6 | 3 retry attempts → failure message | E-E3 | `TestU_C3`, `TestI_C6`, `TestI_D1` | PASS |
| R-7 | 15% payment failure simulation | — (deterministic E2E hooks) | `payment.go` `paymentFailureRate`, `TestU_C2` | PASS |
| R-8 | Retry after failure then succeed | IR-3 | `TestI_D3_FailOnceThenSucceedWithSameCode` | PASS |
| R-9 | Real-time timer countdown | E-E8, E-E10 | `TestI_D4_TimerDecrementsDuringPayment` | PASS |
| R-10 | Real-time order status during payment | IR-4 | `TestU_C4_AwaitingPaymentWhileValidationRuns` | PASS (API); see QA-UI-1 |
| R-11 | Timer expiry during payment → reject | E-E4, IR-7 | `TestI_D2_LatePaymentRejectedOnExpiry`, `TestU_D4` | PASS |
| R-12 | Seat auto-release after failure / expiry | IR-6, IR-7 | `TestI_D1`, `TestI_B4` | PASS |
| R-13 | Success → confirmation + BOOKED seats | E-E1, IR-5 | `TestU_C1`, `TestU_A6` | PASS |
| R-14 | Multi-flight inventory isolation | E-E5 | `TestI_B2_MultiFlightHoldIsolation`, `TestU_B7` | PASS |
| R-15 | Multi-user hold visibility | E-E6 | `TestU_A4`, seat repo tests | PASS |
| R-16 | Single active order rule | E-E7 | `TestU_E1` (partial), UI localStorage | PASS |
| R-17 | Single Temporal workflow per order | — (architecture) | `workflow_test.go` suite | PASS |
| R-18 | Graceful failure handling + user feedback | E-E3, E-E4 | `TestI_D5`, `TestI_D6` | PASS |

### E2E test index

| ID | Spec file | Maps to |
|----|-----------|---------|
| E-E1 | `booking-flow.spec.ts` | S-1 happy path |
| E-E2 | `booking-flow.spec.ts` | S-2 timer refresh |
| E-E3 | `payment.spec.ts` | S-3 payment exhaustion |
| E-E4 | `timer-expiry.spec.ts` | S-4 late payment |
| E-E5 | `inventory.spec.ts` | S-5 multi-flight |
| E-E6 | `inventory.spec.ts` | Multi-user map |
| E-E7 | `inventory.spec.ts` | Single-order rule |
| E-E8 | `booking-flow.spec.ts` | Local timer countdown |
| E-E9 | `booking-flow.spec.ts` | CREATED has no timer |
| E-E10 | `booking-flow.spec.ts` | Timer preserved on proceed |
| IR-1 | `booking-flow.spec.ts` | Select N seats |
| IR-2 | `payment.spec.ts` | 5-digit code validation (UI) |
| IR-3 | `payment.spec.ts` | Retry after one failure |
| IR-4 | `payment.spec.ts` | AWAITING_PAYMENT + timer during validation |
| IR-5 | `booking-flow.spec.ts` | BOOKED on seat map post-success |
| IR-6 | `inventory.spec.ts` | Seat release after PAYMENT_FAILED |
| IR-7 | `timer-expiry.spec.ts` | Seat release after EXPIRED |

---

## Mismatches

### Open

| ID | Severity | Requirement / doc | Observed behavior | Evidence |
|----|----------|-------------------|---------------------|----------|
| QA-UI-1 | Low | Real-time `AWAITING_PAYMENT` visible in UI during validation | UI polling interval is 5s (`ORDER_POLL_INTERVAL_MS`); status often jumps `SEATS_HELD` → `CONFIRMED` without displaying `AWAITING_PAYMENT`. Backend state is correct. | IR-4 uses API poll; manual §2.4 |
| QA-UI-3 | Low | `#payment-feedback` after failed attempt | Failure details appear in `#payment-events`; `#payment-feedback` may stay hidden after failed POST. | IR-3; E-E3 uses `#payment-events` |

### Resolved / accepted

| ID | Resolution | Evidence |
|----|------------|----------|
| QA-DOC-1 | **Resolved** — README updated: hold timer starts when **at least one seat** is selected, not on flight select. | E-E9; README §Booking flow |
| QA-DOC-2 | **Resolved** — README updated: `CREATED` has **no timer** until first seat hold. | E-E9; README §Order states |
| QA-DOC-3 | **Resolved** — `manual_tests.md` §2.1 updated: timer `—` after flight select; starts on first seat. | E-E9; manual_tests §2.1 |
| QA-UI-2 | **Accepted** — disabling Submit for non-5-digit input is intended UX; no change required. | IR-2 |

No **Critical** or **High** implementation gaps found against [initial_requirements.md](initial_requirements.md).

---

## Gaps intentionally not E2E-tested

| Requirement | Reason | Covered by |
|-------------|--------|------------|
| 10s activity timeout (exact wall clock) | Workflow activity `StartToCloseTimeout`; E2E uses env delay | `workflow.go`, `TestU_C4` |
| 15% stochastic failure rate | Non-deterministic; E2E uses `PAYMENT_*` env hooks | `payment.go`, unit/integration RNG tests |
| Single Temporal workflow | Architecture / orchestration | `BookingWorkflow` tests |
| Temporal Entity / DB approach choice | Implementation flexibility per spec | In-memory repo + workflow activities |
| Email/SMS confirmation messages | Not in initial requirements scope | — |

---

## Test harness notes

- **Server spawn:** [tests/e2e/helpers/server.ts](../tests/e2e/helpers/server.ts) clears inherited `PAYMENT_*` env vars before applying profile overrides (prevents shell `PAYMENT_NEVER_FAIL` leaking into failure tests).
- **Port cleanup:** Stale `go run ./cmd/api` processes on fixed E2E ports are killed before each server start (Windows `netstat` + `taskkill`).
- **Run locally:** `npm install && npx playwright install chromium && npm run test:e2e`

---

## Recommendations (optional, not blocking)

1. Reduce `ORDER_POLL_INTERVAL_MS` on payment page or optimistically set `AWAITING_PAYMENT` on submit click (QA-UI-1).
2. Show `#payment-feedback` on failed payment POST response (QA-UI-3).
3. Add E2E to CI workflow when GitHub Actions is introduced.
