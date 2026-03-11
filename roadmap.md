# Shuttle Roadmap

## Purpose
Separate phased delivery planning from the core PRD so product scope, architecture, and implementation sequencing can evolve independently.

---

# 1. Phase 1: Core MVP

Focus on the minimum product that proves the shell-native workflow.

Included work:
- workspace and shell substrate
- shell observation and injected command tracking
- bottom-pane TUI foundation
- composer and structured transcript
- agent workflow and approval loop

This phase maps to Epics 1 through 4 in [requirements-mvp.md](requirements-mvp.md).

## Exit Criteria
- A user can launch Shuttle into a two-pane workspace.
- The top pane remains a real shell session.
- The user can SSH from the top pane and still have Shuttle act on that session.
- The agent can observe shell context, propose actions, request approval, and act through the top pane.
- The user can complete an end-to-end task loop without leaving the terminal.

---

# 2. Phase 2: Inspection and Persistence

Focus on reducing transcript overload and making sessions resumable.

Included work:
- diff inspection view
- command-output inspection view
- session and task inspection views
- local persistence for transcript and command history
- useful session restoration after restart

This phase maps primarily to Epic 5 in [requirements-mvp.md](requirements-mvp.md).

---

# 3. Phase 3: Configuration and Extensibility

Focus on broadening configuration and preparing the architecture for controlled growth.

Included work:
- provider and model profile management
- project-level and global config hardening
- internal command registry maturity
- extension-ready seams for commands, views, tools, and event subscribers

This phase maps primarily to Epic 6 in [requirements-mvp.md](requirements-mvp.md).

---

# 4. Future Considerations

Not required for the initial delivery sequence, but important longer-term directions:
- richer worktree management UI
- broader extension and plugin loading
- alternate multiplexer support
- deeper project-aware context gathering
- improved prompt and session heuristics
- secure secret storage integration
- richer provider capability negotiation

---

# 5. Planning Notes

- Build the substrate before the polish. Reliable pane control, observation, and approvals matter more than richer views.
- Treat inspection views and persistence as high-value follow-on work if the core shell loop takes longer than expected.
- Avoid committing to a marketplace or broad plugin story in v1; keep the extension architecture internal-first.
