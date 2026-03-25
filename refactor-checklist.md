# Refactor Checklist

## Purpose
Break up the controller and TUI monoliths into smaller files without changing behavior, while making the test layout easier to navigate and maintain.

## Scope
- split `internal/controller/controller.go` by responsibility
- split `internal/tui/model.go` by responsibility
- move shared test doubles/helpers out of `internal/controller/controller_test.go` and `internal/tui/model_test.go`
- reorganize tests so each file maps to one behavior area instead of one monolithic source file

## Goals
- reduce `internal/controller/controller.go` and `internal/tui/model.go` to coordination entry points instead of dumping grounds
- preserve package boundaries; do not create new packages in the first pass
- preserve behavior during file moves
- make each test file understandable without scrolling through unrelated scenarios

## Guardrails
- no product behavior changes during the split
- no opportunistic logic rewrites mixed into file moves
- keep exported APIs stable unless a narrow change is required to complete the refactor
- move tests with the code slice that just moved
- run targeted tests after each slice rather than waiting until the end

## Controller Checklist
### Phase 1: Extract shared helpers
- move package-private helpers from `internal/controller/controller.go` into grouped files before moving large flows
- separate helpers into:
  - plan helpers
  - execution registry/state helpers
  - tracked-shell/session helpers
  - prompt/response normalization helpers

### Phase 2: Split orchestration paths
- create `internal/controller/controller_agent.go`
- move:
  - `SubmitAgentPrompt`
  - `SubmitRefinement`
  - `SubmitProposalRefinement`
  - `ContinueActivePlan`
  - `ContinueAfterCommand`
  - `submitAgentTurn`
  - prompt-building and response-normalization helpers used by agent turns

- create `internal/controller/controller_execution.go`
- move:
  - `SubmitShellCommand`
  - `SubmitProposedShellCommand`
  - `submitShellCommand`
  - command completion/lost/canceled/error mapping
  - owned-execution target selection

- create `internal/controller/controller_monitor.go`
- move:
  - `runTrackedCommand`
  - monitor snapshot reduction
  - foreground attach helpers
  - monitor-state to controller-state mapping

- create `internal/controller/controller_shell.go`
- move:
  - shell context refresh
  - shell tail capture
  - tracked-shell target sync
  - recovery snapshot capture

- create `internal/controller/controller_state.go`
- move:
  - execution registry bookkeeping
  - cleanup registration/cancel helpers
  - event creation/appending helpers
  - clone/sort/transition helpers

- create `internal/controller/controller_plan.go`
- move:
  - active plan construction
  - plan advancement/completion helpers
  - plan text normalization helpers

### Phase 3: Simplify file ownership
- keep `internal/controller/types.go` as the domain model file
- leave only constructor/type wiring in the main controller entry file if possible

## TUI Checklist
### Phase 1: Extract dispatch seams
- reduce `Model.Update` so it dispatches to focused handlers instead of owning all state changes inline
- introduce package-private handlers before moving code:
  - controller-event handler
  - take-control handler
  - provider/onboarding/settings message handler
  - key-routing handler

### Phase 2: Split by state domain
- create `internal/tui/model_core.go`
- keep:
  - `Model`
  - constructors
  - small shared state helpers

- create `internal/tui/update_root.go`
- move:
  - `Update`
  - top-level Bubble Tea message dispatch only

- create `internal/tui/update_controller.go`
- move:
  - controller event handling
  - active-execution polling results
  - shell tail refresh handling
  - busy/check-in/provider switch result handlers

- create `internal/tui/update_keys.go`
- move:
  - general key routing
  - mode toggles
  - interrupt/fullscreen-entry behavior
  - primary action routing

- create `internal/tui/composer.go`
- move:
  - cursor movement
  - input editing
  - paste sanitization
  - completion logic
  - history access helpers

- create `internal/tui/transcript.go`
- move:
  - transcript line generation
  - selection/detail helpers
  - scroll math
  - transcript event to entry conversion helpers

- create `internal/tui/render_main.go`
- move:
  - `View`
  - header/status/footer/composer/shell-tail rendering
  - main screen layout helpers

- create `internal/tui/render_cards.go`
- move:
  - action cards
  - active plan card
  - active execution card

- create `internal/tui/onboarding.go`
- move:
  - onboarding state machine
  - onboarding form helpers
  - onboarding model loading/profile switching

- create `internal/tui/settings.go`
- move:
  - settings state machine
  - model filtering/loading
  - provider form save/switch behavior

### Phase 3: Reassess state shape
- only after file splitting is complete, decide whether `Model` should gain narrower embedded state structs such as composer/provider/execution state
- do not do this during the first pass unless a slice cannot be isolated otherwise

## Test Checklist
### Shared helper extraction
- create `internal/controller/controller_test_helpers_test.go`
- move:
  - `stubAgent`
  - runner/monitor/context-reader test doubles
  - common setup helpers

- create `internal/tui/test_helpers_test.go`
- move:
  - `fakeWorkspace`
  - `fakeController`
  - transcript/index helper functions
  - common builders/assertion helpers

### Controller test split
- create:
  - `internal/controller/controller_agent_test.go`
  - `internal/controller/controller_execution_test.go`
  - `internal/controller/controller_monitor_test.go`
  - `internal/controller/controller_plan_test.go`

- group tests by behavior:
  - agent turn normalization and plan handling
  - shell submission and tracked execution lifecycle
  - monitor snapshot/foreground attach behavior
  - plan continuation/normalization helpers

### TUI test split
- create:
  - `internal/tui/model_update_test.go`
  - `internal/tui/composer_test.go`
  - `internal/tui/transcript_test.go`
  - `internal/tui/execution_test.go`
  - `internal/tui/onboarding_test.go`
  - `internal/tui/settings_test.go`
  - `internal/tui/render_test.go`

- group tests by behavior:
  - message/update routing
  - composer editing and completion
  - transcript/detail/scroll behavior
  - active execution, fullscreen, and handoff flows
  - onboarding flow
  - settings flow
  - rendering/status/footer/card output

## Execution Order
1. extract controller test helpers
2. extract TUI test helpers
3. split controller pure helpers into separate files
4. split controller orchestration files
5. split controller tests to match
6. split TUI dispatch and helper files
7. split TUI rendering/onboarding/settings files
8. split TUI tests to match
9. do a final cleanup pass for naming, comments, and file ownership

## Validation
- run `go test ./internal/controller ./internal/tui` after each major slice
- run the relevant integration tests if controller execution behavior changes materially
- confirm no new cyclic dependencies were introduced
- confirm each new file has a coherent, single responsibility

## Done Criteria
- `internal/controller/controller.go` is no longer the primary home for plan, execution, monitor, and shell-sync logic
- `internal/tui/model.go` is no longer the primary home for update routing, rendering, onboarding, settings, and composer logic all at once
- test helpers are centralized
- test files are behavior-oriented and materially smaller than the current monoliths
