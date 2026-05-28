# Neon — Implementation Progress

**Last updated:** 2026-05-28  
**Branch:** `dev` (up to date with `origin/dev`)  
**Canonical plan:** [docs/final_plan.md](final_plan.md)  
**Requirements:** [docs/final_requierments.md](final_requierments.md)

---

## Overall strategy

Implement phases **MVP-A → MVP-E** one at a time. After each phase:

1. All phase tests must pass (`go test ./...`)
2. User manually tests via UI/API
3. **Do not start the next phase until the user confirms**

---

## Phase status summary

| Phase | Name | Status | Tests |
|-------|------|--------|-------|
| **MVP-A** | Flight catalog + read-only UI | **Done** | U-A1–U-A6, I-A1–I-A4 ✅ |
| **MVP-B** | Holds, timer, cancel, booking UI | **Done** (user signed off) | U-B0–U-B7, I-B0–I-B5 ✅ |
| **MVP-C** | Payment happy path | **Done** (user signed off) | U-C1–U-C6, I-C1–I-C10 ✅ |
| **MVP-D** | Payment edge cases | **Done** (user signed off) | U-D1–U-D5, I-D1–I-D10 ✅ |
| **MVP-E** | E2E polish | **Done** | E-E1–E-E7 ✅ |

---

## MVP-A — Complete ✅

**Commit:** `e18edbf` — `feat(ui): add MVP-A read-only web UI and per-phase UI plan`

- Domain, in-memory repos, seed (flights **101**, **102**, 10×6 grid)
- `GET /api/v1/flights`, `GET /api/v1/flights/{id}/seats`
- Read-only UI: flight list, seat map, legend, refresh, departed banner

---

## MVP-B — Complete ✅

**Commit:** `6f79c25` — `feat(mvp-b): add seat holds, timer, cancel, and booking UI`  
**Manual sign-off:** 2026-05-26 (including two-browser hold conflict / grayscale check)

### Backend

| Area | Location | Notes |
|------|----------|-------|
| Order domain | `domain/order.go` | `OrderStatus`, `IsTerminal()` |
| Workflow | `internal/workflow/booking/` | Holds, 15m timer, cancel, expiry |
| Activities | `activities.go` | `HoldSeats`, `ReleaseSeats` |
| Temporal | `internal/infrastructure/temporal/` | `OrderService`, embedded dev server |
| Bootstrap | `internal/app/application.go` | Repos + worker in-process |
| Order API | `internal/api/handler/orders.go` | CRUD + cancel |
| Worker | `cmd/worker/main.go` | Standalone (API embeds worker for in-memory MVP) |

**API endpoints:**

- `POST /api/v1/orders`
- `PATCH /api/v1/orders/{id}/seats`
- `POST /api/v1/orders/{id}/cancel`
- `GET /api/v1/orders/{id}`

**Design notes:**

- Workflow ID == `order_id` (UUID)
- Seat changes via **Temporal workflow updates** (`UpdateSeats`, `CancelOrder`) for sync HTTP responses
- Hold conflicts are **non-retryable** → HTTP **409**
- `HOLD_DURATION` env (default `15m`; tests use `30s` / `2s`)
- `TEMPORAL_AUTO_DEV=1` (default) embeds Temporal dev server when `TEMPORAL_HOST` is unavailable
- In-memory repos shared in **one process** (`go run ./cmd/api`)

### UI

- Flight click → `POST /orders` → `localStorage`
- Interactive seat map, confirm → `PATCH .../seats`
- Hold timer starts on flight click; countdown on seat map and payment (client-side)
- Own holds highlighted (blue); others' HELD/BOOKED grayscale
- Cancel order; single active order guard on flight list

### Tests

All MVP-B tests pass (`go test ./...`).

---

## MVP-C — Complete ✅

**Commit:** `c5a5d29` — `feat(mvp-c): payment happy path with tests and manual guide`  
**Manual sign-off:** 2026-05-26 (UI + API; happy path, invalid codes, retry)

See [final_plan.md](final_plan.md) § MVP-C. Manual steps: [manual_tests.md](manual_tests.md).

### Backend

