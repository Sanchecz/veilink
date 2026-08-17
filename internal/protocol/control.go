package protocol

import (
	"encoding/json"
	"fmt"
	"net/netip"
)

type Hello struct {
	ClientName string `json:"client_name"`
	SessionID  string `json:"session_id"`
	MTU        int    `json:"mtu"`
}

type Welcome struct {
	Address   string `json:"address"`
	Gateway   string `json:"gateway"`
	MTU       int    `json:"mtu"`
	SessionID string `json:"session_id"`
}

type ErrorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func MarshalControl(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("marshal control message: %w", err)
	}
	if len(b) > 4096 {
		return nil, ErrPayloadTooLarge
	}
	return b, nil
}

func UnmarshalControl(b []byte, v any) error {
	if len(b) > 4096 {
		return ErrPayloadTooLarge
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("decode control message: %w", err)
	}
	return nil
}

// IPv4SourceDestination extracts addresses without trusting variable IPv4
// header fields. IPv6 is deliberately unsupported in protocol v1.
func IPv4SourceDestination(packet []byte) (src, dst netip.Addr, err error) {
	if len(packet) < 20 {
		return netip.Addr{}, netip.Addr{}, errorsNew("IPv4 packet is shorter than 20 bytes")
	}
	if packet[0]>>4 != 4 {
		return netip.Addr{}, netip.Addr{}, errorsNew("protocol v1 accepts IPv4 packets only")
	}
	ihl := int(packet[0]&0x0f) * 4
	if ihl < 20 || ihl > len(packet) {
		return netip.Addr{}, netip.Addr{}, errorsNew("invalid IPv4 header length")
	}
	total := int(packet[2])<<8 | int(packet[3])
	if total < ihl || total != len(packet) {
		return netip.Addr{}, netip.Addr{}, errorsNew("invalid IPv4 total length")
	}
	src = netip.AddrFrom4([4]byte{packet[12], packet[13], packet[14], packet[15]})
	dst = netip.AddrFrom4([4]byte{packet[16], packet[17], packet[18], packet[19]})
	return src, dst, nil
}

type protocolError string

func (e protocolError) Error() string { return string(e) }

func errorsNew(s string) error { return protocolError(s) }
