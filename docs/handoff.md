# Agent Handoff — Neon Flight Booking

**Purpose:** Onboard a new AI agent to continue this project from the current stopping point.  
**Last updated:** 2026-05-27  
**Branch:** `dev` (up to date with `origin/dev`)

---

## Quick start (paste this to the agent)

```
Continue the Neon flight booking project on branch dev.

Read first:
  docs/handoff.md          (this file)
  docs/final_plan.md       (canonical architecture + phased MVPs)
  docs/final_requierments.md
  docs/progress.md         (phase checklist)
  docs/manual_tests.md     (MVP-C verification; reference for test style)

Status: MVP-A, MVP-B, MVP-C, and MVP-D are DONE and user-signed-off.
Start MVP-E only when the user explicitly asks.

MVP-D highlights (completed):
  - StartNewPaymentMethod signal + POST .../payment/new-method
  - 3 payment methods × 3 attempts exhaustion (S-3)
  - RejectInFlightPayment + timer-vs-payment race (S-4)
  - Edge-case UI (cached-code submit gating + payment events timeline)
  - Tests U-D1–U-D5, I-D1–I-D10 passing

Rules:
  - One phase at a time
  - Do NOT start MVP-E (Playwright E2E) until user confirms MVP-D
  - Presentation → Temporal only; side effects in activities → repos
  - Seat map is in-memory — restarts reset inventory to AVAILABLE
```

---

## Project summary

**Neon** is a multi-flight seat reservation system:

- **Stack:** Go (Gin API), Temporal workflows, static HTML/JS UI, in-memory repos (Postgres deferred)
- **Flow:** Pick flight → hold seats (15m refreshable timer) → pay with 5-digit code → seats BOOKED
- **Repo:** https://github.com/YanShlain/Neon

Phases are defined in [final_plan.md](final_plan.md). UI is built **incrementally per phase** (not all at the end).

---

## What is done

### MVP-A — Flight catalog & read-only seat map ✅

- `GET /api/v1/flights`, `GET /api/v1/flights/{id}/seats`
- Domain + in-memory repos, seed flights **101** / **102** (10×6 seats)
- Static UI at `/` and `/seats?flight_id=`
- Tests: U-A1–U-A6, I-A1–I-A4

### MVP-B — Holds, timer, cancel, booking UI ✅

- `BookingWorkflow` with hold timer, cancel, auto-expiry
- Activities: `HoldSeats`, `ReleaseSeats`
- Order API: `POST/PATCH/GET /orders`, `POST .../cancel`
- Workflow updates (sync HTTP): `UpdateSeats`, `CancelOrder`
- Hold conflict → non-retryable → **HTTP 409**
- Booking UI: seat selection, timer, cancel, `localStorage`, single-order guard
- Embedded Temporal dev server (`TEMPORAL_AUTO_DEV=1` default)
- Tests: U-B1–U-B7, I-B1–I-B5
- **User manual sign-off:** 2026-05-26

### MVP-C — Payment happy path ✅

- Activities: `ValidatePayment` (5-digit, 10s, 15% failure), `ConfirmSeats`
- `SubmitPayment` signal; states `SEATS_HELD` → `AWAITING_PAYMENT` → `CONFIRMED`
- `payment_events` / `payment_failures` on `GetStatus`
- `POST /api/v1/orders/{order_id}/payment` body `{ "code": "12345" }`
- Payment UI: `/payment`, proceed from seat map, confirmation view
- Test env: `PAYMENT_FAIL_UNTIL`, `PAYMENT_ALWAYS_FAIL`, `PAYMENT_NEVER_FAIL`, `PAYMENT_VALIDATION_DELAY`
- Tests: U-C1–U-C6, I-C1–I-C10
- **User manual sign-off:** 2026-05-26
- Manual guide: [manual_tests.md](manual_tests.md)

**Key commits:**

| Commit | Description |
|--------|-------------|
| `e18edbf` | MVP-A + read UI |
| `6f79c25` | MVP-B backend + booking UI |
| `c5a5d29` | MVP-C payment + tests + manual_tests.md |

---

## What to build next — MVP-E only

**MVP-D is signed off**. Next phase is MVP-E when requested by user.

### MVP-D completion notes (for context)

| Item | Details |
|------|---------|
| `StartNewPaymentMethod` signal | Used by UI submit flow when attempts are exhausted and code changes |
| API | `POST /api/v1/orders/{order_id}/payment/new-method` |
| Method/attempt tracking | Max 3 methods, max 3 attempts per method (S-3) |
| `RejectInFlightPayment` activity | Timer wins race over in-flight payment (S-4) |
| Workflow | Timer branch rejects payment; `payment_events` includes expiry rejections |
| Different code without new-method | Rejected before exhaustion (U-D5, I-D9) |

### UI deliverables (MVP-D, final)

- Submit gating by cached payment code after 3 failures
- Automatic new-method transition on submit when code changes after exhaustion
- Attempt/method counters from `GetStatus`
- **Payment events** timeline
- Timer visible during `AWAITING_PAYMENT` (never pauses) — polish if needed

