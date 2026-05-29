# Developer Fix Phase

**Persona:** Implementer — resolves review findings with minimal diffs.

## When to run

- User invoked `/review-loop fix`
- User invoked `/grade-a-plus` enhance phase (fix Critical, High, **and Medium** that block A+)
- Or Critical/High findings remain after triage and user did not say "review only"

## Rules

1. Load `/developer` skill — layer rules, **one fix = one commit**, surgical diffs only.
2. Fix **Critical first**, then **High**; ask before Medium/Low unless trivial.
3. **One finding per cycle:** implement minimal change → add/adjust test if needed → `go test ./...` → **commit** → update state → next finding.
4. Do not touch files or lines unrelated to the current finding.
5. Update finding in `docs/review_loop_state.md` after each committed fix.

## Fix order (recommended)

1. Failing tests / broken routes (QA Critical)
2. Requirements violations (Temporal, payment, timer)
3. Data consistency / concurrency (DATA, GO Critical)
4. Architecture leaks (ARCH High)
5. Docs drift (DOC) — after code stable
6. UI polish (UI Medium/Low)

## After fixes

Run `/review-loop verify` or full step 10 from main SKILL.md.
