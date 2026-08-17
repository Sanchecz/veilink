# Threat model

## Protected against

- Passive observers reading tunneled packet contents: TLS 1.3 encrypts the
  public link.
- On-path modification or replay within a TLS connection: TLS authenticates
  records.
- Unauthenticated tunnel use: a 256-bit random token is required before the
  WebSocket upgrade.
- Token disclosure from a stolen server config: only SHA-256 digests of
  high-entropy tokens are stored.
- One client spoofing another: every packet source is bound to the token's
  configured address.
- Server injection into an unrelated local address: the client validates every
  destination.
- Memory exhaustion by slow authenticated peers: per-peer queues and frame
  sizes are bounded.
- Basic active probing without a token: the endpoint responds as a missing
  decoy resource.
- Accidental plaintext exposure: server and metrics listeners are restricted to
  loopback by configuration validation.

## Not protected against

- Blocking or seizure of the VDS IP or domain.
- Global statistical traffic analysis, long-flow classification, or a strict
  network allowlist.
- A compromised client, VDS kernel, Caddy process, root account, CA, or DNS
  resolver.
- VDS/provider observation of destination IPs, timing, and volume after tunnel
  exit.
- Malicious destinations, phishing, malware, browser fingerprinting, or
  application-layer tracking.
- Denial of service against the public HTTPS endpoint or VDS uplink.
- Future cryptographic breaks in TLS or defects in the Go/Caddy/Wintun supply
  chain.

## Blocking-resilience position

HTTPS/WebSocket reduces special public protocol surface and lets the same host
serve an ordinary page to unauthenticated probes. It does not make the traffic
identical to browser traffic and Veilink intentionally does not impersonate a
browser TLS fingerprint. Operators should plan for IP/domain rotation and a
future pluggable transport rather than advertise an impossible guarantee.

## Secret lifecycle

Generate one token per device. Transfer it over a separate trusted channel,
store client config with owner-only permissions, retain only its digest on the
server, and revoke by deleting the server entry and restarting Veilink. Never
log the Authorization header. Access logs supplied here do not include request
headers. Treat shell history and CI logs as potential disclosure paths.
