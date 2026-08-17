# Changelog

## 0.1.0-rc.2 - 2026-08-17

- Published the private test repository with `main` and `develop` branches for
  continued development.
- Preserved executable permissions for Linux deployment, smoke-test, and
  release-build scripts.
- Removed dead Linux platform code and restricted system command execution to
  the explicitly supported `ip` and `resolvectl` programs.
- Added manually repeatable CI verification and updated artifact publishing to
  the current Node.js 24-based action runtime.

## 0.1.0-rc.1 - 2026-08-17

- Initial Veilink v1 protocol, Linux VDS server, and Linux/Windows clients.
- TLS 1.3/WebSocket transport behind a loopback-only Caddy origin.
- Per-client token digests and fixed addresses with source/destination checks.
- Full IPv4 routes, reconnect kill-switch behavior, DNS handling, and IPv6
  fail-closed routes.
- Prometheus metrics, health endpoints, systemd/nftables/sysctl deployment,
  unit/integration/fuzz seeds, race/vulnerability CI, and release packaging.
