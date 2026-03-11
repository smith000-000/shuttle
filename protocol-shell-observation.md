# Shuttle Shell Observation and Sentinel Protocol

## Purpose
Define how Shuttle observes the top pane and robustly tracks controller-injected command lifecycles.

---

# 1. Scope

This protocol covers:
- near-real-time observation of top-pane shell output
- rolling context capture for agent reasoning
- lifecycle tracking for controller-injected commands
- start and end boundary detection
- exit status capture

This protocol does not attempt to solve general-purpose prompt detection for every shell interaction. It is primarily concerned with reliable tracking of commands injected by Shuttle.

---

# 2. Protocol Requirements

- Every controller-injected command shall carry a unique command ID.
- The system shall emit or inject a begin marker before the command.
- The system shall emit or inject an end marker after the command.
- The end marker shall include the command exit code.
- The parser shall associate relevant output with the correct command ID.
- The system shall maintain a rolling buffer of recent shell output.
- The system shall support on-demand capture of recent top-pane content for context gathering.

---

# 3. Command Lifecycle Model

1. The controller allocates a unique command ID.
2. The controller injects a begin marker into the active top-pane shell context.
3. The controller injects the requested command into that same shell context.
4. The controller injects an end marker that includes the command ID and exit status.
5. The observer and parser associate the output between those boundaries with the command record.
6. The controller emits a structured completion event for transcript, approvals, and persistence.

The key rule is that the markers must execute in the same shell context as the command itself. That is what allows the mechanism to survive SSH boundaries.

---

# 4. Reliability Expectations

- The protocol should work wherever the active shell currently is, including remote SSH hosts reached from the top pane.
- The protocol should not depend on prompt detection to identify injected command completion.
- The parser must tolerate noisy shell output.
- The system should degrade gracefully when alternate-screen apps or shell-specific quirks interfere with perfect observation.

Prompt detection may still be useful as a supplemental heuristic, but it should not be the authoritative source for controller-driven command boundaries.

---

# 5. Implementation Notes

- Marker format should be explicit, machine-parseable, and unlikely to collide with ordinary shell output.
- Marker visibility should be acceptable in the normal shell transcript, or the system should provide a clear strategy for hiding or de-emphasizing marker noise in the TUI.
- Observation should feed both a rolling context buffer and structured command result records.
- Parser output should be normalized into events the TUI can render without needing shell-specific logic.

---

# 6. Known Edge Cases

- Fullscreen or alternate-screen applications such as `vim`, `less`, `top`, and `fzf` may break simple capture assumptions.
- Users may interleave manual commands with controller-injected commands.
- Shell startup scripts or prompt decorations may introduce noise around command boundaries.

These cases should be treated as degraded modes, not as reasons to fall back to terminal emulation.

---

# 7. Related Documents

- Product acceptance criteria: [requirements-mvp.md](requirements-mvp.md)
- System boundaries and implementation direction: [architecture.md](architecture.md)
