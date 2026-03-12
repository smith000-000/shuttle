#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="$ROOT_DIR/env.sh"
ENV_SAMPLE_FILE="$ROOT_DIR/env.sh.sample"

if [[ -f "$ENV_FILE" ]]; then
  # shellcheck disable=SC1091
  source "$ENV_FILE"
elif [[ -f "$ENV_SAMPLE_FILE" ]]; then
  echo "launch.sh: env.sh not found, loading env.sh.sample defaults" >&2
  # shellcheck disable=SC1091
  source "$ENV_SAMPLE_FILE"
fi

cd "$ROOT_DIR"

if [[ $# -eq 0 ]]; then
  DEFAULT_SESSION="${SHUTTLE_SESSION:-shuttle-dev}"
  DEFAULT_SOCKET="${SHUTTLE_TMUX_SOCKET:-$DEFAULT_SESSION}"
  set -- --socket "$DEFAULT_SOCKET" --session "$DEFAULT_SESSION" --tui
fi

exec go run ./cmd/shuttle "$@"
