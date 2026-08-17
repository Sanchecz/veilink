#!/usr/bin/env bash
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then echo "install-server.sh must run as root" >&2; exit 2; fi
if [[ "$#" -ne 2 ]]; then echo "usage: $0 /path/to/veilink vpn.example.com" >&2; exit 2; fi
binary="$(readlink -f -- "$1")"
domain="$2"
case "$domain" in *[!A-Za-z0-9.-]*|.*|*.) echo "invalid domain" >&2; exit 2;; esac
[[ -x "$binary" ]] || { echo "binary is not executable: $binary" >&2; exit 2; }

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
for command in caddy nft sysctl systemctl; do command -v "$command" >/dev/null || { echo "missing dependency: $command" >&2; exit 1; }; done

id veilink >/dev/null 2>&1 || useradd --system --home /nonexistent --shell /usr/sbin/nologin veilink
install -o root -g root -m 0755 "$binary" /usr/local/bin/veilink
install -d -o root -g veilink -m 0750 /etc/veilink
if [[ ! -e /etc/veilink/server.yaml ]]; then
  install -o root -g veilink -m 0640 "$root/configs/server.example.yaml" /etc/veilink/server.yaml
  echo "Created /etc/veilink/server.yaml; replace the token placeholder before starting." >&2
fi

install -o root -g root -m 0644 "$root/deploy/systemd/veilink.service" /etc/systemd/system/veilink.service
install -o root -g root -m 0644 "$root/deploy/sysctl/99-veilink.conf" /etc/sysctl.d/99-veilink.conf
install -d -o root -g root -m 0755 /etc/nftables.d
install -o root -g root -m 0644 "$root/deploy/nftables/veilink.nft" /etc/nftables.d/veilink.nft
touch /etc/nftables.conf
grep -Fqx 'include "/etc/nftables.d/*.nft"' /etc/nftables.conf || printf '\ninclude "/etc/nftables.d/*.nft"\n' >> /etc/nftables.conf

install -d -o root -g root -m 0755 /etc/caddy /etc/systemd/system/caddy.service.d
install -o root -g root -m 0644 "$root/deploy/caddy/Caddyfile" /etc/caddy/veilink.caddy
printf 'VEILINK_DOMAIN=%s\n' "$domain" > /etc/caddy/veilink.env
chmod 0644 /etc/caddy/veilink.env
install -o root -g root -m 0644 "$root/deploy/caddy/caddy-veilink-env.conf" /etc/systemd/system/caddy.service.d/veilink.conf
touch /etc/caddy/Caddyfile
grep -Fqx 'import /etc/caddy/veilink.caddy' /etc/caddy/Caddyfile || printf '\nimport /etc/caddy/veilink.caddy\n' >> /etc/caddy/Caddyfile

sysctl --system >/dev/null
nft -c -f /etc/nftables.conf
caddy validate --config /etc/caddy/Caddyfile --adapter caddyfile
systemctl daemon-reload

if /usr/local/bin/veilink validate --type server --config /etc/veilink/server.yaml; then
  systemctl enable --now nftables caddy veilink
  echo "Veilink installed and started. Verify /readyz and metrics locally."
else
  systemctl enable nftables caddy veilink
  echo "Installation complete but service was not started: edit /etc/veilink/server.yaml, validate it, then start services." >&2
fi