| Area | Notes |
|------|-------|
| Activities | `ValidatePayment` (5-digit, 10s timeout, 15% failure), `ConfirmSeats` |
| Workflow | `SubmitPayment` signal; states `SEATS_HELD` → `AWAITING_PAYMENT` → `CONFIRMED`; timer keeps running during payment |
| Query | `payment_events`, `payment_failures` on `GetStatus` |
| API | `POST /api/v1/orders/{id}/payment` body `{ "code": "12345" }` |
| Test hooks | `PAYMENT_FAIL_UNTIL`, `PAYMENT_VALIDATION_DELAY` env vars |

### UI

- Seat map → **Proceed to payment** when `SEATS_HELD`
- `/payment` page: 5-digit input, submit, inline feedback, confirmation view
- Status strip includes `AWAITING_PAYMENT`; timer visible during payment

### Tests

All MVP-C tests pass (`go test ./...`).

| ID | Type | Scenario | Result |
|----|------|----------|--------|
| U-C1 | Unit | Pay success → CONFIRMED, BOOKED | ✅ |
| U-C2 | Unit | Fail once, retry same code → CONFIRMED | ✅ |
| U-C3 | Unit | 3 failures, 4th rejected | ✅ |
| U-C4 | Unit | AWAITING_PAYMENT + timer running | ✅ |
| U-C5 | Unit | Code `1234` format error | ✅ |
| U-C6 | Unit | Code `abcde` format error | ✅ |
| I-C1 | Integration | S-1 happy path API | ✅ |
| I-C2 | Integration | Retry then succeed (3 events) | ✅ |
| I-C3 | Integration | Timer > 0 during AWAITING_PAYMENT | ✅ |
| I-C4 | Integration | Invalid code `1234` → HTTP 400 | ✅ |
| I-C5 | Integration | Invalid code `abcde` → HTTP 400 | ✅ |
| I-C6 | Integration | Attempt exhaustion → HTTP 400 | ✅ |
| I-C7 | Integration | Payment on CONFIRMED → HTTP 400 | ✅ |
| I-C8 | Integration | Unknown order → HTTP 404 | ✅ |
| I-C9 | Integration | Payment without seats → HTTP 400 | ✅ |
| I-C10 | Integration | Missing body → HTTP 400 | ✅ |

**Not in scope (deferred to MVP-D):** `StartNewPaymentMethod`, timer-vs-payment race rejection (S-4), 3×3 method exhaustion (S-3). *(Implemented in MVP-D — see below.)*

**Data note:** Seat inventory is **in-memory only** — restarting `go run ./cmd/api` resets all seats to AVAILABLE (no Postgres yet).

---

## MVP-D — Complete ✅

**Scope:** Payment edge cases — 3 methods × 3 attempts, timer/payment race (S-3, S-4), new-method API and UI.
**Manual sign-off:** 2026-05-27

### Backend

| Area | Notes |
|------|-------|
| Signal | `StartNewPaymentMethod` — required before a different 5-digit code |
| API | `POST /api/v1/orders/{id}/payment/new-method` |
| Activity | `RejectInFlightPayment` when timer wins over in-flight validation |
| Status | `PAYMENT_FAILED` terminal state when all methods exhausted |
| Query | `methods_used`, `methods_remaining`, extended `payment_events` |

### UI

- Submit is gated by cached payment code after 3 failures on a method
- Auto `POST .../payment/new-method` on submit when attempts are exhausted and code changes
- Method/attempt counters from `GetStatus`
- Payment events timeline
- Terminal messaging for `PAYMENT_FAILED` and `EXPIRED`

### Tests

All MVP-D tests pass (`go test ./...`).

