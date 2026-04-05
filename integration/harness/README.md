# Interactive Harness

This subfolder contains tmux-driven interactive integration coverage for Shuttle.

The harness is currently opt-in and does not run in ordinary `go test` or
`make test-integration` flows unless `SHUTTLE_RUN_INTERACTIVE_HARNESS=1` is set.
This is intentional while interactive UX automation is paused.

The harness runs the real TUI in one isolated tmux server and lets Shuttle create
its managed workspace in a second isolated tmux server. That gives the tests a
real tty for the bottom-pane interaction while still allowing direct inspection
of the managed shell panes, trace logs, and workspace files.

Current coverage:
- patch proposal -> apply -> auto-continue
- patch proposal -> failed apply -> corrected retry -> auto-continue
- command proposal -> run -> auto-continue
- checklist plan -> command -> eval -> command -> completion without `Ctrl+G`

Artifacts:
- each test writes trace, pane captures, and provider request logs into a temp
  artifact directory
- when a test fails, the artifact directory path is reported in the test output

Run only this harness:

```bash
SHUTTLE_RUN_INTERACTIVE_HARNESS=1 go test ./integration/harness -v
```

Run the patch-focused test script:

```bash
SHUTTLE_RUN_INTERACTIVE_HARNESS=1 ./integration/harness/run_patch_tests.sh
```

Requirements:
- `tmux`
- `go`
