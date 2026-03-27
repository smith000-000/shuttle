#!/usr/bin/env bash

set -euo pipefail

LOCAL_ROOT_DEFAULT="${XDG_DATA_HOME:-$HOME/.local/share}/shuttle/coding-agents"
SCOPE="local"
LOCAL_ROOT="${LOCAL_ROOT:-$LOCAL_ROOT_DEFAULT}"
FORCE=0
DRY_RUN=0

usage() {
  cat <<'EOF'
Usage:
  scripts/install-coding-agents.sh [options] [pi] [codex|codex-cli] [all]

Options:
  --scope local|global   Install into Shuttle-managed local paths or npm global space.
  --local-root DIR       Root directory for local installs.
  --force                Reinstall even if the command already exists.
  --dry-run              Print what would run without changing the system.
  -h, --help             Show this help.

Examples:
  scripts/install-coding-agents.sh pi
  scripts/install-coding-agents.sh --scope global codex
  scripts/install-coding-agents.sh --local-root "$HOME/.local/share/shuttle/agents" all
EOF
}

main() {
  local agents=()
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --scope)
        [[ $# -ge 2 ]] || die "--scope requires a value"
        SCOPE="$(normalize_scope "$2")"
        shift 2
        ;;
      --local-root)
        [[ $# -ge 2 ]] || die "--local-root requires a value"
        LOCAL_ROOT="$2"
        shift 2
        ;;
      --force)
        FORCE=1
        shift
        ;;
      --dry-run)
        DRY_RUN=1
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *)
        agents+=("$(normalize_agent "$1")")
        shift
        ;;
    esac
  done

  require_cmd npm

  if [[ ${#agents[@]} -eq 0 ]]; then
    agents=("pi" "codex")
  fi
  if contains_value "all" "${agents[@]}"; then
    agents=("pi" "codex")
  fi
  agents=($(dedupe_values "${agents[@]}"))

  local local_bin_dir=""
  if [[ "$SCOPE" == "local" ]]; then
    local_bin_dir="${LOCAL_ROOT}/npm/bin"
    if [[ "$DRY_RUN" -eq 0 ]]; then
      mkdir -p "${LOCAL_ROOT}/npm"
    fi
  fi

  for agent in "${agents[@]}"; do
    install_agent "$agent" "$local_bin_dir"
  done
}

install_agent() {
  local agent="$1"
  local local_bin_dir="$2"
  local binary package target_path install_display_path
  binary="$(agent_binary "$agent")"
  package="$(agent_package "$agent")"

  if [[ "$SCOPE" == "local" ]]; then
    target_path="${local_bin_dir}/${binary}"
    install_display_path="$target_path"
    if [[ "$FORCE" -eq 0 ]]; then
      if command -v "$binary" >/dev/null 2>&1; then
        echo "skipping ${agent}: ${binary} already available at $(command -v "$binary")"
        print_shuttle_hint "$agent" "$(command -v "$binary")"
        return
      fi
      if [[ -x "$target_path" ]]; then
        echo "skipping ${agent}: local install already exists at ${target_path}"
        print_shuttle_hint "$agent" "$target_path"
        return
      fi
    fi
    run_cmd npm install -g --prefix "${LOCAL_ROOT}/npm" "$package"
  else
    install_display_path="global npm"
    if [[ "$FORCE" -eq 0 ]] && command -v "$binary" >/dev/null 2>&1; then
      echo "skipping ${agent}: ${binary} already available at $(command -v "$binary")"
      print_shuttle_hint "$agent" "$(command -v "$binary")"
      return
    fi
    run_cmd npm install -g "$package"
    target_path="$(command -v "$binary" || true)"
  fi

  if [[ "$DRY_RUN" -eq 1 ]]; then
    echo "would verify ${binary} after install from ${install_display_path}"
    return
  fi

  if [[ "$SCOPE" == "local" ]]; then
    [[ -x "$target_path" ]] || die "${agent} install did not produce ${target_path}"
  else
    [[ -n "$target_path" ]] || die "${agent} install did not expose ${binary} in PATH"
  fi

  echo "installed ${agent} -> ${target_path}"
  if [[ "$SCOPE" == "local" ]]; then
    print_path_hint "$local_bin_dir"
  fi
  print_shuttle_hint "$agent" "$target_path"
}

print_path_hint() {
  local bin_dir="$1"
  case ":${PATH}:" in
    *":${bin_dir}:"*) ;;
    *)
      echo "note: add ${bin_dir} to PATH"
      ;;
  esac
}

print_shuttle_hint() {
  local agent="$1"
  local binary_path="$2"
  case "$agent" in
    pi)
      cat <<EOF
Shuttle hint:
  export SHUTTLE_RUNTIME=pi
  export SHUTTLE_RUNTIME_COMMAND=${binary_path}
EOF
      ;;
    codex)
      cat <<EOF
Shuttle hint:
  export SHUTTLE_PROVIDER=codex_cli
  export SHUTTLE_CLI_COMMAND=${binary_path}
EOF
      ;;
  esac
}

normalize_scope() {
  case "${1:-}" in
    local|global)
      printf '%s\n' "$1"
      ;;
    *)
      die "scope must be local or global"
      ;;
  esac
}

normalize_agent() {
  case "${1:-}" in
    pi)
      printf 'pi\n'
      ;;
    codex|codex-cli|codex_cli)
      printf 'codex\n'
      ;;
    all)
      printf 'all\n'
      ;;
    *)
      die "unsupported agent: $1"
      ;;
  esac
}

agent_binary() {
  case "$1" in
    pi) printf 'pi\n' ;;
    codex) printf 'codex\n' ;;
    *)
      die "unsupported agent binary lookup: $1"
      ;;
  esac
}

agent_package() {
  case "$1" in
    pi) printf '@mariozechner/pi-coding-agent\n' ;;
    codex) printf '@openai/codex\n' ;;
    *)
      die "unsupported agent package lookup: $1"
      ;;
  esac
}

dedupe_values() {
  local seen=""
  local value
  for value in "$@"; do
    case " ${seen} " in
      *" ${value} "*) ;;
      *)
        seen="${seen} ${value}"
        printf '%s\n' "$value"
        ;;
    esac
  done
}

contains_value() {
  local needle="$1"
  shift
  local value
  for value in "$@"; do
    if [[ "$value" == "$needle" ]]; then
      return 0
    fi
  done
  return 1
}

run_cmd() {
  if [[ "$DRY_RUN" -eq 1 ]]; then
    printf 'dry-run:'
    printf ' %q' "$@"
    printf '\n'
    return
  fi
  "$@"
}

require_cmd() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

die() {
  echo "$*" >&2
  exit 1
}

main "$@"