See [final_plan.md](final_plan.md) § MVP-D.

### Acceptance tests (all passing)

**Unit** — [final_plan.md](final_plan.md) § MVP-D: U-D1–U-D5

**Integration:** I-D1–I-D10

Run: `go test ./...`

---

## Repository map

```
cmd/
  api/main.go              # HTTP server + embedded worker (use this for dev)
  worker/main.go           # Standalone worker (future Postgres)

domain/
  seat.go, repository.go, order.go

internal/
  api/
    handler/orders.go      # Orders + POST .../payment
    handler/flights.go
    order_integration_test.go
  app/application.go
  infrastructure/
    memory/                # In-memory repos (not persistent across restarts)
    temporal/              # OrderService, dev server
  workflow/booking/
    workflow.go            # BookingWorkflow + SubmitPayment signal
    activities.go          # Hold/Release/ValidatePayment/ConfirmSeats
    payment.go             # Payment RNG + format helpers
    workflow_test.go
  web/static/
    payment.html, js/payment.js
    seats.html, js/seats.js

docs/
  final_plan.md
  progress.md
  handoff.md               # This file
  manual_tests.md          # MVP-C manual + curl steps
```

---

## Architecture rules

| Layer | May call | Must NOT |
|-------|----------|----------|
| **Presentation** (`internal/api/`, `internal/web/`) | Temporal client, repos for **read-only** seat map | Direct seat mutation, payment logic |
| **Service** (`internal/workflow/booking/`) | Activities, workflow APIs | Gin, HTTP types |
| **Data** (`internal/infrastructure/memory/`) | Storage | Business rules, HTTP |

**Locked decisions:**

- Temporal namespace: `flight-booking`
- Task queue: `booking-task-queue`
- Workflow ID = `order_id` (UUID)
- Seat map reads bypass Temporal: `GET .../seats` → `SeatRepository`
- Seat writes only via activities
- Timer starts on **POST /orders** (flight click); refreshes on seat change; **never pauses** during payment
- `HOLD_DURATION` env overrides hold timer (default 15m; use `30s` in tests)
- **Inventory:** in-memory per API process — restart clears BOOKED/HELD seats

---

## Key files to read before MVP-E

1. [final_plan.md](final_plan.md) §2.5 — signals, selector loop, payment rules
2. [final_plan.md](final_plan.md) § MVP-D — acceptance tests
3. `internal/workflow/booking/workflow.go` — selector + payment flow (MVP-C baseline)
4. `internal/workflow/booking/payment.go` — RNG test hooks
5. `internal/infrastructure/temporal/order_service.go` — SignalWorkflow / polling pattern
6. `internal/api/handler/orders.go` — payment handler + error mapping

---

## Development setup (Windows)

```powershell
$env:Path = "C:\Program Files\Go\bin;" + $env:Path
cd c:\Users\YanSh\Dev\Neon

go test ./...
go run ./cmd/api       # http://localhost:8080
```

**Environment variables:**

| Variable | Default | Purpose |
|----------|---------|---------|
| `TEMPORAL_AUTO_DEV` | `1` in bootstrap | Embed Temporal dev server |
| `TEMPORAL_HOST` | `127.0.0.1:7233` | External Temporal if set |
| `HOLD_DURATION` | `15m` | Hold timer length |
| `API_ADDR` | `:8080` | HTTP listen address |
| `PAYMENT_FAIL_UNTIL` | — | Test: fail first N RNG calls |
| `PAYMENT_ALWAYS_FAIL` | — | Test: always fail validation |
| `PAYMENT_NEVER_FAIL` | — | Test: never fail validation |

**Port conflict:** `netstat -ano | findstr ":8080"` → `Stop-Process -Id <PID> -Force`

---

## Suggested implementation order for MVP-E

1. Confirm MVP-E scope in `final_plan.md`
2. Add/expand Playwright specs for end-to-end booking/payment paths
3. Cover edge cases already validated in MVP-D (including different-code rejection)
4. Validate responsive and UX polish tasks
5. Run test suite and update docs/checklists

---

## Process rules (mandatory)

1. **One MVP phase at a time** — currently **MVP-E** when user approves
2. **`go test ./...` green** before declaring done
3. **Manual test steps** at phase end (extend [manual_tests.md](manual_tests.md) or add MVP-D section)
4. **Wait for user confirmation** before starting/changing MVP-E scope
5. **Surgical changes** — match existing style
6. **Commit** when user asks (they push separately)

---

## After MVP-D (do not start without user OK)

| Phase | Focus |
|-------|-------|
| **MVP-E** | Playwright E2E (E-E1–E-E7), responsive polish |

---

## Related docs

- [final_plan.md](final_plan.md) — canonical architecture
- [final_requierments.md](final_requierments.md) — locked requirements
- [progress.md](progress.md) — living phase status
- [manual_tests.md](manual_tests.md) — MVP-C manual verification
