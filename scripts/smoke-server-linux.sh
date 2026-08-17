#!/usr/bin/env bash
set -euo pipefail

if [[ "$(id -u)" -ne 0 ]]; then echo "run as root" >&2; exit 2; fi
binary="${1:-./veilink}"
tmp="$(mktemp -d)"
pid=""
cleanup() {
  if [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null; then kill -TERM "$pid"; wait "$pid" || true; fi
  ip link delete veilinksmoke 2>/dev/null || true
  rm -rf -- "$tmp"
}
trap cleanup EXIT

cat > "$tmp/server.yaml" <<'YAML'
listen: 127.0.0.1:18080
metrics_listen: 127.0.0.1:19090
tunnel_path: /assets/v1/stream
network: 10.253.0.0/24
gateway: 10.253.0.1
interface: veilinksmoke
mtu: 1280
handshake_timeout: 2s
idle_timeout: 15s
shutdown_timeout: 5s
max_clients: 1
log: {level: info, format: json}
clients:
  - name: smoke
    address: 10.253.0.2
    token_sha256: "sha256:0000000000000000000000000000000000000000000000000000000000000000"
YAML
chmod 0640 "$tmp/server.yaml"

sysctl -q -w net.ipv4.ip_forward=1
"$binary" validate --type server --config "$tmp/server.yaml"
"$binary" server --config "$tmp/server.yaml" >"$tmp/server.log" 2>&1 &
pid="$!"
for _ in $(seq 1 50); do curl -fsS -o /dev/null http://127.0.0.1:18080/readyz && break; sleep .1; done
curl -fsS -o /dev/null http://127.0.0.1:18080/healthz
curl -fsS http://127.0.0.1:19090/metrics | grep -q '^veilink_active_sessions 0$'
test "$(curl -sS -o /dev/null -w '%{http_code}' http://127.0.0.1:18080/assets/v1/stream)" = 404
kill -TERM "$pid"
wait "$pid"
pid=""
grep -q 'tunnel listener ready' "$tmp/server.log"
echo "server smoke test passed"
