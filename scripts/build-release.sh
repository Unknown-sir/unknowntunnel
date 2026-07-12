#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${VERSION:-$(cat "$ROOT/VERSION")}"
mkdir -p "$ROOT/dist"
rm -f "$ROOT/dist"/unknowntunnel-linux-* "$ROOT/dist/SHA256SUMS"

build() {
  local goarch="$1"
  local goarm="${2:-}"
  local output="$ROOT/dist/unknowntunnel-linux-$goarch"
  if [[ "$goarch" == "arm" ]]; then
    output="$ROOT/dist/unknowntunnel-linux-armv7"
  fi
  echo "Building $output"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" GOARM="$goarm" \
    go build -trimpath -ldflags "-s -w -X main.version=$VERSION" -o "$output" ./cmd/unknowntunnel)
}

build amd64
build arm64
build arm 7
(cd "$ROOT/dist" && sha256sum unknowntunnel-linux-* > SHA256SUMS)
cat "$ROOT/dist/SHA256SUMS"
