package main

import (
	"os"
	"path/filepath"
	"testing"

	"veilink/internal/auth"
)

func TestAdministrativeCommands(t *testing.T) {
	if err := run([]string{"version"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"token", "--json"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"help"}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"unknown"}); err == nil {
		t.Fatal("unknown command accepted")
	}
	token, _ := auth.Generate()
	path := filepath.Join(t.TempDir(), "client.yaml")
	content := "server_url: wss://vpn.example/assets/v1/stream\ntoken: \"" + token + "\"\nname: test\ninterface: veilink0\nmtu: 1280\ndial_timeout: 15s\nkeepalive: 25s\nreconnect: 3s\nblock_ipv6: true\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"validate", "--type", "client", "--config", path}); err != nil {
		t.Fatal(err)
	}
}
