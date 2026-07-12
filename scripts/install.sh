#!/usr/bin/env bash
set -Eeuo pipefail

PROJECT_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
INSTALL_BIN="/usr/local/bin/unknowntunnel"
CONFIG_DIR="/etc/unknowntunnel"
DOC_DIR="/usr/share/doc/unknowntunnel"
UNIT_FILE="/etc/systemd/system/unknowntunnel@.service"

log() { printf '[Unknowntunnel] %s\n' "$*"; }
die() { printf '[Unknowntunnel] ERROR: %s\n' "$*" >&2; exit 1; }

[[ ${EUID:-$(id -u)} -eq 0 ]] || die "run this installer as root"
[[ "$(uname -s)" == "Linux" ]] || die "only Linux is supported"
command -v install >/dev/null 2>&1 || die "the install command is required"
command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required"
command -v systemctl >/dev/null 2>&1 || die "systemd is required"
command -v ip >/dev/null 2>&1 || die "iproute2 is required"
if [[ ! -e /dev/net/tun ]]; then
  log "warning: /dev/net/tun is missing; Layer 4 forwarding can still run, but Layer 3 requires TUN"
fi

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  armv7l|armv7) arch="armv7" ;;
  *) arch="" ;;
esac

binary=""
if [[ -n "$arch" && -f "$PROJECT_ROOT/dist/unknowntunnel-linux-$arch" ]]; then
  binary="$PROJECT_ROOT/dist/unknowntunnel-linux-$arch"
  command -v sha256sum >/dev/null 2>&1 || die "sha256sum is required to verify the prebuilt binary"
  [[ -f "$PROJECT_ROOT/dist/SHA256SUMS" ]] || die "dist/SHA256SUMS is missing"
  expected="$(grep "  unknowntunnel-linux-$arch$" "$PROJECT_ROOT/dist/SHA256SUMS" || true)"
  [[ -n "$expected" ]] || die "checksum for architecture $arch is missing"
  (cd "$PROJECT_ROOT/dist" && printf '%s\n' "$expected" | sha256sum -c -)
else
  command -v go >/dev/null 2>&1 || die "no prebuilt binary for this architecture and Go is not installed"
  log "building the binary from source"
  mkdir -p "$PROJECT_ROOT/dist"
  binary="$PROJECT_ROOT/dist/unknowntunnel-local"
  version="$(cat "$PROJECT_ROOT/VERSION")"
  (cd "$PROJECT_ROOT" && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$binary" ./cmd/unknowntunnel)
fi

log "installing binary"
install -m 0755 "$binary" "$INSTALL_BIN"
install -d -m 0700 "$CONFIG_DIR"
install -d -m 0755 "$DOC_DIR"
install -m 0644 "$PROJECT_ROOT/README.md" "$DOC_DIR/README.md"
install -m 0644 "$PROJECT_ROOT/examples/server.json" "$DOC_DIR/server.json.example"
install -m 0644 "$PROJECT_ROOT/examples/client.json" "$DOC_DIR/client.json.example"
install -m 0644 "$PROJECT_ROOT/systemd/unknowntunnel@.service" "$UNIT_FILE"
systemctl daemon-reload

log "installation completed"
log "copy an example to $CONFIG_DIR, create the shared secret, validate the config, then enable the matching instance"
log "example: systemctl enable --now unknowntunnel@server"
