# Engineering Manager Code Review — Neon Flight Booking System

**Reviewer:** Engineering Manager (SWE Audit)  
**Date:** 2026-05-29  
**Codebase state:** Current implementation — cross-checked against `docs/design_overview.md`, `docs/final_requierments.md`, `docs/final_review.md`, and `go test ./... -count=1` (pass, ~92s)

---

## Engineering Manager Evaluation Matrix

| Dimension | Grade | Notes |
|-----------|-------|-------|
| **Concurrency Safety** | **A-** | Per-flight `sync.RWMutex` with single-lock write paths is correct. No manual `RUnlock` landmines. Read path copies `domain.Seat` by value — no slice aliasing. Missing: `-race` stress tests; `flightInv` lookup window is safe today but undocumented. |
| **Architectural Decoupling** | **A** | Clean 3-tier split. `domain.SeatRepository` swappable. Payment uses Workflow Updates (not signals). `domain.IsValidPaymentCode` is shared. HTTP logging middleware exists. |
| **State Reliability** | **B-** | `ReconcileHolds` on startup is a thoughtful mitigation for HELD seats. Still in-memory: BOOKED inventory lost on restart, split API/worker processes diverge, reconciliation skips conflicts silently. |
| **Go Idiomatic Quality** | **A-** | `defer` unlocks, context-aware activity delay, typed domain errors, injected `PaymentRNG`, deterministic workflow timers. Worker uses default `worker.Options{}` with no backpressure tuning. |

**Overall verdict: Senior Engineer — credible Tech Lead trajectory.** The central-repo + Temporal-orchestration bet is defensible and well-executed for a take-home. What blocks Tech Lead sign-off is operational state durability and a few production-scale blind spots, not core booking logic.

---

## Ruthless Code Review Findings

### CRITICAL — C1: Process restart drops BOOKED seats; reconciliation does not restore them

**Files:** `internal/app/reconcile.go` (lines 44–46), `internal/infrastructure/memory/seat_repository.go` (entire store)

`ReconcileHolds` only replays **non-terminal orders with held seats**:

```44:46:internal/app/reconcile.go
			if status.Status.IsTerminal() || len(status.HeldSeatIDs) == 0 {
				continue
			}
```

After a successful payment, workflow status is `CONFIRMED` (terminal). `ConfirmSeats` has already run in activity history, but the in-memory map is wiped on restart. Seat `1A` returns to `AVAILABLE` in `GET /flights/{id}/seats` while Temporal still shows a confirmed order.

**Impact:** Silent double-booking vector. A new user can hold and confirm the same seat. The original workflow is terminal — nothing re-runs `Confirm`. This is worse than the HELD-seat gap (which reconciliation partially fixes) because there is no compensating path at all.

For a cyber startup interview: you chose in-memory MVP — fine. But production trust requires either durable inventory or reconcile of `CONFIRMED` → `BOOKED` as well. The codebase acknowledges HELD drift (`reconcile.go` comment, `application.go:75–77` split-deploy warning) but not BOOKED drift.

---

### HIGH — H1: Split API/worker deployment = two inventories, one Temporal

**Files:** `cmd/api/main.go`, `cmd/worker/main.go`, `internal/app/application.go` (lines 75–77), `internal/api/handler/flights.go` (direct `SeatRepository` reads)

Activities mutate `SeatRepository` inside the **worker process**. `GET /flights/{id}/seats` reads `SeatRepository` inside the **API process**. These are different heap allocations unless you run the embedded worker (default `cmd/api`).

The warning at bootstrap is honest:

```75:77:internal/app/application.go
	if opts.StartWorker && os.Getenv("NEON_ROLE") == "api" && os.Getenv("TEMPORAL_AUTO_DEV") == "0" {
		slog.Warn("split deployment with in-memory seats: run a single API+worker process or use durable storage")
	}
```

But nothing **fails fast**. An operator can deploy `cmd/worker` + `cmd/api`, run reconcile on both, and still diverge on the first `SwapSeats` activity vs first seat-map GET. Temporal says HELD; API says AVAILABLE.

**Impact:** Architecture doc read/write split is correct for latency, but only with **shared durable storage** or **single co-located process**. As written, horizontal scale of API replicas each carries its own fiction of inventory.

---

### HIGH — H2: Reconciliation conflicts are logged and swallowed

**File:** `internal/app/reconcile.go` (lines 47–55)

When `ApplyHold` hits `ErrHoldConflict` (two running workflows believe they hold the same seat after partial corruption), reconciliation logs `Warn` and **continues**. No alert, no workflow termination, no ops hook.

```47:55:internal/app/reconcile.go
			if err := seats.ApplyHold(ctx, status.FlightID, orderID, status.HeldSeatIDs); err != nil {
				slog.Warn("reconcile hold apply failed",
					"order_id", orderID,
					"flight_id", status.FlightID,
					"seat_ids", status.HeldSeatIDs,
					"error", err,
				)
				continue
			}
```

