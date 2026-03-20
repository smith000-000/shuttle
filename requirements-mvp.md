# Shuttle MVP Requirements

## Purpose
This document groups the product requirements into delivery-oriented epics. The original FR IDs are preserved for traceability, but the requirements are organized by implementation sequence instead of as one long flat list.

## Priority Levels
- `P0`: required for the first usable release
- `P1`: valuable and planned, but safe to defer if the substrate work takes longer than expected

---

# Epic 1. Workspace and Shell Substrate

## Priority
`P0`

## Outcome
Shuttle creates or attaches to a tmux-managed two-pane workspace and treats the top pane as the source of truth for shell execution.

## Requirements
- `FR-1`: The system shall create or attach to a tmux workspace containing two panes: top pane for the real shell and bottom pane for the Shuttle TUI.
- `FR-2`: The system shall store or be able to discover the pane IDs for the top and bottom panes.
- `FR-3`: The system shall title or otherwise identify the panes for debugging and control purposes.
- `FR-4`: The default layout shall allocate approximately 70 percent height to the top pane and 30 percent height to the bottom pane.
- `FR-5`: The top pane shall run a normal shell session.
- `FR-6`: The user shall be able to SSH from the top pane into a remote machine and continue working normally.
- `FR-7`: The controller shall be able to inject commands into the exact shell session in the top pane.
- `FR-8`: Commands injected into the top pane shall run wherever that shell currently is, including on a remote SSH host.

## Acceptance Criteria
- Launching Shuttle creates or attaches to a predictable two-pane tmux workspace.
- The controller can reliably discover and reuse pane IDs after startup.
- The user can manually work in the top pane as if Shuttle were not present.
- If the user SSHs from the top pane into a remote system, injected commands execute in that remote shell context.

---

# Epic 2. Shell Observation and Command Tracking

## Priority
`P0`

## Outcome
Shuttle can observe top-pane output in near real time and reliably associate controller-driven commands with their resulting output and exit status.

## Requirements
- `FR-9`: The system shall observe output from the top pane in near real time.
- `FR-10`: The system shall maintain a rolling buffer of recent shell output.
- `FR-11`: The system shall support on-demand capture of recent top-pane content for context gathering.
- `FR-12`: The system shall support a robust command lifecycle tracking mechanism for controller-injected commands.
- `FR-13`: The system shall identify start and end boundaries for injected commands using a sentinel-based protocol.
- `FR-14`: The system shall capture the exit status of controller-injected commands.

Protocol details are specified in [protocol-shell-observation.md](protocol-shell-observation.md).

## Acceptance Criteria
- Recent shell output is available for context gathering without blocking normal shell usage.
- The controller maintains a rolling context window and can request on-demand snapshots.
- Every injected command has a unique lifecycle record with a start marker, end marker, and exit status.
- Output associated with a controller-driven command is attached to the correct command record even in noisy shell sessions.

---

# Epic 3. TUI Core, Composer, and Transcript

## Priority
`P0`

## Outcome
The bottom pane is a full-featured, keyboard-first TUI for interaction, review, and task flow management.

## Requirements
- `FR-15`: The bottom pane shall host a full-featured TUI application.
- `FR-16`: The TUI shall include a header or status area, a transcript view, a composer or input area, a footer or key hint area, and popup or modal capability.
- `FR-17`: The TUI shall support at least three conceptual interaction layers: Agent, Shell, and Command.
- `FR-18`: The TUI shall clearly indicate the current mode.
- `FR-19`: The TUI shall be fully usable by keyboard.
- `FR-20`: The TUI shall support configurable keybindings, including future Ctrl-based actions.
- `FR-21`: The composer shall support single-line and multiline input.
- `FR-22`: The composer shall intelligently expand in height as content grows, up to a configurable maximum.
- `FR-23`: The composer shall preserve formatting for pasted multiline content.
- `FR-24`: The composer shall maintain separate history for Agent and Shell inputs.
- `FR-25`: The composer shall support submit, explicit newline insertion, history navigation, and line clearing or editing shortcuts.
- `FR-25a`: The composer should support Up and Down arrow history cycling for prior inputs within the active interaction mode.
- `FR-25b`: Composer history shall be application-owned and separate from the shell's own interactive command history.
- `FR-41`: The transcript shall display structured events rather than raw shell dump by default.
- `FR-42`: Transcript entries shall support at least user messages, agent messages, system notices, shell command entries, shell result summaries, approval cards, plan summaries, and error cards.
- `FR-43`: The transcript shall be scrollable.
- `FR-44`: Transcript entries shall allow drill-down via popups where appropriate.

