# Agent instructions — paste into Cursor Automations

**Do not** use a short reference like `Use /review-loop + steps from agent-instructions.md` in the Automations UI — the cloud agent may not open this file. **Paste the full block below** into Agent instructions.

Replace `/multi-model-review` with the block below (Neon uses the project skill `/review-loop`, not multi-model-review).

---

```
/review-loop

You are the Neon post-push review automation.

## Context
- Repo: YanShlain/Neon
- Branch: dev (fires after you push to dev)
- Read AGENTS.md and .cursor/skills/review-loop/SKILL.md first

## Steps
1. Orient — git log -3 --oneline; diff since docs/review_loop_state.md last_reviewed_commit; go test ./... -count=1 -timeout 120s
2. Run all 7 expert passes (architect → go → temporal → database → ui → qa → docs)
3. Update docs/review_loop_state.md (findings, S-1..S-5 coverage, test baseline)
4. Triage — dedupe; list Critical/High open items
5. Fix phase — ONLY if Critical/High exist: /review-loop fix with /developer rules:
   - one finding at a time
   - minimal surgical diff (no unrelated edits)
   - go test ./... then one git commit per fix
6. Report — Review loop summary: READY or NOT READY; open Critical/High; next actions

## Do not
- Batch multiple fixes in one commit
- Refactor or reformat unrelated code
- Commit unless fixing Critical/High (state file updates are OK without commit if review-only)
```

## Optional: delivery loop (fix + subagent review until READY)

```
/deliver-ready
```

Or self-paced until gates pass:

```
/loop /deliver-ready
```

Same repo context as above; reads `docs/final_review.md` first, fixes Critical/High, then runs seven dedicated read-only expert subagents. See `.cursor/skills/review-loop/SKILL.md` **Deliver-ready loop**.

## Optional: grade A+ loop (enhance until all experts A+)

```
/grade-a-plus
```

Or self-paced:

```
/loop /grade-a-plus
```

Runs after delivery gates pass. Fixes **Medium+** findings blocking A+ per role. Grading rubric: `.cursor/skills/review-loop/roles/grading-rubric.md`. See **Grade A+ loop** in review-loop SKILL.md.

## Optional: review-only (no auto-fix)

Use this variant if you only want a report after each push:

```
/review-loop

Run full review cycle. Update docs/review_loop_state.md. Report READY/NOT READY.
Do not fix code or commit unless the push message says "autofix".
```

## Tools (optional)

| Tool | When |
|------|------|
| **Memories** | Cross-run context (optional) |
| **MCP server** | Only if you registered Playwright/Browserless — see [README.md Browser section](../automations/README.md#browser--ui-e2e-in-automations) |
| **None extra** | Cloud agent can use built-in VM browser — add UI steps to the prompt (see below) |

For **Test loop** (cloud): you do **not** need Browser MCP if the prompt tells the UI expert to start `go run ./cmd/api` and open `http://localhost:8080` in the cloud VM browser.

Skills load from `.cursor/skills/review-loop/` automatically — no extra MCP required for code review.
