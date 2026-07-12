#!/usr/bin/env bash
set -Eeuo pipefail

purge=false
if [[ "${1:-}" == "--purge" ]]; then
  purge=true
elif [[ $# -gt 0 ]]; then
  printf 'Usage: %s [--purge]\n' "$0" >&2
  exit 2
fi

[[ ${EUID:-$(id -u)} -eq 0 ]] || { echo "run as root" >&2; exit 1; }

if command -v systemctl >/dev/null 2>&1; then
  while read -r unit; do
    [[ -n "$unit" ]] || continue
    systemctl disable --now "$unit" >/dev/null 2>&1 || true
  done < <(
    {
      systemctl list-units --all 'unknowntunnel@*.service' --no-legend --plain 2>/dev/null | awk '{print $1}'
      systemctl list-unit-files 'unknowntunnel@*.service' --no-legend 2>/dev/null | awk '{print $1}'
    } | awk '/^unknowntunnel@[^.][^[:space:]]*\.service$/' | sort -u
  )
fi

rm -f /usr/local/bin/unknowntunnel
rm -f /etc/systemd/system/unknowntunnel@.service
rm -rf /usr/share/doc/unknowntunnel
if $purge; then
  rm -rf /etc/unknowntunnel
fi
systemctl daemon-reload 2>/dev/null || true

echo "Unknowntunnel removed. Configuration was preserved unless --purge was used."
