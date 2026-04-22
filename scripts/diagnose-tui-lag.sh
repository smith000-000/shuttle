#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

profile="${1:-list}"
if [[ $# -gt 0 ]]; then
  shift
fi

disable=""

case "$profile" in
  list|--list|-l)
    cat <<'EOF'
Usage:
  scripts/diagnose-tui-lag.sh PROFILE [launch args...]

Profiles:
  baseline
  shell-completion
  history-completion
  slash-completion
  completion-ghost
  footer-hints
  status-line
  shell-context
  approval-label
  model-status
  context-usage
  busy-indicator
  action-card
  plan-card
  execution-card
  shell-tail
  transcript-chrome
  transcript
  mouse
  busy-tick
  execution-polling
  shell-context-polling
  cards-off
  chrome-off
  polling-off
  minimal-ui

Recommended order:
  1. baseline
  2. shell-completion
  3. history-completion
  4. slash-completion
  5. completion-ghost
  6. footer-hints
  7. status-line
  8. shell-context
  9. approval-label
  10. model-status
  11. context-usage
  12. busy-indicator
  13. action-card
  14. plan-card
  15. execution-card
  16. shell-tail
  17. transcript-chrome
  18. transcript
  19. mouse
  20. busy-tick
  21. execution-polling
  22. shell-context-polling

Examples:
  scripts/diagnose-tui-lag.sh baseline
  scripts/diagnose-tui-lag.sh shell-completion
  scripts/diagnose-tui-lag.sh chrome-off --tui
  scripts/diagnose-tui-lag.sh polling-off --trace-mode safe --tui
EOF
    exit 0
    ;;
  baseline)
    disable=""
    ;;
  shell-completion)
    disable="shell-completion"
    ;;
  history-completion)
    disable="history-completion"
    ;;
  slash-completion)
    disable="slash-completion"
    ;;
  completion-ghost)
    disable="completion-ghost"
    ;;
  footer-hints)
    disable="footer-hints"
    ;;
  status-line)
    disable="status-line"
    ;;
  shell-context)
    disable="shell-context"
    ;;
  approval-label)
    disable="approval-label"
    ;;
  model-status)
    disable="model-status"
    ;;
  context-usage)
    disable="context-usage"
    ;;
  busy-indicator)
    disable="busy-indicator"
    ;;
  action-card)
    disable="action-card"
    ;;
  plan-card)
    disable="plan-card"
    ;;
  execution-card)
    disable="execution-card"
    ;;
  shell-tail)
    disable="shell-tail"
    ;;
  transcript-chrome)
    disable="transcript-chrome"
    ;;
  transcript)
    disable="transcript"
    ;;
  mouse)
    disable="mouse"
    ;;
  busy-tick)
    disable="busy-tick"
    ;;
  execution-polling)
    disable="execution-polling"
    ;;
  shell-context-polling)
    disable="shell-context-polling"
    ;;
  cards-off)
    disable="action-card,plan-card,execution-card"
    ;;
  chrome-off)
    disable="completion-ghost,footer-hints,status-line,context-usage,busy-indicator,action-card,plan-card,execution-card,shell-tail,transcript-chrome"
    ;;
  polling-off)
    disable="busy-tick,execution-polling,shell-context-polling,shell-tail"
    ;;
  minimal-ui)
    disable="shell-completion,history-completion,slash-completion,completion-ghost,footer-hints,status-line,context-usage,busy-indicator,action-card,plan-card,execution-card,shell-tail,transcript-chrome,mouse"
    ;;
  *)
    echo "Unknown profile: $profile" >&2
    echo "Run scripts/diagnose-tui-lag.sh list for available profiles." >&2
    exit 1
    ;;
esac

if [[ -n "$disable" ]]; then
  export SHUTTLE_TUI_DISABLE="$disable"
  echo "diagnose-tui-lag: SHUTTLE_TUI_DISABLE=$SHUTTLE_TUI_DISABLE" >&2
else
  unset SHUTTLE_TUI_DISABLE || true
  echo "diagnose-tui-lag: baseline launch" >&2
fi

has_tui_flag=0
for arg in "$@"; do
  if [[ "$arg" == "--tui" ]]; then
    has_tui_flag=1
    break
  fi
done
if [[ $has_tui_flag -eq 0 ]]; then
  exec "$ROOT_DIR/launch.sh" "$@" --tui
fi

exec "$ROOT_DIR/launch.sh" "$@"
