# Neon — Agent Guide

This project uses **Cursor Agent Skills** for implementation and quality gates.

## Quick commands

| Invoke | Purpose |
|--------|---------|
| `/grade-a-plus` | Enhance until all 7 expert grades are A+; one cycle |
| `/loop /grade-a-plus` | Repeat until every expert grade is A+ |
| `/deliver-ready` | Fix issues from `docs/final_review.md`, then 7 subagent experts; one delivery cycle |
| `/loop /deliver-ready` | Repeat until READY (self-paced) |
| `/review-loop` | Full multi-expert review cycle |
| `/review-loop fix` | Fix open Critical/High findings |
| `/review-loop verify` | Re-run tests and update coverage state |
| `/developer` | MVP implementation with layer rules and test gates |
| `/senior-system-architect` | Architecture / plan.md only |
| `/loop 30m /review-loop` | Repeat review every 30 minutes |
| `/loop /review-loop` | Self-paced loop until gates pass |

## Review loop experts

Sequential passes (see `.cursor/skills/review-loop/SKILL.md`):

1. **Architect** — 3-tier boundaries, API vs plan
2. **Go expert** — idioms, concurrency, errors
3. **Temporal expert** — timer, payment, workflow updates
4. **Database expert** — repository contracts, hold consistency
5. **UI expert** — static web flows and E-E scenarios
6. **QA expert** — S-1..S-5 traceability, test matrix §9
7. **Docs expert** — README and design doc drift

State persists in [docs/review_loop_state.md](docs/review_loop_state.md).

## Source of truth

- Requirements: [docs/final_requierments.md](docs/final_requierments.md)
- Architecture & test matrix: [docs/final_plan.md](docs/final_plan.md)
- Overview: [README.md](README.md)

## Delivery gates

Review loop exits **READY** when:

- `go test ./...` passes
- No Critical/High open findings
- S-1..S-5 each have passing automated tests
- README matches implementation

**Grade A+ loop** exits when all seven experts in [docs/review_loop_state.md](docs/review_loop_state.md) score **A+** (see [grading rubric](.cursor/skills/review-loop/roles/grading-rubric.md)).

## Typical workflow

```
1. Implement feature     → /developer
2. Delivery loop         → /loop /deliver-ready   (fix final_review gaps → subagent review → repeat)
3. Excellence loop       → /loop /grade-a-plus    (enhance until all expert grades A+)
4. Or manual review      → /review-loop → /review-loop fix → /review-loop verify
5. (Optional) CI watch   → /loop 15m /review-loop verify
```

## Cursor Automations (on commit / push)

Cursor Cloud Automations trigger on **git push** (not local `git commit`). Prefill is in [`.cursor/automations/review-on-push.prefill.json`](.cursor/automations/review-on-push.prefill.json).

**Install (Cloud — push to `main`):**

1. In Cursor chat, ask the agent: *Open Automations with the Neon review prefill*
   — or run MCP `open_automation` with `prefillWorkflowData` from that JSON file.
2. Connect repo `YanShlain/Neon`, confirm branch `main`, save automation.
3. Adjust branch in the JSON if you use a different default.

**Local commit hook (optional):** see [`.cursor/automations/review-on-local-commit.hook.example.sh`](.cursor/automations/review-on-local-commit.hook.example.sh) — runs verify after each local commit via Cursor CLI.

| Trigger | Mechanism | Prefill file |
|---------|-----------|--------------|
| Push to `main` | Cursor Cloud Automation | `review-on-push.prefill.json` |
| Local `git commit` | `post-commit` hook + Cursor CLI | `review-on-local-commit.hook.example.sh` |
