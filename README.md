# Neon

A multi-flight seat reservation and payment system orchestrated by [Temporal](https://temporal.io/). Users browse flights, hold seats on a 15-minute refreshable timer, and complete booking with a simulated 5-digit payment code.

**Requirements:** [docs/final_requierments.md](docs/final_requierments.md) · **Architecture:** [docs/final_plan.md](docs/final_plan.md)

---

## Project overview

Neon lets anonymous users book seats on one or more flights. Each flight has its own seat inventory — seat `1A` on Flight 101 is independent from `1A` on Flight 102.

### Booking flow

1. **Select a flight** — creates an order and starts a 15-minute hold timer.
2. **Choose seats** — holds are applied per flight; the timer **resets to a full 15 minutes** on every seat change.
3. **Pay** — submit a 5-digit code; validation runs with a 10-second timeout and a 15% simulated failure rate.
4. **Confirm or fail** — successful payment moves seats from `HELD` to `BOOKED`; timer expiry or exhausted payment attempts release all held seats.

### Payment rules

| Rule | Detail |
|------|--------|
| Code format | Exactly 5 digits |
| Attempts per method | Up to 3 tries with the same code |
| Methods per order | Up to 3 different codes (changing code requires **Try new payment method**) |
| Timer during payment | The 15-minute timer **never pauses**, even while payment is validating |

### Order states

| State | Meaning |
|-------|---------|
| `CREATED` | Order started; timer running; no seats held yet |
| `SEATS_HELD` | Seats held; timer running (refreshes on seat changes) |
| `AWAITING_PAYMENT` | Payment validation in progress; timer still running |
| `CONFIRMED` | Terminal success — seats booked |
| `EXPIRED` | Terminal failure — timer reached zero |
| `CANCELLED` | Terminal failure — user cancelled |

---

## Design overview

Neon follows a **three-tier model** where Temporal owns orchestration in the service layer. The web frontend talks to a Go REST API; the API starts, signals, and queries Temporal workflows; activities perform side effects through repository interfaces.

```mermaid
flowchart TB
  subgraph presentation [Presentation — Gin REST + Temporal Client]
    R[Routers / DTOs]
    TC[Temporal Client]
  end
  subgraph service [Service — Temporal]
    WF[BookingWorkflow]
    ACT[Activities]
  end
  subgraph data [Data — Repository Adapters]
    SI[SeatRepository]
    FI[FlightRepository]
    MEM[In-Memory Adapter]
  end
  UI[Web Frontend] --> R
  R --> TC
  TC -->|start / signal / query| WF
  WF --> ACT
  ACT --> SI
  ACT --> FI
  MEM -.implements.-> SI
  MEM -.implements.-> FI
  R -->|GET seats only| SI
```

### Layer responsibilities

| Layer | Technology | Owns |
|-------|------------|------|
| **Presentation** | Go (Gin), Temporal Client | HTTP routes, DTOs, workflow start/signal/query, static web UI |
| **Service** | Temporal Workflow + Activities | Order state machine, timer, payment retry rules, hold/release/book |
| **Data** | Repository interfaces | Seat and flight inventory (in-memory for MVP) |

### Key design decisions

- **Single workflow per order** — one `BookingWorkflow` owns the full lifecycle (timer, holds, payment, expiry). Workflow ID equals `order_id`.
- **Read/write split for seats** — `GET /api/v1/flights/{flight_id}/seats` reads the seat repository directly; all seat mutations go through Temporal activities.
- **Signals and queries** — `UpdateSeats`, `SubmitPayment`, `StartNewPaymentMethod`, `CancelOrder`; status via `GetStatus` query.
- **Two binaries** — `cmd/api` (HTTP + embedded UI) and `cmd/worker` (Temporal worker). For local development, `cmd/api` bootstraps an embedded Temporal dev server and starts the worker automatically.
- **MVP storage** — in-memory repositories seeded at startup (≥2 flights). Postgres and Redis are deferred to post-MVP.

### API surface

| Method | Path | Purpose |
|--------|------|---------|
| GET | `/api/v1/flights` | List flights |
| GET | `/api/v1/flights/{flight_id}/seats` | Seat map (`?order_id=` highlights caller's holds) |
| POST | `/api/v1/orders` | Start booking (`{ "flight_id" }`) |
| PATCH | `/api/v1/orders/{order_id}/seats` | Update held seats |
| POST | `/api/v1/orders/{order_id}/payment/new-method` | Switch to a new payment code |
| POST | `/api/v1/orders/{order_id}/payment` | Submit 5-digit payment code |
| POST | `/api/v1/orders/{order_id}/cancel` | Cancel order |
| GET | `/api/v1/orders/{order_id}` | Order status, timer, payment events |

The static web UI is served from the API at `/` (flight list → seat map → payment → confirmation).

For sequence diagrams, signal internals, and phased MVP delivery, see [docs/final_plan.md](docs/final_plan.md).

---

## Running locally

### Prerequisites

- **Go 1.24+**
- No external Temporal server required — the API embeds a dev server by default.

### Quick start

```powershell
cd c:\Users\YanSh\Dev\Neon

go test ./...
go run ./cmd/api
```

Open **http://localhost:8080** in your browser.

The API seeds flight inventory, starts an embedded Temporal dev server (`TEMPORAL_AUTO_DEV=1`), registers the booking worker on task queue `booking-task-queue`, and serves the web UI.

### Environment variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `TEMPORAL_AUTO_DEV` | `1` | Embed Temporal dev server when no external server is reachable |
| `TEMPORAL_HOST` | `127.0.0.1:7233` | External Temporal address (used when auto-dev is off) |
| `HOLD_DURATION` | `15m` | Hold timer length (`30s` or `2m` useful for manual testing) |
| `API_ADDR` | `:8080` | HTTP listen address |

Optional test hooks for payment simulation:

| Variable | Purpose |
|----------|---------|
| `PAYMENT_NEVER_FAIL` | Always succeed payment validation |
| `PAYMENT_ALWAYS_FAIL` | Always fail payment validation |
| `PAYMENT_FAIL_UNTIL` | Fail the first N RNG calls, then succeed |

Example — shorter timer for manual testing:

```powershell
$env:HOLD_DURATION = "2m"
go run ./cmd/api
```

### Troubleshooting

**Port 8080 already in use**

```powershell
netstat -ano | findstr ":8080"
Stop-Process -Id <PID> -Force
```

Or use a different port:

```powershell
$env:API_ADDR = ":8081"
go run ./cmd/api
```

**Inventory resets on restart** — seat holds and bookings live in memory per API process. Restarting the server clears all held and booked seats.

### Running the worker separately

For a split deployment, run an external Temporal server and start the worker independently:

```powershell
$env:TEMPORAL_AUTO_DEV = "0"
$env:TEMPORAL_HOST = "127.0.0.1:7233"
go run ./cmd/worker
```

In typical local development, `go run ./cmd/api` is sufficient.

---

## Project layout

```
cmd/
  api/          HTTP server + embedded UI
  worker/       Temporal worker (optional split)
internal/
  api/          Gin routes, handlers, DTOs
  app/          Bootstrap and wiring
  domain/       Core types and repository interfaces
  infrastructure/
    memory/     In-memory flight/seat repositories
    temporal/   Temporal client, dev server, order service
  workflow/
    booking/    BookingWorkflow, activities, payment logic
  web/          Static HTML/JS/CSS (embedded)
docs/
  final_requierments.md   Locked functional requirements
  final_plan.md           Architecture and MVP phases
```

---

## Documentation

| Document | Description |
|----------|-------------|
| [docs/final_requierments.md](docs/final_requierments.md) | Functional requirements, state machine, scenarios |
| [docs/final_plan.md](docs/final_plan.md) | Three-tier architecture, API contract, MVP phases, test matrix |
| [docs/manual_tests.md](docs/manual_tests.md) | Step-by-step manual test scripts |
| [docs/handoff.md](docs/handoff.md) | Developer handoff notes and key file index |
