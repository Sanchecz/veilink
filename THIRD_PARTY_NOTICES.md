# Third-party notices

Veilink depends on the following projects. Release artifacts must retain their
licenses; consult `go.sum` and the module graph for the exact resolved set.

- `github.com/coder/websocket` — ISC license.
- `golang.zx2c4.com/wireguard` TUN package — MIT license.
- `golang.zx2c4.com/wintun` Go bindings — MIT license.
- Wintun prebuilt signed DLL — distributable license included in the official
  Wintun archive and copied into Windows release bundles.
- `gopkg.in/yaml.v3` — MIT and Apache-2.0 licensed components.
- `golang.org/x/net` and `golang.org/x/sys` — BSD-style licenses.

No third-party cryptographic implementation is copied into this repository;
TLS uses the Go standard library through Caddy and the HTTPS client.