**Impact:** Two orders can coexist in Temporal with overlapping `HeldSeatIDs` while memory picks a winner arbitrarily. In production this is a data-integrity incident, not a log line.

---

### MEDIUM — M1: Per-flight mutex enables targeted DoS (scoped, not global)

**File:** `internal/infrastructure/memory/seat_repository.go` (lines 20–23, 96–97, 189–190)

Write paths (`TryHold`, `SwapHold`, `Release`, `Confirm`) serialize on `flightInventory.mu`. This is **not** a global lock — other flights are unaffected. Good.

However: an attacker hammering `PATCH /orders/{id}/seats` on a hot flight queues Temporal updates → activities → blocked goroutines on that flight's mutex. Default Temporal worker concurrency (`worker.Options{}` in `runtime.go:60`) allows many parked activity goroutines. Combined with synchronous `UpdateWorkflow` on the HTTP thread, this becomes **latency amplification** on one flight and **worker slot exhaustion**, not a cluster-wide deadlock.

The old audit's claim about a global write lock blocking all flights is **obsolete** — current code does not do that.

---

### MEDIUM — M2: No concurrency verification in test suite

**Files:** `internal/infrastructure/memory/seat_repository_test.go` (unit tests only), entire repo (no `-race` tests)

Repository tests are sequential. No `testing`-parallel stress test, no `go test -race` harness. For a submission that foregrounds mutex correctness, the absence of a concurrent `TryHold`/`ListByFlight` torture test is a gap. The design looks right; you didn't prove it under the race detector.

---

### MEDIUM — M3: `StartNewPaymentMethod` consumes a method slot before validation completes

**File:** `internal/workflow/booking/workflow.go` (lines 112–121)

```112:121:internal/workflow/booking/workflow.go
	if err := workflow.SetUpdateHandlerWithOptions(ctx, UpdateStartNewPaymentMethod,
		func(updateCtx workflow.Context) (StatusResponse, error) {
			state.MethodsUsed++
			if state.MethodsUsed >= maxPaymentMethods {
				if failErr := failOrderPaymentExhausted(activityCtx(updateCtx), &state, state.CurrentCode); failErr != nil {
```

`MethodsUsed++` happens **unconditionally** at the start of the handler (after the validator). Calling `new-method` when `MethodsUsed == 2` immediately triggers `PAYMENT_FAILED` without a third code ever being tried. That matches locked requirements ("changing the code … consumes one of the 3 allowed method slots") but means total validation attempts can fall **below 9** if the user switches early. Documented in tests (`TestU_D1`); worth calling out as product semantics, not a bug.

---

### MEDIUM — M4: Observability is improved but not production-grade

**Files:** `internal/api/router.go` (lines 15–33, `requestLogger`), `internal/infrastructure/temporal/order_service.go` (outbound RPC logging on every query)

HTTP middleware with `X-Request-ID` is the right pattern — fixed since the prior audit. Remaining gaps:

- Every `GetStatus` / SSE tick logs `outbound temporal QueryWorkflow` — noisy at 1 Hz × N clients.
- No metrics hooks (latency histograms, hold conflict rate, reconcile failures).
- Activity/workflow layer has no structured correlation ID from HTTP → Temporal.

Structure **allows** middleware extension; implementation is MVP-adequate, not scale-ready.

---

### LOW — L1: `flightInv` releases registry lock before acquiring flight lock

**File:** `internal/infrastructure/memory/seat_repository.go` (lines 58–67, 90–97)

```58:67:internal/infrastructure/memory/seat_repository.go
func (r *SeatRepository) flightInv(flightID string) (*flightInventory, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	inv, ok := r.flights[flightID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrFlightNotFound, flightID)
	}
	return inv, nil
}
```

Between `flightInv` return and `inv.mu.Lock()`, another goroutine cannot delete the flight (no delete API). Pointer remains valid. Safe today; fragile if someone adds flight removal without lifecycle audit.

---

### LOW — L2: Read path accepts stale seat maps by design — acceptable with eyes open

**File:** `internal/infrastructure/memory/seat_repository.go` (lines 70–86)

`ListByFlight` takes `inv.mu.RLock()`, copies each `domain.Seat` by value into a new slice, sorts. No pointer escape. Concurrent writes block until read completes — snapshot is internally consistent. UI may lag behind workflow state by one HTTP round-trip; that matches `design_overview.md` §2.1 read/write split. Not a bug.

---

### RESOLVED — Payment 3×3 model (prior audit gap closed)

**Cross-check vs `final_review.md` FR-2:** The earlier "3 total failures" cutoff **is fixed** in current code.

**Evidence:**