## UX and Keybinding Expectations
- The primary interaction flow should remain coherent when the user switches between talking to the agent, sending shell commands, and triggering app-level actions.
- The experience should be keyboard-first.
- The product should ship with sensible defaults for toggling modes, opening a command palette, approving or rejecting actions, opening keymap help, navigating the transcript, inserting newlines, clearing the composer, and cancelling tasks.
- Users must be able to override default keybindings.

## Acceptance Criteria
- The bottom pane renders a stable TUI with visible status, transcript, composer, and key hints.
- The current interaction mode is always obvious.
- A user can complete normal flows without leaving the keyboard.
- The composer supports multiline entry, pastes, separate mode history, and common editing actions.
- In Shell mode and Agent mode, Up and Down arrows cycle through prior entries for that mode without mixing the two histories together.
- The transcript renders structured event cards and supports drill-down where more detail is needed.

---

# Epic 4. Agent Workflow and Approvals

## Priority
`P0`

## Outcome
The user can ask the agent to reason over recent shell state, propose actions, and act safely through the top pane.

## Requirements
- `FR-26`: In Agent mode, the user shall be able to submit natural-language instructions.
- `FR-27`: The controller shall gather context from recent observed shell output, current host or session context, prior task state, and relevant execution results.
- `FR-28`: The agent shall be able to produce a direct answer, a proposed shell command, a proposed multi-step plan, a proposed patch or diff, or an approval request.
- `FR-28c`: When the shell is waiting for input or a fullscreen terminal app is active, the agent may propose a short raw terminal input sequence as a first-class action rather than only narrating what the user should press.
- `FR-28a`: A proposed patch or diff shall remain proposal-only until the user explicitly applies it through a product-owned patch-application flow.
- `FR-28b`: Until a proposed patch is actually applied, the agent and UI shall not claim that files were created, modified, or available for execution.
- `FR-29`: The agent shall support iterative loops: observe, reason, propose or act, capture result, and continue until complete or cancelled.
- `FR-30`: In Shell mode, input from the bottom pane shall be treated as a shell command.
- `FR-31`: Shell commands entered in the bottom pane shall be injected into the top pane.
- `FR-32`: Shell mode shall not execute commands in a separate subprocess environment; it shall target the top pane session.
- `FR-32a`: Commands submitted from the bottom pane should avoid polluting the user's normal interactive shell history by default where the active shell environment supports it.
- `FR-32b`: If history-isolated execution cannot be guaranteed for the active shell or remote session, the system should expose that limitation clearly rather than silently assuming it is solved.
- `FR-33`: The system shall support approval prompts as first-class UI elements.
- `FR-34`: Approval prompts shall support at minimum Yes, No, and Refine.
- `FR-35`: The system shall require approval by default before file-modifying commands, patch application, potentially destructive operations, and multi-step execution plans unless the user has enabled automatic execution.
- `FR-36`: When the user selects Refine, the system shall return focus to an agent input flow seeded with the proposed action context.

## Execution Policy Requirements
- The system shall support at minimum `suggest`, `confirm`, and `auto` policy modes.
- The default policy shall be `confirm`.
- Commands that modify files, change git state, or may be destructive should require approval unless policy explicitly permits otherwise.

## Acceptance Criteria
- A user can submit a natural-language instruction and receive a structured answer, plan, or proposed action.
- The controller includes recent shell context in the agent loop without pretending to own the shell.
- Shell mode sends commands to the top pane rather than to an isolated subprocess.
- Shell submissions use Shuttle-managed composer history independent of the shell's own history list.
- Approval cards support Yes, No, and Refine, with Refine returning to a pre-seeded input flow.
- When the shell is awaiting input or a fullscreen app is active, Shuttle can surface a first-class proposed terminal-input action instead of forcing the user to infer the needed keystrokes from prose alone.
- The task loop can continue until the task is complete or the user cancels it.
- The system does not treat patch proposals as real workspace state until they are explicitly applied, and follow-up commands cannot assume proposed files already exist.

