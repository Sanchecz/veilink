//go:build linux && integration

package device

import (
	"os/exec"
	"testing"
)

func TestRealTUNLifecycle(t *testing.T) {
	d, err := Open("vldevsmoke", 1280)
	if err != nil {
		t.Fatal(err)
	}
	name, err := d.Name()
	if err != nil {
		t.Fatal(err)
	}
	if name == "" {
		t.Fatal("TUN returned an empty name")
	}
	if out, err := exec.Command("ip", "link", "show", "dev", name).CombinedOutput(); err != nil {
		t.Fatalf("ip link did not find TUN: %v: %s", err, out)
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("ip", "link", "show", "dev", name).Run(); err == nil {
		t.Fatal("TUN still exists after Close")
	}
}
