#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMPDIR_ROOT="$(mktemp -d)"
trap 'rm -rf "$TMPDIR_ROOT"' EXIT

DIST_DIR="${TMPDIR_ROOT}/dist"
DOWNLOAD_ROOT="${TMPDIR_ROOT}/downloads"
INSTALL_DIR="${TMPDIR_ROOT}/install"
VERSION="v0.0.0-test"

mkdir -p "$DIST_DIR" "$DOWNLOAD_ROOT/${VERSION}"

(
  cd "$ROOT_DIR"
  DIST_DIR="$DIST_DIR" TARGETS="linux/amd64" VERSION="$VERSION" ./scripts/package-release.sh
)

cp "$DIST_DIR"/shuttle_"${VERSION}"_linux_amd64.tar.gz "$DOWNLOAD_ROOT/${VERSION}/"
cp "$DIST_DIR"/SHA256SUMS "$DOWNLOAD_ROOT/${VERSION}/"

DOWNLOAD_BASE_URL="file://${DOWNLOAD_ROOT}" \
INSTALL_DIR="$INSTALL_DIR" \
VERSION="$VERSION" \
  "$ROOT_DIR/scripts/install-release.sh"

"$INSTALL_DIR/shuttle" --version | grep -F "$VERSION" >/dev/null
