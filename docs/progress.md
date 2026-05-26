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
| **MVP-B** | Holds, timer, cancel, booking UI | **Done** (user signed off) | U-B1–U-B7, I-B1–I-B5 ✅ |
| **MVP-C** | Payment happy path | **Done** (awaiting manual sign-off) | U-C1–U-C6, I-C1–I-C10 ✅ |
| **MVP-D** | Payment edge cases | Not started | U-D1–U-D5, I-D1–I-D4 |
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
- Hold timer countdown (client-side)
- Own holds highlighted (blue); others' HELD/BOOKED grayscale
- Cancel order; single active order guard on flight list

### Tests

All MVP-B tests pass (`go test ./...`).

---

## MVP-C — Complete ✅ (awaiting manual sign-off)

See [final_plan.md](final_plan.md) § MVP-C.

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

Manual steps: [manual_tests.md](manual_tests.md).

**Not in scope (MVP-D):** `StartNewPaymentMethod`, timer-vs-payment race rejection, 3×3 method exhaustion.

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

> **Continue Neon on branch `dev`.** MVP-A, MVP-B, and MVP-C are implemented; MVP-C awaits manual sign-off.  
> **Next task: MVP-D only** after user confirms MVP-C (see `docs/handoff.md`).  
> Read `docs/progress.md`, `docs/final_plan.md`, and `docs/handoff.md` first.  
> **Do not start MVP-D** until the user explicitly approves.

### MVP-C manual test checklist

See **[manual_tests.md](manual_tests.md)** for UI steps, curl commands, and sign-off checklist.

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
Branch: dev
Latest commits:
  6f79c25 feat(mvp-b): add seat holds, timer, cancel, and booking UI
  e18edbf feat(ui): add MVP-A read-only web UI and per-phase UI plan
```

Push when ready: `git push origin dev`

---

## Future phases (after MVP-C)

| Phase | Focus |
|-------|-------|
| **MVP-D** | Payment edge cases (3 methods, timer/payment race S-3/S-4) |
| **MVP-E** | Playwright E2E, responsive polish |

See [final_plan.md](final_plan.md) §8 for per-phase UI deliverables.
