# Reviewed Requirements: Flight Booking System (Temporal)

**Status:** LOCKED
**Last updated:** 2026-05-24
**Confidence:** 98.75%

---

## 1. Executive Summary
A multi-flight seat reservation and payment system orchestrated by **Temporal**. Users select a flight, hold seats (15-minute continuous timer), and pay via a 5-digit code. The system handles simulated payment failures (15%), allows up to 3 different payment methods, and ensures seat inventory integrity across multiple flights.

## 2. Functional Requirements

### 2.1 Seat Management & Timer
- **Multi-Flight:** Inventory is isolated per `flight_id`. Seat `1A` on Flight A is distinct from `1A` on Flight B.
- **Hold Limit:** A user can hold up to the total capacity of the plane in a single order.
- **Continuous Timer:** The 15-minute hold timer **never pauses**. It starts upon the first seat selection and **refreshes to a full 15 minutes** on every subsequent selection change.
- **Expiry:** If the timer hits zero, all held seats are released immediately, even if a payment validation is currently in progress.

### 2.2 Payment Logic
- **Code Format:** Exactly 5 digits.
- **Validation:** 10-second timeout per attempt; 15% simulated failure rate.
- **Attempts & Methods:**
    - **3 attempts per method:** A user can try the same 5-digit code up to 3 times.
    - **3 methods per order:** A user can try up to 3 *different* 5-digit codes. Changing the code resets the attempt counter but consumes one of the 3 allowed method slots.
- **Success:** Seats transition from `HELD` to `BOOKED`.
- **Failure:** If all 3 methods (9 total attempts max) fail, or if the timer expires, the order is terminated.

### 2.3 Edge Cases
- **Flight Departure:** If a flight departs while an order is active, the order is allowed to continue until the 15-minute timer expires or payment succeeds.
- **Race Condition:** If the timer expires while a payment activity is running, the payment must be rejected/refunded (simulated) and the seats released.

## 3. Technical Constraints & NFRs
- **Orchestration:** A single Temporal workflow must own the entire order lifecycle (timer, state transitions, payment retries).
- **Consistency:** Strong consistency is required for seat holds to prevent double-booking on the same flight.
- **Stack:** Go (API & Workers), Temporal, Web Frontend.

## 4. State Machine (Order)
1.  `CREATED`: Initial state.
2.  `SEATS_HELD`: Timer running (15 min).
3.  `AWAITING_PAYMENT`: Payment validation active; **Timer still running**.
4.  `CONFIRMED`: Terminal success.
5.  `EXPIRED`: Terminal failure (timer hit zero).
6.  `CANCELLED`: Terminal failure (user abandoned).

## 5. Scenarios

| ID | Scenario | Expected Result |
|----|----------|-----------------|
| S-1 | Happy Path | Select flight -> Select seats -> Pay -> Success. |
| S-2 | Timer Refresh | Select seats (8m left) -> Add seat -> Timer resets to 15m. |
| S-3 | Method Exhaustion | Fail code A 3 times -> Fail code B 3 times -> Fail code C 3 times -> Order fails. |
| S-4 | Late Payment | Payment starts at 14:55 -> Timer hits 15:00 at 15:05 -> Order expires; payment rejected. |
| S-5 | Multi-Flight | Hold `1A` on Flight 101; `1A` on Flight 102 remains available. |

---
*Requirements Locked per User Instruction.*
