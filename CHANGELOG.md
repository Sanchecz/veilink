# Changelog

## 0.1.0-rc.1 - 2026-08-17

- Initial Veilink v1 protocol, Linux VDS server, and Linux/Windows clients.
- TLS 1.3/WebSocket transport behind a loopback-only Caddy origin.
- Per-client token digests and fixed addresses with source/destination checks.
- Full IPv4 routes, reconnect kill-switch behavior, DNS handling, and IPv6
  fail-closed routes.
- Prometheus metrics, health endpoints, systemd/nftables/sysctl deployment,
  unit/integration/fuzz seeds, race/vulnerability CI, and release packaging.
