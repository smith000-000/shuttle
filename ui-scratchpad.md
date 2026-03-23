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
- [done] Transcript viewport bug: when the transcript is already full, a new user prompt may not become visible immediately until the next controller event arrives.
- Patch proposals are inert until Shuttle can actually apply them; the agent should not imply proposed files already exist.

### 2026-03-22
- [done] The labeling of the transcript is bulky/noisy. We should have a mode that uses emoji to indicate what the segments are. Pick appropriate emoji and a color coding of the text. No more SYSTEM (purple), AGENT (orange), SHELL (bright grey), RESULT (green) in the default mode. If we're in a terminal that doesn't support the emojis in its character set, let's faill back to this method. Keep the > indicator
- [done] Include mouse support for clicking on and expanding the transcript similar to the Ctrl-o behavior. This should expand inline inside the transcript for the first several lines (30% of our current terminal height?) esc or click elsewhere should collapse this back to its default state. Keep the ctrl-o option to open a longer transcript. ctrl-o should still work as it does now.
- [done] Have clickable areas for proposed shell commands (Yes/No/Refine) in all of our approval contexts
- [done] Introduce initial slash commands: `/model` opens the current provider model picker, `/provider` opens the provider picker, `/quit` quits, and invalid slash commands are handled gracefully.
- Slash command suggestions like Codex does are still pending.
- [done] Return plain `Tab` to the composer and move the mode toggle/dial to `Ctrl-]`.
- [done] some sort of tab completion support. First pass is ghost-text completion with inline cycling and right-arrow accept.
- some sort of command suggestion? Not sure how warp does this but it's shitty. zsh has some command suggestion built in. maybe we can detect the zsh shell and piggy-back and just leave standard tab completions for bash and sh
- resume functionality... /resume to pick up previous session. if we're tracking session content maybe we can have a session picker? I'm not sure if this is a feature yet
- UI themeing support
- [done] warning when running as root #
