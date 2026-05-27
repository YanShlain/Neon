# Neon — Implementation Progress

**Last updated:** 2026-05-26  
**Branch:** `dev` (1 commit ahead of `origin/dev` as of last update)  
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
| **MVP-D** | Payment edge cases | **Done** (awaiting sign-off) | U-D1–U-D5, I-D1–I-D10 ✅ |
| **MVP-E** | E2E polish | Not started | E-E1–E-E7 |

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

## MVP-D — Complete (awaiting manual sign-off)

**Scope:** Payment edge cases — 3 methods × 3 attempts, timer/payment race (S-3, S-4), new-method API and UI.

### Backend

| Area | Notes |
|------|-------|
| Signal | `StartNewPaymentMethod` — required before a different 5-digit code |
| API | `POST /api/v1/orders/{id}/payment/new-method` |
| Activity | `RejectInFlightPayment` when timer wins over in-flight validation |
| Status | `PAYMENT_FAILED` terminal state when all methods exhausted |
| Query | `methods_used`, `methods_remaining`, extended `payment_events` |

### UI

- **Try new payment method** button → `POST .../payment/new-method`
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

Manual steps: [manual_tests.md](manual_tests.md) §6.

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

> **Continue Neon on branch `dev`.** MVP-A through MVP-D are **implemented**; MVP-D awaits manual sign-off.  
> **Next task: MVP-E only** (Playwright E2E) — when user confirms MVP-D.  
> Read `docs/progress.md`, `docs/final_plan.md`, and `docs/handoff.md` first.

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
Branch: dev (ahead of origin/dev by 1 commit as of last update)
Latest commits:
  c5a5d29 feat(mvp-c): payment happy path with tests and manual guide
  9adfc25 docs: add agent handoff guide for MVP-C continuation
  6f79c25 feat(mvp-b): add seat holds, timer, cancel, and booking UI
  e18edbf feat(ui): add MVP-A read-only web UI and per-phase UI plan
```

Push when ready: `git push origin dev`

---

## Next phase — MVP-E (not started)

| Phase | Focus |
|-------|-------|
| **MVP-E** | Playwright E2E, responsive polish |

See [final_plan.md](final_plan.md) §8 for per-phase UI deliverables.