| ID | Type | Scenario | Result |
|----|------|----------|--------|
| U-D1 | Unit | New method resets attempts | ✅ |
| U-D2 | Unit | 3×3 exhaustion → terminal | ✅ |
| U-D3 | Unit | 4th new-method rejected | ✅ |
| U-D4 | Unit | Timer rejects in-flight payment | ✅ |
| U-D5 | Unit | Different code without new-method | ✅ |
| I-D1 | Integration | S-3 method exhaustion | ✅ |
| I-D2 | Integration | S-4 late payment | ✅ |
| I-D3 | Integration | Fail 2× → new method → success | ✅ |
| I-D4 | Integration | Timer decrements during payment | ✅ |
| I-D5 | Integration | GET exposes payment counters | ✅ |
| I-D6 | Integration | 4th payment attempt → HTTP 400 | ✅ |
| I-D7 | Integration | New method when slots exhausted → 400 | ✅ |
| I-D8 | Integration | 3 failures → new method resets counter | ✅ |
| I-D9 | Integration | Different code without new method → 400 | ✅ |
| I-D10 | Integration | New method before payment → 400 | ✅ |

Manual steps: [manual_tests.md](manual_tests.md) §6 (6.1 and 6.2 confirmed).

---

## MVP-E — Complete ✅

**Scope:** Playwright E2E coverage for full stakeholder journeys (S-1 through S-5) with stable local test orchestration.
**Completed:** 2026-05-28

### Deliverables

- Extended Playwright suite in `tests/e2e/payment-attempts.spec.ts` to cover E-E1 through E-E7
- Added deterministic test server bootstrap helper: `tests/e2e/helpers/server.ts`
- Updated Playwright config for stable local execution: `playwright.config.ts`
- Verified full backend regression safety with `go test ./...`

### Tests

| ID | Type | Scenario | Result |
|----|------|----------|--------|
| E-E1 | E2E | S-1 via UI happy path | ✅ |
| E-E2 | E2E | S-2 timer refresh via UI | ✅ |
| E-E3 | E2E | S-3 attempt exhaustion state via UI | ✅ |
| E-E4 | E2E | S-4 timer vs in-flight payment race | ✅ |
| E-E5 | E2E | S-5 multi-flight isolation | ✅ |
| E-E6 | E2E | Multi-user map visibility (held seats) | ✅ |
| E-E7 | E2E | Single active-order guard + rebook | ✅ |

Validation commands:

- `npx playwright test tests/e2e/payment-attempts.spec.ts`
- `go test ./...`

---

## Run locally (Windows)

```powershell
$env:Path = "C:\Program Files\Go\bin;" + $env:Path
cd c:\Users\YanSh\Dev\Neon
go run ./cmd/api
# → http://localhost:8080
```

Optional: `$env:HOLD_DURATION = "2m"` for faster timer testing.

If port 8080 is busy: `netstat -ano | findstr ":8080"` then `Stop-Process -Id <PID> -Force`, or `$env:API_ADDR = ":8081"`.

---

## Agent handoff — copy to next AI agent

Use this block when onboarding a new agent:

> **Continue Neon on branch `dev`.** MVP-A through MVP-E are **implemented**.  
> Read `docs/progress.md`, `docs/final_plan.md`, and `docs/handoff.md` first.  
> If needed, run final manual UI verification and prepare release/readme polish.

### Architecture reminders

| Layer | Path | Must not |
|-------|------|----------|
| Presentation | `internal/api/`, `internal/web/` | Mutate seats directly; business rules |
| Service | `internal/workflow/booking/` | HTTP types |
| Data | `internal/infrastructure/memory/` | HTTP, workflow signals |

Temporal: namespace `flight-booking`, task queue `booking-task-queue`.

---

## Git state

```text
Branch: dev (up to date with origin/dev)
Latest commits:
  1954e2f fix(ui): preserve method-change rejection feedback on payment page
  8e07d94 fix(ui): remove new-method button; gate submit by cached payment code
  5d42d4a fix(ui): enable Proceed to payment when order is CREATED
  cfa1ffd fix(ui): remove seat-click server sync to eliminate blink
  7537eb1 fix(ui): restore payment submit after attempts exhausted
  7ff6e73 fix(ui): seat selection sync and payment attempt exhaustion UX
  2d4a617 feat(mvp-d): payment edge cases, timer on flight click, and booking UI
```

Push when ready: `git push origin dev`

---

## Next phase

| Phase | Focus |
|-------|-------|
| **Post MVP-E** | Optional manual verification, release polish, deferred infra backlog |

See [final_plan.md](final_plan.md) §8 for per-phase UI deliverables.
