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
  DIST_DIR="$DIST_DIR" TARGETS="linux/amd64,windows/amd64" VERSION="$VERSION" ./scripts/package-release.sh
)

cp "$DIST_DIR"/shuttle_"${VERSION}"_linux_amd64.tar.gz "$DOWNLOAD_ROOT/${VERSION}/"
cp "$DIST_DIR"/SHA256SUMS "$DOWNLOAD_ROOT/${VERSION}/"

python3 - "$DIST_DIR/shuttle_${VERSION}_windows_amd64.zip" <<'PY'
import sys
import zipfile

with zipfile.ZipFile(sys.argv[1]) as archive:
    names = set(archive.namelist())

expected = {
    "shuttle_v0.0.0-test_windows_amd64/shuttle.exe",
    "shuttle_v0.0.0-test_windows_amd64/README.md",
    "shuttle_v0.0.0-test_windows_amd64/LICENSE",
    "shuttle_v0.0.0-test_windows_amd64/env.sh.sample",
}
missing = expected - names
if missing:
    raise SystemExit(f"missing files from windows archive: {sorted(missing)}")
PY

DOWNLOAD_BASE_URL="file://${DOWNLOAD_ROOT}" \
INSTALL_DIR="$INSTALL_DIR" \
VERSION="$VERSION" \
  "$ROOT_DIR/scripts/install-release.sh"

"$INSTALL_DIR/shuttle" --version | grep -F "$VERSION" >/dev/null
