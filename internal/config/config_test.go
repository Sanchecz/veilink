package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"veilink/internal/auth"
)

func TestLoadServerStrictAndValid(t *testing.T) {
	token, _ := auth.Generate()
	hash, _ := auth.Hash(token)
	path := writeTemp(t, `listen: 127.0.0.1:8080
metrics_listen: 127.0.0.1:9090
network: 10.77.0.0/24
gateway: 10.77.0.1
clients:
  - name: alice
    address: 10.77.0.2
    token_sha256: "`+hash+`"
`)
	cfg, err := LoadServer(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MTU != 1280 || cfg.TunnelPath == "" {
		t.Fatalf("defaults not applied: %#v", cfg)
	}

	unknown := writeTemp(t, "unknown_field: true\n")
	if _, err := LoadServer(unknown); err == nil || !strings.Contains(err.Error(), "field unknown_field") {
		t.Fatalf("unknown field was not rejected: %v", err)
	}
	multiple := writeTemp(t, "---\nlisten: 127.0.0.1:8080\n---\nlisten: 127.0.0.1:8081\n")
	if _, err := LoadServer(multiple); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("multiple documents were not rejected: %v", err)
	}
}

func TestServerRejectsUnsafeTopology(t *testing.T) {
	token, _ := auth.Generate()
	hash, _ := auth.Hash(token)
	base := Server{Listen: "127.0.0.1:8080", MetricsListen: "127.0.0.1:9090", TunnelPath: "/x", Network: "10.77.0.0/24", Gateway: "10.77.0.1", Interface: "veilink0", MTU: 1280, MaxClients: 10, Handshake: Duration{Duration: 2_000_000_000}, Idle: Duration{Duration: 20_000_000_000}, Clients: []ServerClient{{Name: "a", Address: "10.77.0.2", TokenSHA256: hash}}}
	if err := base.Validate(); err != nil {
		t.Fatalf("base config invalid: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(*Server)
	}{
		{"public listener", func(c *Server) { c.Listen = "0.0.0.0:8080" }},
		{"public network", func(c *Server) { c.Network = "8.8.8.0/24"; c.Gateway = "8.8.8.1"; c.Clients[0].Address = "8.8.8.2" }},
		{"network address", func(c *Server) { c.Clients[0].Address = "10.77.0.0" }},
		{"broadcast address", func(c *Server) { c.Clients[0].Address = "10.77.0.255" }},
		{"reserved path", func(c *Server) { c.TunnelPath = "/healthz" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			c.Clients = append([]ServerClient(nil), base.Clients...)
			tc.mutate(&c)
			if err := c.Validate(); err == nil {
				t.Fatal("unsafe config accepted")
			}
		})
	}
}

func TestClientRequiresWSSAndStrongToken(t *testing.T) {
	token, _ := auth.Generate()
	c := Client{ServerURL: "wss://vpn.example/assets/v1/stream", Token: token, Name: "a", Interface: "veilink0", MTU: 1280, DialTimeout: Duration{Duration: 2_000_000_000}, KeepAlive: Duration{Duration: 10_000_000_000}, Reconnect: Duration{Duration: 2_000_000_000}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	c.ServerURL = "ws://vpn.example/x"
	if err := c.Validate(); err == nil {
		t.Fatal("plaintext WebSocket URL accepted")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
