#!/usr/bin/env bash

set -euo pipefail

REPO="${REPO:-smith000-000/shuttle}"
BINARY_NAME="${BINARY_NAME:-shuttle}"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${VERSION:-}"
DOWNLOAD_BASE_URL="${DOWNLOAD_BASE_URL:-https://github.com/${REPO}/releases/download}"
LATEST_RELEASE_URL="${LATEST_RELEASE_URL:-https://github.com/${REPO}/releases/latest}"

main() {
  require_cmd tar
  downloader="$(resolve_downloader)"
  checksum_tool="$(resolve_checksum_tool)"

  local version
  version="$(resolve_version "$downloader")"

  local platform arch asset_name asset_url checksums_url
  platform="$(detect_platform)"
  arch="$(detect_arch)"
  asset_name="${BINARY_NAME}_${version}_${platform}_${arch}.tar.gz"
  asset_url="${DOWNLOAD_BASE_URL}/${version}/${asset_name}"
  checksums_url="${DOWNLOAD_BASE_URL}/${version}/SHA256SUMS"

  local tmpdir
  tmpdir="$(mktemp -d)"
  trap "rm -rf '$tmpdir'" EXIT

  local archive_path checksums_path
  archive_path="${tmpdir}/${asset_name}"
  checksums_path="${tmpdir}/SHA256SUMS"

  download_file "$downloader" "$asset_url" "$archive_path"
  download_file "$downloader" "$checksums_url" "$checksums_path"
  verify_checksum "$checksum_tool" "$asset_name" "$archive_path" "$checksums_path"

  tar -xzf "$archive_path" -C "$tmpdir"

  local extracted_root binary_path
  extracted_root="${tmpdir}/${BINARY_NAME}_${version}_${platform}_${arch}"
  binary_path="${extracted_root}/${BINARY_NAME}"
  if [[ ! -x "$binary_path" ]]; then
    echo "release archive did not contain executable ${BINARY_NAME}" >&2
    exit 1
  fi

  mkdir -p "$INSTALL_DIR"
  install -m 0755 "$binary_path" "${INSTALL_DIR}/${BINARY_NAME}"

  echo "installed ${BINARY_NAME} to ${INSTALL_DIR}/${BINARY_NAME}"
  "${INSTALL_DIR}/${BINARY_NAME}" --version

  case ":${PATH}:" in
    *":${INSTALL_DIR}:"*) ;;
    *)
      echo "note: ${INSTALL_DIR} is not in PATH"
      ;;
  esac
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "missing required command: $1" >&2
    exit 1
  fi
}

resolve_downloader() {
  if command -v curl >/dev/null 2>&1; then
    echo "curl"
    return
  fi
  if command -v wget >/dev/null 2>&1; then
    echo "wget"
    return
  fi
  echo "missing downloader: need curl or wget" >&2
  exit 1
}

resolve_checksum_tool() {
  if command -v sha256sum >/dev/null 2>&1; then
    echo "sha256sum"
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    echo "shasum"
    return
  fi
  echo "missing checksum tool: need sha256sum or shasum" >&2
  exit 1
}

download_file() {
  local downloader="$1"
  local url="$2"
  local output_path="$3"

  if [[ "$downloader" == "curl" ]]; then
    curl -fsSL "$url" -o "$output_path"
    return
  fi

  wget -qO "$output_path" "$url"
}

resolve_version() {
  local downloader="$1"
  if [[ -n "$VERSION" ]]; then
    echo "$VERSION"
    return
  fi

  if [[ "$DOWNLOAD_BASE_URL" != "https://github.com/${REPO}/releases/download" ]]; then
    echo "set VERSION when using a custom DOWNLOAD_BASE_URL" >&2
    exit 1
  fi

  local effective_url
  if [[ "$downloader" == "curl" ]]; then
    effective_url="$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$LATEST_RELEASE_URL")"
  else
    effective_url="$(wget -qO- --server-response "$LATEST_RELEASE_URL" 2>&1 | awk '/^  Location: / {print $2}' | tail -n1 | tr -d '\r')"
  fi

  local version
  version="$(printf '%s\n' "$effective_url" | sed -E 's#.*/tag/([^/?#]+).*#\1#')"
  if [[ -z "$version" || "$version" == "$effective_url" ]]; then
    echo "failed to resolve latest release version" >&2
    exit 1
  fi
  echo "$version"
}

detect_platform() {
  case "$(uname -s)" in
    Linux) echo "linux" ;;
    Darwin) echo "darwin" ;;
    *)
      echo "unsupported platform: $(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    arm64|aarch64) echo "arm64" ;;
    *)
      echo "unsupported architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

verify_checksum() {
  local checksum_tool="$1"
  local asset_name="$2"
  local archive_path="$3"
  local checksums_path="$4"

  local expected actual
  expected="$(awk -v name="$asset_name" '$2 == name || $2 == ("./" name) { print $1; exit }' "$checksums_path")"
  if [[ -z "$expected" ]]; then
    echo "failed to find checksum for ${asset_name}" >&2
    exit 1
  fi

  if [[ "$checksum_tool" == "sha256sum" ]]; then
    actual="$(sha256sum "$archive_path" | awk '{print $1}')"
  else
    actual="$(shasum -a 256 "$archive_path" | awk '{print $1}')"
  fi

  if [[ "$expected" != "$actual" ]]; then
    echo "checksum mismatch for ${asset_name}" >&2
    exit 1
  fi
}

main "$@"
