# Interactive Harness

This subfolder contains tmux-driven interactive integration coverage for Shuttle.

The harness runs the real TUI in one isolated tmux server and lets Shuttle create
its managed workspace in a second isolated tmux server. That gives the tests a
real tty for the bottom-pane interaction while still allowing direct inspection
of the managed shell panes, trace logs, and workspace files.

Current coverage:
- patch proposal -> apply -> auto-continue
- patch proposal -> failed apply -> corrected retry -> auto-continue
- command proposal -> run -> auto-continue
- checklist plan -> command -> eval -> command -> completion without `Ctrl+G`
- builtin handoff suggestion -> approve -> PI RPC external turn -> `F3` runtime activity streaming
- full app restart -> external-work banner -> resume external context -> PI RPC activity streaming

Artifacts:
- each test writes trace, pane captures, and provider request logs into a temp
  artifact directory
- when a test fails, the artifact directory path is reported in the test output

Run only this harness:

```bash
go test ./integration/harness -v
```

Run the patch-focused test script:

```bash
./integration/harness/run_patch_tests.sh
```

Requirements:
- `tmux`
- `go`

Notes:
- the external-runtime harness path uses a repository-local fake PI RPC helper so
  the test can exercise the real Shuttle handoff/controller/TUI loop without a
  network dependency or a separately installed coding runtime
- future external runtimes such as Codex still need their own harness coverage
