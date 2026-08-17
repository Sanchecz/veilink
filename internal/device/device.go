package device

import (
	"fmt"

	wgtun "golang.zx2c4.com/wireguard/tun"
)

// Device is the narrow packet interface used by the core. Keeping it small
// makes the protocol testable without administrator privileges or a real TUN.
type Device interface {
	ReadPacket([]byte) (int, error)
	WritePacket([]byte) error
	Name() (string, error)
	Close() error
}

type wireGuardDevice struct{ tun wgtun.Device }

func Open(name string, mtu int) (Device, error) {
	t, err := wgtun.CreateTUN(name, mtu)
	if err != nil {
		return nil, fmt.Errorf("create TUN %q: %w", name, err)
	}
	return &wireGuardDevice{tun: t}, nil
}

func (d *wireGuardDevice) ReadPacket(dst []byte) (int, error) {
	sizes := []int{0}
	n, err := d.tun.Read([][]byte{dst}, sizes, 0)
	if err != nil {
		return 0, err
	}
	if n != 1 {
		return 0, fmt.Errorf("TUN returned unexpected packet count %d", n)
	}
	return sizes[0], nil
}

func (d *wireGuardDevice) WritePacket(packet []byte) error {
	n, err := d.tun.Write([][]byte{packet}, 0)
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("TUN wrote unexpected packet count %d", n)
	}
	return nil
}

func (d *wireGuardDevice) Name() (string, error) { return d.tun.Name() }
func (d *wireGuardDevice) Close() error          { return d.tun.Close() }
