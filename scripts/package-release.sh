#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist}"
TARGETS="${TARGETS:-linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64,windows/arm64}"
VERSION="${VERSION:-}"
COMMIT="${COMMIT:-$(git -C "$ROOT_DIR" rev-parse --short HEAD)}"
BUILD_DATE="${BUILD_DATE:-$(date -u +%Y-%m-%dT%H:%M:%SZ)}"

if [[ -z "$VERSION" ]]; then
  if VERSION="$(git -C "$ROOT_DIR" describe --tags --exact-match 2>/dev/null)"; then
    :
  else
    VERSION="dev-$COMMIT"
  fi
fi

mkdir -p "$DIST_DIR"
rm -rf "$DIST_DIR/stage"
mkdir -p "$DIST_DIR/stage"

if command -v sha256sum >/dev/null 2>&1; then
  CHECKSUM_CMD=(sha256sum)
elif command -v shasum >/dev/null 2>&1; then
  CHECKSUM_CMD=(shasum -a 256)
else
  echo "missing checksum tool: need sha256sum or shasum" >&2
  exit 1
fi

LD_FLAGS=(
  "-X" "aiterm/internal/version.Version=$VERSION"
  "-X" "aiterm/internal/version.Commit=$COMMIT"
  "-X" "aiterm/internal/version.BuildDate=$BUILD_DATE"
)

IFS=',' read -r -a target_list <<< "$TARGETS"
ARCHIVE_PATHS=()
for target in "${target_list[@]}"; do
  target="${target//[[:space:]]/}"
  if [[ -z "$target" ]]; then
    continue
  fi

  goos="${target%/*}"
  goarch="${target#*/}"
  if [[ -z "$goos" || -z "$goarch" || "$goos" == "$target" ]]; then
    echo "invalid target: $target (expected GOOS/GOARCH)" >&2
    exit 1
  fi

  archive_root="shuttle_${VERSION}_${goos}_${goarch}"
  stage_dir="$DIST_DIR/stage/$archive_root"
  binary_name="shuttle"
  archive_ext=".tar.gz"
  if [[ "$goos" == "windows" ]]; then
    binary_name="shuttle.exe"
    archive_ext=".zip"
    if ! command -v zip >/dev/null 2>&1; then
      echo "missing archive tool: need zip for Windows targets" >&2
      exit 1
    fi
  fi
  archive_path="$DIST_DIR/${archive_root}${archive_ext}"

  rm -rf "$stage_dir"
  mkdir -p "$stage_dir"

  (
    cd "$ROOT_DIR"
    GOOS="$goos" GOARCH="$goarch" go build \
      -trimpath \
      -ldflags "${LD_FLAGS[*]}" \
      -o "$stage_dir/$binary_name" \
      ./cmd/shuttle
  )

  cp "$ROOT_DIR/README.md" "$stage_dir/"
  cp "$ROOT_DIR/LICENSE" "$stage_dir/"
  cp "$ROOT_DIR/env.sh.sample" "$stage_dir/"

  if [[ "$goos" == "windows" ]]; then
    (
      cd "$DIST_DIR/stage"
      zip -rq "$archive_path" "$archive_root"
    )
  else
    tar -C "$DIST_DIR/stage" -czf "$archive_path" "$archive_root"
  fi
  ARCHIVE_PATHS+=("$archive_path")
done

(
  cd "$DIST_DIR"
  if [[ ${#ARCHIVE_PATHS[@]} -eq 0 ]]; then
    echo "no archives were produced" >&2
    exit 1
  fi
  "${CHECKSUM_CMD[@]}" "${ARCHIVE_PATHS[@]##$DIST_DIR/}" > SHA256SUMS
)

rm -rf "$DIST_DIR/stage"

echo "packaged Shuttle $VERSION into $DIST_DIR"