---

# Epic 5. Inspection Views and Persistence

## Priority
`P1`

## Outcome
The user can inspect detailed results without polluting the main transcript and can recover useful local state across restarts.

## Requirements
- `FR-37`: The TUI shall support popups or modal overlays.
- `FR-38`: v1 shall support popups for diff viewing, command output viewing, model and provider settings, keymap help, session or task inspection, and worktree-related inspection or selection.
- `FR-39`: The diff popup shall support scrolling and clear presentation of a unified diff.
- `FR-40`: The output popup shall support viewing the full output associated with a captured command.
- `FR-54`: The system shall persist local session and task state.
- `FR-55`: The system shall persist transcript history and command results sufficient for session or task inspection.
- `FR-56`: The system shall support restoring useful session context across restarts where practical.

## Acceptance Criteria
- The user can open a diff view and inspect a unified diff without leaving the TUI.
- The user can open a full command-output view when a summary is insufficient.
- Session state, task state, transcript history, and command results are persisted locally.
- Restarting Shuttle restores useful context when a previous session can be resumed safely.

---

# Epic 6. Configuration and Extensibility

## Priority
`P1`

## Outcome
Shuttle supports provider configuration and keymap customization while leaving room for future extension without prematurely building a plugin marketplace.

## Requirements
- `FR-45`: The system shall support configurable provider and model profiles.
- `FR-46`: The user shall be able to inspect and modify provider settings through the TUI.
- `FR-47`: Provider configuration shall support at minimum provider type, model name, base URL, authentication reference, and profile name.
- `FR-48`: The system shall support multiple named provider profiles and allow switching among them.
- `FR-49`: The system should prefer referencing secrets via environment variables or secure storage rather than storing raw secrets in project config.
- `FR-50`: The application shall define an internal command registry.
- `FR-51`: Commands shall be bindable to keys independently of implementation logic.
- `FR-52`: The system shall support adding new Ctrl-based actions through configuration and internal command registration.
- `FR-53`: The application architecture shall support future plugin or extension capabilities for commands, popups and views, settings sections, agent tools, and event subscribers.

## Configuration Requirements
- The system shall support a global config file for UI settings, keybindings, provider profiles, and execution policy defaults.
- The system should support project-level overrides for relevant settings.
- The system should prefer environment variable references or secure secret handling rather than raw credential storage in project config.

## Acceptance Criteria
- A user can inspect, edit, and switch provider profiles from the TUI.
- The system supports named profiles without hardcoding one provider path.
- Keybinding definitions are decoupled from command implementation.
- The internal command registry exists early enough to avoid UI-specific action sprawl.
- The architecture leaves room for future extension points without requiring a plugin marketplace in v1.

---

# Cross-Cutting Requirements

## Non-Functional Requirements
- Performance: The TUI must feel responsive during normal interaction. Observation and command event handling should feel near real time.
- Reliability: Injected command boundaries must be reliable. The system must tolerate noisy shell output and degrade gracefully if prompt detection is imperfect.
- Safety: The default execution policy should be confirm-before-act. Destructive commands must not run silently by default. The user must be able to cancel active tasks.
- Portability: v1 must support common local environments using bash and zsh. It should work with standard tmux-based workflows and should not depend on a specific GUI terminal emulator.
- Maintainability: Core systems should be modular across tmux integration, controller, TUI, provider layer, config, persistence, and command registry. Internal event and command interfaces should be defined clearly early.

## Target Interaction Flow
1. The user launches Shuttle.
2. The top pane shows a real shell.
3. The user SSHs into a remote box in the top pane if needed.
4. The user encounters an error or wants help.
5. In the bottom pane, the user enters an agent instruction.
6. The agent observes recent shell state and proposes a plan or command.
7. The user approves or refines the proposal.
8. Shuttle injects the command into the top pane.
9. Output is observed and returned into the local reasoning loop.
10. The agent summarizes the result and continues until the task is complete.