- `internal/workflow/booking/payment.go:10–14` — `maxAttemptsPerMethod = 3`, `maxPaymentMethods = 3`
- `internal/workflow/booking/workflow.go:215–239` — per-code failure counter, method exhaustion, `attempts_exhausted` events
- `internal/api/order_integration_test.go:577–618` — `TestI_D1` fails three codes three times each → `PAYMENT_FAILED`
- `internal/workflow/booking/workflow_test.go:428–453`, `622–640` — unit coverage for method counters

Payment uses synchronous **Workflow Updates** (`order_service.go:106–147`), not signal+polling. Timer race S-4 handled inline in `handleTimerExpiry` (`workflow.go:323–334`) without a no-op activity.

---

### RESOLVED — Mutex implementation (prior C1 obsolete)

Current `TryHold` / `Release` / `Confirm` hold `inv.mu.Lock()` with `defer inv.mu.Unlock()` for the entire validate+mutate critical section. No split-phase global RLock/Lock dance. Example:

```96:108:internal/infrastructure/memory/seat_repository.go
	inv.mu.Lock()
	defer inv.mu.Unlock()

	if err := validateAvailable(inv.seats, seatIDs); err != nil {
		return err
	}
	for _, seatID := range seatIDs {
		seat := inv.seats[seatID]
		seat.Status = domain.SeatStatusHeld
		seat.OrderID = orderID
		inv.seats[seatID] = seat
	}
```

No deadlock cycle: registry `r.mu` and flight `inv.mu` are never held together.

---

## Temporal Logic & Edge-Case Integrity

### Timer determinism — PASS

**File:** `internal/workflow/booking/workflow.go` (lines 270–319)

- Deadline from `workflow.Now(ctx).Add(HoldDuration)` — deterministic.
- `workflow.NewTimer` + selector with `resetCh` — stale timer firings ignored via `state.TimerDeadline.Equal(deadline)` guard (line 296).
- Timer reset on seat PATCH via `notifyTimerReset()` — correct S-2 behavior.
- `applySeatUpdate` transitions empty seat list back to `CREATED` (lines 382–386) — fixed state-machine hygiene.

### Payment state machine — PASS (with M3 semantics noted)

Update validators reject payment in wrong states before workflow task creation. `AWAITING_PAYMENT` blocks seat PATCH. Post-activity check for terminal status handles S-4 timer race. `MethodsUsed` / `MethodsRemaining` exposed in API.

### Activity layer — PASS

`ValidatePayment` uses context-aware delay (`activities.go:77–81`). Non-retryable application errors for hold conflicts and confirm failures — correct Temporal hygiene.

---

## Tech Lead Architectural Verdict

### Domain interface longevity — HOLDS UP

**File:** `domain/repository.go`

```6:13:domain/repository.go
type SeatRepository interface {
	ListByFlight(ctx context.Context, flightID string) ([]Seat, error)
	TryHold(ctx context.Context, flightID string, seatIDs []string, orderID string) error
	SwapHold(ctx context.Context, flightID, orderID string, releaseIDs, holdIDs []string) error
	ApplyHold(ctx context.Context, flightID, orderID string, seatIDs []string) error
	Release(ctx context.Context, flightID string, seatIDs []string, orderID string) error
	Confirm(ctx context.Context, flightID string, seatIDs []string, orderID string) error
}
```

Maps cleanly to PostgreSQL: `ListByFlight` → `SELECT`; `SwapHold`/`TryHold` → transaction with `SELECT … FOR UPDATE` + conditional `UPDATE`; `ApplyHold` → startup reconciliation or idempotent upsert. Workflow and activities require **no structural rewrite** — only a new `internal/infrastructure/postgres` package.

**Gap:** Interface comments do not document **atomic multi-seat** requirement. A naive per-seat UPDATE implementation could reintroduce TOCTOU. Add contract docs, not new methods.

`ApplyHold` is reconciliation-specific — acceptable on the interface for MVP; in production you might move it to a separate `HoldReconciler` port to keep the core repo pure.

### Observability readiness — PARTIAL

HTTP middleware: yes. Distributed tracing from request → workflow → activity: no. Metrics: no. Reconcile failure alerting: no. You won't fly blind, but you won't sleep through on-call either.

### Rating: **Senior Engineer**

**Not Junior:** Temporal update handlers, selector timer loop, per-flight locking, 3×3 payment, integration test matrix S-1..S-5, reconciliation awareness — this is beyond mid-level glue code.

**Not yet Tech Lead:** A Tech Lead documents and gates the in-memory foot-guns (BOOKED loss, split deploy) with fail-fast bootstrap or feature flags, adds `-race` coverage for the mutex story you're selling, and designs reconcile to handle conflicts as incidents — not warnings.

Fix C1 + H1 + H2 for production credibility; add race stress tests; this becomes a strong Senior+/Staff take-home.

---

## Production Refactoring Prescription

