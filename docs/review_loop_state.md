# Review Loop State

> Maintained by `/review-loop`. Do not delete — agents read this for continuity between cycles.

## Meta

| Field | Value |
|-------|-------|
| **Last cycle** | 2026-05-29 (post-push automation) |
| **Last reviewed commit** | `880ecbf35a84a6174596b2e8ffb42392ac1d0d8d` |
| **Trigger** | push to `dev` by YanShlain |
| **Verdict** | **READY** |
| **Loop mode** | automation (push) |

## Test baseline

```
Last run: 2026-05-29
Command: go test ./... -count=1 -timeout 120s
Result: PASS (exit 0)
Packages:
  ok  neon/internal/api
  ok  neon/internal/api/handler
  ok  neon/internal/infrastructure/memory
  ok  neon/internal/workflow/booking
Note: TestU_D6_NewMethodRequiredForDifferentCode — 5× re-run PASS (prior flake not reproduced).
```

## Scenario coverage (S-1..S-5)

| Scenario | Description | Test(s) | Status |
|----------|-------------|---------|--------|
| S-1 | Happy path | `TestI_C1_PaymentHappyPath`, `TestU_C1_PaymentSuccessConfirmsSeats` | PASS |
| S-2 | Timer refresh on seat change | `TestI_B1_TimerRefreshAfterSeatChange`, `TestU_B2_SeatChangeResetsTimer` | PASS |
| S-3 | Method exhaustion (3×3) | `TestI_D1_AttemptExhaustionReleasesSeats`, `TestU_C3_PaymentAttemptsExhausted` (partial unit) | PASS |
| S-4 | Late payment / timer expiry during payment | `TestI_D2_LatePaymentRejectedOnExpiry`, `TestU_D4_TimerRejectsInFlightPayment` | PASS |
| S-5 | Multi-flight isolation | `TestI_B2_MultiFlightHoldIsolation`, `TestU_B7_IsolatedFlightsAllowSameSeatID` | PASS |

## Test matrix snapshot (final_plan.md §9)

| Block | Covered | Missing | Notes |
|-------|---------|---------|-------|
| MVP-A | I-A1–A4, U-A1–A7 | — | Seat repo unit + integration |
| MVP-B | I-B0–B5, U-B0–B7 | — | |
| MVP-C | I-C1–C10, U-C1–C6 | — | |
| MVP-D | I-D1–D10, U-D4–D6 | U-D1, U-D2, U-D3 | 3×3 logic covered by `TestI_D1` + `TestU_C3` |
| MVP-E | Playwright E-E1–E7 in `tests/e2e/` | Not in `go test ./...` gate | Run separately: `npx playwright test` |

## Expert summary (latest cycle)

| Expert | Grade | Top issue |
|--------|-------|-----------|
| Architect | A− | Layers intact; workflow **updates** replace signals; `SwapHold`/`ApplyHold` on domain interface. Plan doc still says "Signal" in §2.4 (doc drift only). |
| Go | A− | Per-flight `flightInventory` mutex; atomic `SwapHold`; context-aware payment delay; no manual RUnlock paths. |
| Temporal | A | Sync `UpdateSubmitPayment` / validators; timer race via in-workflow `handleTimerExpiry` (no signal polling). |
| Database | A− | `SwapHold` rollback tested (`TestU_A7`); `ReconcileHolds` on startup; conflict skips logged (RL-M1). |
| UI | B+ | `payment.js` SSE + poll; new-method route wired; attempt counter uses cumulative `payment_failures` (RL-M2). |
| QA | A | All S-1..S-5 green in Go suite; commit closes prior S-3 gap (3×3). |
| Docs | B+ | README/design_overview match code; `final_plan.md` / `general_review.md` stale (signals, RejectInFlightPayment). |

## Open findings

| ID | Sev | Role | Title | File(s) |
|----|-----|------|-------|---------|
| _none Critical/High_ | | | | |

### Medium / Low (tracked, no fix phase)

| ID | Sev | Role | Title | File(s) |
|----|-----|------|-------|---------|
| RL-M1 | Medium | Database | `ReconcileHolds` skips `ApplyHold` conflicts with warn only — workflow vs memory can diverge until next mutation | `internal/app/reconcile.go` |
| RL-M2 | Medium | UI | Payment page shows `payment_failures` as "attempts on current code"; counter does not reset on new method | `internal/web/static/js/payment.js` |
| RL-L1 | Low | Docs | `final_plan.md` §2.4–2.5 still documents signals + `RejectInFlightPayment` | `docs/final_plan.md` |
| RL-L2 | Low | Docs | `general_review.md` / `final_review.md` describe pre-880ecbf issues | `docs/general_review.md` |
| RL-L3 | Low | QA | Missing unit tests U-D1, U-D2, U-D3 (integration covers behavior) | `internal/workflow/booking/workflow_test.go` |
| RL-L4 | Low | Temporal | `[TMPRL1102]` handler-not-finished warning on fast test teardown | workflow tests |

## Resolved findings (this cycle)

| ID | Resolved | Evidence |
|----|----------|----------|
| EM-H1 | 880ecbf | Payment uses `UpdateWorkflow` (no signal polling) — `order_service.go` |
| EM-C2 | 880ecbf | `ReconcileHolds` + `ApplyHold` on API/worker bootstrap |
| EM-C1 | 880ecbf | `TryHold` refactored to per-flight lock; no manual RUnlock |
| EM-H2 | 880ecbf | `maxAttemptsPerMethod` / `maxPaymentMethods` in `payment.go`; `TestI_D1` |
| EM-H3 | 880ecbf | Payment delay uses `select` on `ctx.Done()` in `activities.go` |
| EM-M1 | 880ecbf | Atomic `SwapSeats` → `SwapHold` (`workflow.go`, `seat_repository.go`) |

---

_Findings use format from `.cursor/skills/review-loop/SKILL.md` (skill file absent in repo; template above)._
