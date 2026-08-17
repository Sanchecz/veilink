// Package protocol implements the Veilink v1 wire format carried in binary
// WebSocket messages. Cryptography and record protection are provided by TLS.
package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const Version byte = 1
const HeaderSize = 8
const MaxPacketSize = 65_535

var magic = [2]byte{'V', 'L'}

type Type byte

const (
	TypeHello   Type = 1
	TypeWelcome Type = 2
	TypePacket  Type = 3
	TypeError   Type = 4
)

var (
	ErrShortFrame         = errors.New("frame is shorter than header")
	ErrInvalidMagic       = errors.New("invalid frame magic")
	ErrUnsupportedVersion = errors.New("unsupported protocol version")
	ErrLengthMismatch     = errors.New("frame length mismatch")
	ErrPayloadTooLarge    = errors.New("frame payload is too large")
	ErrInvalidType        = errors.New("invalid frame type")
)

type Frame struct {
	Type    Type
	Flags   uint16
	Payload []byte
}

func Encode(typ Type, flags uint16, payload []byte) ([]byte, error) {
	if !validType(typ) {
		return nil, ErrInvalidType
	}
	length := len(payload)
	if length > MaxPacketSize {
		return nil, ErrPayloadTooLarge
	}
	b := make([]byte, HeaderSize+length)
	copy(b[:2], magic[:])
	b[2] = Version
	b[3] = byte(typ)
	binary.BigEndian.PutUint16(b[4:6], flags)
	binary.BigEndian.PutUint16(b[6:8], uint16(length))
	copy(b[HeaderSize:], payload)
	return b, nil
}

func Decode(b []byte) (Frame, error) {
	if len(b) < HeaderSize {
		return Frame{}, ErrShortFrame
	}
	if b[0] != magic[0] || b[1] != magic[1] {
		return Frame{}, ErrInvalidMagic
	}
	if b[2] != Version {
		return Frame{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, b[2], Version)
	}
	typ := Type(b[3])
	if !validType(typ) {
		return Frame{}, ErrInvalidType
	}
	n := int(binary.BigEndian.Uint16(b[6:8]))
	if n != len(b)-HeaderSize {
		return Frame{}, ErrLengthMismatch
	}
	payload := make([]byte, n)
	copy(payload, b[HeaderSize:])
	return Frame{Type: typ, Flags: binary.BigEndian.Uint16(b[4:6]), Payload: payload}, nil
}

func validType(typ Type) bool {
	return typ >= TypeHello && typ <= TypeError
}
