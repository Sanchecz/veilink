# Operations runbook

## Supported production topology

- Debian 13 or Ubuntu 24.04+ VDS, amd64 or arm64.
- Public domain with a valid A record.
- Caddy on ports 80/443; Veilink on `127.0.0.1:8080`; metrics on
  `127.0.0.1:9090`.
- nftables for client isolation and IPv4 masquerade.
- One fixed private IPv4 address and token per client.

Do not expose 8080 or 9090 in the provider firewall. Keep SSH recovery access
before changing forwarding/firewall rules.

## Install

1. Verify the release archive against `SHA256SUMS` and obtain it over an
   authenticated channel.
2. Install `veilink` to `/usr/local/bin/veilink` mode `0755`.
3. Create the service identity and directories:

   ```bash
   sudo useradd --system --home /nonexistent --shell /usr/sbin/nologin veilink
   sudo install -d -o root -g veilink -m 0750 /etc/veilink
   sudo install -o root -g veilink -m 0640 configs/server.example.yaml /etc/veilink/server.yaml
   ```

4. Generate one token per client. Put only the digest in `server.yaml` and the
   raw token in that client's mode-`0600` config.
5. Install `deploy/sysctl/99-veilink.conf`,
   `deploy/nftables/veilink.nft`, `deploy/systemd/veilink.service`, and the
   Caddyfile fragment. Ensure `/etc/nftables.conf` includes
   `/etc/nftables.d/*.nft` and `/etc/caddy/Caddyfile` imports
   `/etc/caddy/veilink.caddy`. The supplied installer performs those two
   idempotent additions. Replace the domain and the VDS uplink if your firewall
   policy requires an explicit interface.
6. Apply and validate:

   ```bash
   sudo sysctl --system
   sudo nft -c -f /etc/nftables.d/veilink.nft
   sudo veilink validate --type server --config /etc/veilink/server.yaml
   sudo systemd-analyze security veilink.service
   sudo systemctl daemon-reload
   sudo systemctl enable --now nftables caddy veilink
   ```

## Client

Linux requires root/CAP_NET_ADMIN and the `ip` command. DNS configuration uses
`resolvectl`; when DNS entries are configured but `resolvectl` is unavailable,
startup fails instead of leaking DNS. Windows requires an elevated process and
the official signed architecture-matching `wintun.dll` beside `veilink.exe`.

Start interactively for the first test, keeping a second terminal available:

```bash
sudo veilink client --config /etc/veilink/client.yaml
```

Verify the public address and DNS through destinations you trust. Then stop the
client and confirm its temporary routes are removed. The client keeps tunnel
routes while reconnecting; this is the kill-switch behavior.

## Monitoring

Scrape `127.0.0.1:9090/metrics` through a local Prometheus agent. Alert on:

- Veilink or Caddy service down.
- `veilink_invalid_packets_total` increasing.
- sustained `veilink_packets_dropped_total` growth.
- connection/auth rejection spikes in application and Caddy logs.
- disk, bandwidth, conntrack, CPU, and memory saturation on the VDS.

Health endpoints: `/healthz` means the process HTTP handler is alive;
`/readyz` means the tunnel listener and TUN pump were initialized.

## Rotation and revocation

Generate a new token, add its digest/address entry, validate, and restart the
service. After the new client connects, remove the old entry and restart again.
A restart intentionally terminates all active sessions. Never assign the same
address or token digest twice.

## Upgrade

1. Read `CHANGELOG.md`; back up `/etc/veilink` securely.
2. Verify checksums/signatures and run config validation with the new binary.
3. Replace the binary atomically (`install` to a temporary path, then `mv`).
4. `systemctl restart veilink`; verify ready/metrics and one test client.
5. Retain the prior binary until the observation window completes.

## Rollback

Restore the previous binary and configuration, restart Veilink, and verify
readiness. If routing is unhealthy, stop Veilink, remove only the `veilink0`
rules shown by `ip route show dev veilink0`, and reload nftables. Do not flush
the host firewall or all routes.

## Incident response

For suspected token theft: remove the client entry, restart, rotate the token,
review auth-rejection/session metrics and Caddy access logs, and check the host
for broader compromise. For VDS compromise: revoke all tokens, destroy rather
than reuse the host, rotate DNS and administrative credentials, rebuild from a
known-good image, and issue fresh client configs.
