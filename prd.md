# PRD: Shuttle - Core Product Definition

## Document Status
Draft v2

## Owner
Joshua

## Purpose
Define the product requirements for a local, shell-native, two-pane agent workspace without mixing detailed implementation and protocol design into the core product document.

## Document Map
Detailed material that used to live in the monolithic PRD is now split into focused supporting docs:
- [MVP Requirements](requirements-mvp.md)
- [Architecture](architecture.md)
- [Shell Observation Protocol](protocol-shell-observation.md)
- [Roadmap](roadmap.md)
- [Implementation Plan](implementation-plan.md)
- [Milestone 1 Plan](milestone-1-workspace.md)
- [Agent Runtime Design](agent-runtime-design.md)
- [Provider Integration Design](provider-integration-design.md)
- [Provider Integration Plan](provider-integration-plan.md)
- [Runtime Management Design](runtime-management-design.md)
- [Shell Execution Strategy](shell-execution-strategy.md)

---

# 1. Overview

## 1.1 Product Summary
Shuttle is a local, shell-native agent workspace built on top of tmux.

It provides:
- a top pane containing a real shell session, including SSH sessions when the user connects remotely
- a bottom pane containing a full-featured TUI app used to interact with an agent, submit shell commands, review plans, approve actions, inspect diffs, and configure provider and model settings

Shuttle does not attempt to build or replace a terminal emulator. It uses tmux as the pane, layout, and control substrate and acts on the exact shell session visible in the top pane.

## 1.2 Product Goal
Match the useful parts of Warp's agent functionality strictly in the shell by enabling a local agent to:
- observe shell output
- propose next steps
- send commands into the active shell session, including through SSH
- capture output and results
- iterate in an agent loop
- present approvals, diffs, and interactive controls in a bottom-pane TUI

## 1.3 Product Non-Goals
Shuttle is not:
- a terminal emulator
- a remote daemon system
- a GUI terminal replacement
- a code editor
- a shell replacement

---

# 2. Problem Statement

Terminal-native AI tools tend to fall into one of two categories:
- single-command chat helpers that do not maintain a real execution loop
- GUI terminal products that tightly couple terminal rendering and AI features

The desired workflow is different:
- the user wants to stay in a real shell
- the user wants the agent to operate on the exact session already in progress
- the user wants that to work through a live SSH session
- the user wants an extensible, keyboard-first TUI control surface in a second pane
- the user does not want to recreate a terminal

There is a gap for a shell-native, pane-based, extensible agent workspace that uses the shell as reality and a TUI as the interaction and control layer.

---

# 3. Product Vision

## 3.1 Core Vision
Shuttle should feel like this:

- Top pane: reality
  The actual shell session where commands run, including remote commands over SSH.

- Bottom pane: intent and control
  A rich TUI where the user can:
  - talk to the agent
  - type shell commands to send upward
  - review plans and actions
  - approve or refine proposed changes
  - inspect diffs and outputs
  - configure model and provider settings
  - trigger keyboard-driven app commands and popups

## 3.2 Experience Goals
Shuttle should feel:
- shell-native
- fast
- trustworthy
- keyboard-first
- extensible
- robust under SSH-based workflows
- powerful without becoming a terminal emulator

---

# 4. Users

## 4.1 Primary User
A technical user who:
- works heavily in shell sessions
- often uses SSH into remote systems
- wants agentic assistance without leaving the terminal
- prefers keyboard-first workflows
- wants visibility and control over what the agent is doing
- wants an extensible platform, not just a prompt box

## 4.2 Typical Scenarios
- debugging a failing command or test in a local or remote repo
- inspecting and editing code over SSH
- running iterative test and fix loops
- asking the agent to inspect the error above and determine next commands
- reviewing diffs before applying changes
- switching providers or models depending on the task

---

# 5. Product Principles

1. Do not reimplement the terminal.
2. Operate on the exact shell session visible to the user.
3. Treat SSH as a first-class workflow.
4. Make the bottom pane a full product, not just an input box.
5. Keep the user in control through approvals and visibility.
6. Design extensibility into commands, views, and tools from day one.
7. Use tmux as infrastructure, not as the product.
8. Default to safety and explicit approvals.

---

# 6. Goals and Non-Goals

## 6.1 Goals
Shuttle v1 must:
- create and manage a two-pane tmux workspace
- use the top pane as a real shell pane
- provide a full-fledged TUI app in the bottom pane
- support Agent mode and Shell mode, with command-level app actions
- observe shell output from the top pane
- inject commands into the top pane
- support agent loops that reason locally and act through the top pane
- support SSH sessions running in the top pane
- support approvals such as Yes, No, and Refine
- support inspection views for diffs, outputs, settings, help, and session state
- support configurable keybindings
- support configurable provider and model profiles
- support multiline input composition
- persist useful local session and task state

## 6.2 v1 Non-Goals
Shuttle v1 will not:
- replace tmux
- support non-tmux backends
- provide remote daemon autonomy
- embed a file editor
- deeply understand fullscreen TUIs in the top pane
- become a full IDE
- implement a plugin marketplace

---

# 7. Constraints and Risks

## 7.1 Constraints
- the product must not require terminal rendering or emulator behavior
- the product must act through the real shell session in the top pane
- the product must not assume remote daemon installation
- the product must remain usable in standard terminal environments

## 7.2 Key Risks
- alternate-screen applications such as `vim`, `less`, `top`, and `fzf` may complicate observation or automation
- generic prompt detection is unreliable, so controller-driven command tracking must lean on sentinels
- aggressive auto-execution may reduce trust; confirm-by-default should remain the baseline
- dumping too much raw shell output into the transcript will reduce usability
- power-user keybinding conflicts are likely, so configurability matters

---

# 8. MVP Definition

The first usable release should focus on:
- tmux two-pane workspace setup
- a real top shell pane and a bottom TUI pane
- shell observation and shell command injection
- sentinel-based tracking for injected commands
- an agent loop with confirm-by-default execution
- a structured transcript with approval cards
- minimal diff and output inspection views
- basic provider and model profile support
- local persistence for useful session context

Detailed acceptance criteria and delivery grouping live in [requirements-mvp.md](requirements-mvp.md).

---

# 9. Success Criteria

Shuttle v1 will be successful if a user can:

1. Launch a two-pane workspace.
2. SSH into a remote machine in the top pane.
3. Ask the bottom-pane agent to inspect an error or task.
4. See the agent propose a command or plan.
5. Approve the action.
6. Watch the command run in the exact remote shell session already open above.
7. Have Shuttle capture the output and continue the agent loop.
8. Review diffs, outputs, and settings from within the bottom-pane TUI.
9. Customize keybindings and provider settings without changing the product architecture.
10. Feel that the system is extensible rather than hardcoded.

---

# 10. Open Questions

1. What exact default keybindings should ship in v1?
2. Should Command mode exist as a full mode, or primarily as a command palette overlay?
3. How much project-level config should be supported in v1?
4. Should worktree views in v1 be read-only or support limited actions?
5. What provider profiles need first-class support out of the box?

---

# 11. Summary

Shuttle is a shell-native, tmux-backed, two-pane agent workspace.

Its defining characteristics are:
- the top pane is a real shell, including live SSH sessions
- the bottom pane is a rich, keyboard-first TUI
- the agent acts by observing and injecting into the actual shell session
- the user remains in control through structured approvals and visibility
- the architecture is designed to expand without becoming a terminal emulator