### Fix 1 — Extend reconciliation to BOOKED seats (C1)

```go
// internal/app/reconcile.go — after existing hold replay loop
func ReconcileBooked(ctx context.Context, c client.Client, seats domain.SeatRepository) error {
	req := &workflowservice.ListWorkflowExecutionsRequest{
		Namespace: booking.Namespace,
		Query: fmt.Sprintf(
			`WorkflowType = %q AND ExecutionStatus = "Completed" AND CloseStatus = "Completed"`,
			booking.WorkflowName,
		),
	}
	// Paginate; for each workflow query final status via history or persisted memo.
	// If status == CONFIRMED, call seats.Confirm(ctx, flightID, heldSeatIDs, orderID).
	// Idempotent Confirm when already BOOKED for same orderID.
	return nil
}
```

Better long-term: **durable seat inventory as source of truth**; Temporal owns order lifecycle only; `Confirm` writes to Postgres with unique `(flight_id, seat_id)` constraint.

---

### Fix 2 — Fail fast on split deploy with in-memory seats (H1)

```go
// internal/app/application.go — in BootstrapApp, after repos wired
if opts.StartWorker && os.Getenv("NEON_ROLE") == "api" {
	if os.Getenv("ALLOW_SPLIT_INMEMORY") != "1" {
		return nil, fmt.Errorf(
			"refusing split API/worker with in-memory SeatRepository: "+
				"set ALLOW_SPLIT_INMEMORY=1 to override or use durable storage",
		)
	}
}
```

For production: single binary or shared Redis/Postgres seat store — not two reconciled copies.

---

### Fix 3 — Treat reconcile conflicts as fatal or operational events (H2)

```go
if err := seats.ApplyHold(ctx, status.FlightID, orderID, status.HeldSeatIDs); err != nil {
	if errors.Is(err, domain.ErrHoldConflict) {
		// Option A: fail bootstrap so orchestrator restarts pod
		return fmt.Errorf("reconcile conflict order=%s seats=%v: %w", orderID, status.HeldSeatIDs, err)
		// Option B: emit metric + cancel loser workflow via Temporal API
	}
	return fmt.Errorf("reconcile apply order=%s: %w", orderID, err)
}
```

---

### Fix 4 — Add race-detector torture test (M2)

```go
func TestSeatRepository_ConcurrentTryHoldNoDoubleBook(t *testing.T) {
	seats := memory.NewSeatRepository()
	// seed one flight, one seat
	const flight = "NA4821"
	const seat = "1A"
	// initFlight(...)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_ = seats.TryHold(context.Background(), flight, []string{seat}, fmt.Sprintf("O%d", id))
		}(i)
	}
	wg.Wait()

	held, _ := seats.ListByFlight(context.Background(), flight)
	var holders int
	for _, s := range held {
		if s.SeatID == seat && s.Status == domain.SeatStatusHeld {
			holders++
		}
	}
	if holders != 1 {
		t.Fatalf("want exactly 1 holder, got %d", holders)
	}
}
```

Run in CI: `go test -race ./internal/infrastructure/memory/...`

---

### Fix 5 — PostgreSQL repository skeleton (interface-compatible)

```go
func (r *PostgresSeatRepository) TryHold(ctx context.Context, flightID string, seatIDs []string, orderID string) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `
		SELECT seat_id, status, order_id FROM seats
		WHERE flight_id = $1 AND seat_id = ANY($2)
		FOR UPDATE`, flightID, seatIDs)
	// validate all AVAILABLE, then UPDATE all in same tx
	return tx.Commit(ctx)
}
```

Workflow layer unchanged. Atomicity enforced by transaction, not mutex.

---

### Fix 6 — Worker backpressure for hot-flight DoS (M1)

```go
w := worker.New(r.Client, booking.TaskQueue, worker.Options{
	MaxConcurrentActivityExecutionSize: 100,
})
```

Combine with API rate limiting per `flight_id` on `PATCH .../seats`. Mutex serialization is correct; unbounded concurrency is the amplifier.

---

## Summary

You made the right architectural bet for a take-home: **central inventory + Temporal for order lifecycle**, not per-seat entity workflows. The mutex story in `seat_repository.go` is clean. The payment model now matches locked requirements (3×3). Temporal timers and S-4 race handling are solid.

What would make me approve this in a production design review tomorrow:

1. Durable inventory (or full reconcile including BOOKED).
2. Explicit split-deploy prohibition or shared store.
3. Race-detector proof for concurrent holds.
4. Reconcile conflicts elevated from `Warn` to incident.

Ship this as an MVP demo — strong Senior work. Come back with durability and ops story — that's Tech Lead.

---

*Prior audit in this file (pre-2026-05-29) referenced signal+polling payment and manual `TryHold` unlocks — both superseded. Delivery QA status: [final_review.md](final_review.md).*
