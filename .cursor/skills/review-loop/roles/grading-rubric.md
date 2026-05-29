# Expert grading rubric

Used by `/grade-a-plus` and `/loop /grade-a-plus`. Every expert assigns one letter grade per cycle.

## Scale

| Grade | Meaning |
|-------|---------|
| **A+** | Exemplary for a Senior+ take-home. Zero Medium-or-higher findings from this role. Would impress an EM in interview debrief. |
| **A** | Strong. At most **Low** nits; no Medium+ findings. |
| **B** | Good MVP. One or more **Medium** gaps; no Critical/High. |
| **C** | Material gaps (**High**) or multiple Medium issues. |
| **D** | Critical correctness, safety, or requirements failure. |
| **F** | Broken tests, failing scenarios, or unusable in role's domain. |

**Grade A+ loop exits only when all seven roles = A+.**

## Role-specific A+ bar

### Architect (A+)

- Strict 3-tier boundaries; no seat writes outside activities
- API matches `final_plan.md` §5; deployment model documented and consistent
- Plan/design docs aligned with implementation (updates vs signals documented accurately)
- State machine documented including `PAYMENT_FAILED` if present in code

### Go expert (A+)

- Idiomatic Go; clear errors; no flaky tests in `go test ./...`
- Concurrency safe for in-memory MVP scope; race risks documented
- Injectable dependencies for testability; no dead code in hot paths

### Temporal expert (A+)

- Timer, payment, seat updates correct for S-1..S-5
- Workflow updates validated; payment 3×3 and `StartNewPaymentMethod` covered by tests
- No ambiguous implicit vs explicit method-switch behavior without docs/tests

### Database expert (A+)

- Repository contracts complete; hold/swap/release consistent
- Idempotent release (or documented intentional behavior with tests)
- Split-deploy limitation documented; reconciliation on startup verified

### UI expert (A+)

- Static UI flows match API; timer and payment counters accurate
- Seat selection PATCH works; terminal states clear
- E-E or integration equivalent covers payment exhaustion path (S-3)

### QA expert (A+)

- S-1..S-5 each have passing automated tests named in state
- Test matrix §9 gaps either filled or explicitly deferred with integration equivalent cited
- No tests asserting removed behavior; suite stable under `-count=1`

### Docs expert (A+)

- README, `final_plan.md`, `design_overview.md`, `final_review.md` match code
- No stale failure lists; env vars and routes documented
- Handoff docs usable without reading source

## Blocking findings by grade

When triaging for `/grade-a-plus`, map findings to max achievable grade:

| Finding severity | Blocks grade above |
|------------------|-------------------|
| Critical | A+ (max D or F) |
| High | A+ (max C) |
| Medium | A+ (max B) |
| Low | A (max A; fix if trivial on path to A+) |

Fix phase priority: lowest role grade first, then Critical → High → Medium within that role.
