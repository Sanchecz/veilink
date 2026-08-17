# Architecture

## Components

`cmd/veilink` is the single executable. `server` and `client` are separate
subcommands so packaging, version reporting, configuration validation, and
logging remain identical.

The core packages have deliberately narrow responsibilities:

- `internal/protocol`: deterministic v1 frame encoding and IPv4 validation.
- `internal/auth`: token generation, validation, digest parsing, and
  constant-time comparison helpers.
- `internal/device`: a small packet-device abstraction over WireGuard's mature
  cross-platform TUN implementation.
- `internal/core`: session registry, replacement semantics, bounded queues, and
  address-based packet routing.
- `internal/server`: authentication, WebSocket lifecycle, anti-spoofing, TUN
  pump, health endpoints, and metrics.
- `internal/client`: certificate-validating HTTPS dial, handshake, reconnect,
  keepalive, packet validation, and TUN pump.
- `internal/platform`: transactional Linux/Windows address and route changes.
- `internal/config`: strict YAML decoding, defaults, and topology validation.

## Packet path

The client OS sends IPv4 packets to a /32 TUN address. Two /1 routes cover all
IPv4 destinations while a more-specific /32 route keeps the VDS connection on
the original gateway. The client validates that locally emitted packets use its
assigned source address before framing them.

The server authenticates the HTTP upgrade, maps the token digest to one fixed
client address, and validates every packet's source. Valid packets are injected
into the server TUN. Linux forwarding and nftables masquerade them to the VDS
uplink. Return packets are read from the TUN and dispatched by IPv4 destination
to the matching authenticated session. The client performs the symmetric
destination check before injecting a response into its TUN.

## Failure behavior

- A disconnected client keeps the full-tunnel routes during reconnect, so
  traffic fails closed instead of silently returning to the physical gateway.
- Graceful shutdown removes only the routes and addresses created by Veilink.
- Server sessions use bounded queues. A slow consumer drops packets rather than
  growing memory without limit.
- A new session for an already assigned address cancels the old session
  atomically.
- Malformed frames or source/destination violations terminate the session.
- An unauthenticated tunnel request is indistinguishable at the HTTP status
  level from a missing decoy resource (`404`).

## Trust boundaries

Caddy is the public TLS boundary. The Go HTTP listener is forced by validation
to a loopback address. The metrics endpoint is also loopback-only. Raw bearer
tokens exist only on clients; server config contains digests. Root/CAP_NET_ADMIN
is required for TUN and routes, but the systemd service drops all unrelated
capabilities and has a read-only filesystem view.
