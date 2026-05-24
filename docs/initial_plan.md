# Technical Plan: Flight Booking System (Temporal)

**Architect:** Senior System Architect  
**Status:** SUPERSEDED — see **[plan.md](../plan.md)** for the canonical architecture  
**Principles:** S.O.L.I.D, 3-Tier Layering, Temporal Orchestration

> This document was the initial proposal. The full plan now includes: user flow, REST API design, project file structure, seat-map UI rules, workflow signals/queries, timer pattern, and interview-scoped phases.

---

## Quick reference

| Topic | Location in [plan.md](../plan.md) |
|-------|-----------------------------------|
| User flow & state machine | §2 |
| 3-tier model & repos | §3 |
| Project file structure | §4 |
| BookingWorkflow (signals, timer) | §5 |
| REST API (`GET /flights/{id}/seats`, orders) | §6 |
| Phase 1 / 2 / 3 | §7 |
| Implementation order | §10 |

## Original 3-tier summary (unchanged intent)

| Tier | Responsibility | Tech |
|------|----------------|------|
| **Presentation** | REST API, DTOs, Temporal Client | Go (Gin) |
| **Service** | Temporal Workflow + Activities | Go (Temporal SDK) |
| **Data** | Seat/Flight inventory via repository pattern | In-memory → Postgres |

## Key refinements since this draft

1. Seat map via `GET /api/v1/flights/{flight_id}/seats` (reads `SeatRepository`, not Temporal)
2. Full order lifecycle signals: `UpdateSeats`, `SubmitPayment`, `CancelOrder`; query `GetStatus`
3. Cancellable 15m timer with selector loop; timer never pauses during payment
4. Separate `cmd/api` and `cmd/worker` binaries
5. Grayscale UI for HELD (others) and BOOKED; highlight user's own holds via `?order_id=`

---

*For locked functional requirements see [final_requierments.md](final_requierments.md).*
