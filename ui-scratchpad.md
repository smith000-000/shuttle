# UI Scratchpad

Temporary place to capture Shuttle UX and UI notes during testing.

## How To Use
- Add rough notes freely.
- Keep this focused on UX, layout, controls, wording, and visual behavior.
- Promote stable decisions into `requirements-mvp.md` or `implementation-plan.md` later.

## Suggested Format

### Issue
- What happened?
- Where in the UI did it happen?
- Why was it confusing or slow?

### Desired Change
- What should happen instead?

### Notes
- Screenshots
- Key sequence
- Repro steps
- Open questions

---

## Backlog

### 2026-03-13
- Transcript viewport bug: when the transcript is already full, a new user prompt may not become visible immediately until the next controller event arrives.
- Patch proposals are inert until Shuttle can actually apply them; the agent should not imply proposed files already exist.
