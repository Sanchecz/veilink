# Veilink protocol v1

## Transport

Veilink v1 uses one authenticated WebSocket connection over HTTPS. Production
URLs must use `wss://`. TLS termination is performed by Caddy with TLS 1.3; the
application listener is loopback plaintext and must never be exposed directly.

Authentication is an HTTP `Authorization: Bearer <token>` header inside TLS.
Tokens contain 32 random bytes encoded as `vl1_<base64url>`. The server stores
`sha256:<hex>` digests and maps each digest to one IPv4 address.

## Binary frame

Every application WebSocket message is binary and contains exactly one frame:

| Offset | Size | Field | Value |
|---:|---:|---|---|
| 0 | 2 | magic | ASCII `VL` |
| 2 | 1 | version | `1` |
| 3 | 1 | type | see below |
| 4 | 2 | flags | unsigned big-endian, zero in v1 |
| 6 | 2 | length | payload length, unsigned big-endian |
| 8 | N | payload | exactly `length` bytes |

Types are `1 HELLO`, `2 WELCOME`, `3 PACKET`, and `4 ERROR`. Control payloads
are compact UTF-8 JSON limited to 4096 bytes. PACKET payloads contain exactly
one complete IPv4 packet. Trailing bytes, IPv6, invalid IHL/total length, frames
larger than 65,535 bytes, and unknown versions or types are rejected.

## Handshake

After the WebSocket upgrade, the client sends HELLO:

```json
{"client_name":"laptop","session_id":"random-base64url","mtu":1280}
```

The server replies with WELCOME containing the fixed address assigned to the
authenticated token:

```json
{"address":"10.77.0.2","gateway":"10.77.0.1","mtu":1280,"session_id":"same-value"}
```

The echoed session ID binds the response to the request at the application
layer. TLS provides replay and record integrity protection. v1 flags must be
zero when sent; receivers currently ignore unknown flag bits for forward
compatibility.

## Data invariants

- Client-to-server PACKET source must equal the address assigned to the token.
- Server-to-client PACKET destination must equal that client's address.
- Packet size must not exceed the negotiated MTU.
- Only one live session owns an assigned address. A later authenticated session
  replaces the earlier one.
- WebSocket compression is disabled to remove compression side channels and
  unpredictable CPU/memory use.

## Versioning

Changing the header, authentication semantics, or packet invariants requires a
new protocol version. A transport can be added without changing v1 frame
semantics. Unknown versions fail closed; there is no silent downgrade.
