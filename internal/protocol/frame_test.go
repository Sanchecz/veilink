package protocol

import (
	"errors"
	"net/netip"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	payload := []byte("payload")
	raw, err := Encode(TypePacket, 7, payload)
	if err != nil {
		t.Fatal(err)
	}
	frame, err := Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if frame.Type != TypePacket || frame.Flags != 7 || string(frame.Payload) != "payload" {
		t.Fatalf("unexpected frame: %#v", frame)
	}
	raw[HeaderSize] = 'X'
	if string(frame.Payload) != "payload" {
		t.Fatal("Decode returned an alias of caller-owned memory")
	}
}

func TestDecodeRejectsMalformedFrames(t *testing.T) {
	valid, _ := Encode(TypePacket, 0, []byte{1})
	tests := []struct {
		name   string
		raw    []byte
		target error
	}{
		{"short", []byte{1}, ErrShortFrame},
		{"magic", append([]byte(nil), valid...), ErrInvalidMagic},
		{"version", append([]byte(nil), valid...), ErrUnsupportedVersion},
		{"type", append([]byte(nil), valid...), ErrInvalidType},
		{"length", append([]byte(nil), valid...), ErrLengthMismatch},
	}
	tests[1].raw[0] = 0
	tests[2].raw[2] = 9
	tests[3].raw[3] = 99
	tests[4].raw[7] = 2
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Decode(tc.raw)
			if !errors.Is(err, tc.target) {
				t.Fatalf("got %v, want %v", err, tc.target)
			}
		})
	}
}

func FuzzDecode(f *testing.F) {
	valid, _ := Encode(TypePacket, 0, []byte("seed"))
	f.Add(valid)
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, raw []byte) { _, _ = Decode(raw) })
}

func TestIPv4SourceDestination(t *testing.T) {
	packet := ipv4Packet([4]byte{10, 77, 0, 2}, [4]byte{8, 8, 8, 8}, nil)
	src, dst, err := IPv4SourceDestination(packet)
	if err != nil {
		t.Fatal(err)
	}
	if src != netip.MustParseAddr("10.77.0.2") || dst != netip.MustParseAddr("8.8.8.8") {
		t.Fatalf("got %s -> %s", src, dst)
	}
	if _, _, err := IPv4SourceDestination(append(packet, 0)); err == nil {
		t.Fatal("accepted trailing bytes")
	}
	packet[0] = 0x60
	if _, _, err := IPv4SourceDestination(packet); err == nil {
		t.Fatal("accepted IPv6")
	}
}

func ipv4Packet(src, dst [4]byte, payload []byte) []byte {
	p := make([]byte, 20+len(payload))
	p[0] = 0x45
	p[2], p[3] = byte(len(p)>>8), byte(len(p))
	copy(p[12:16], src[:])
	copy(p[16:20], dst[:])
	copy(p[20:], payload)
	return p
}
