package metrics

import (
	"fmt"
	"io"
	"sync/atomic"
)

type Counters struct {
	ActiveSessions atomic.Int64
	Accepted       atomic.Int64
	AuthRejected   atomic.Int64
	PacketsFromTun atomic.Int64
	PacketsToTun   atomic.Int64
	BytesFromTun   atomic.Int64
	BytesToTun     atomic.Int64
	Dropped        atomic.Int64
	Invalid        atomic.Int64
}

// WritePrometheus exposes a fixed, allocation-light Prometheus text format.
func (c *Counters) WritePrometheus(w io.Writer) error {
	values := []struct {
		name string
		help string
		typ  string
		val  int64
	}{
		{"veilink_active_sessions", "Current authenticated tunnel sessions.", "gauge", max(c.ActiveSessions.Load(), 0)},
		{"veilink_sessions_accepted_total", "Authenticated sessions accepted.", "counter", c.Accepted.Load()},
		{"veilink_auth_rejected_total", "Rejected authentication attempts.", "counter", c.AuthRejected.Load()},
		{"veilink_packets_from_tun_total", "Packets read from the server TUN.", "counter", c.PacketsFromTun.Load()},
		{"veilink_packets_to_tun_total", "Packets written to the server TUN.", "counter", c.PacketsToTun.Load()},
		{"veilink_bytes_from_tun_total", "Bytes read from the server TUN.", "counter", c.BytesFromTun.Load()},
		{"veilink_bytes_to_tun_total", "Bytes written to the server TUN.", "counter", c.BytesToTun.Load()},
		{"veilink_packets_dropped_total", "Packets dropped because of backpressure or no route.", "counter", c.Dropped.Load()},
		{"veilink_invalid_packets_total", "Packets rejected by protocol validation.", "counter", c.Invalid.Load()},
	}
	for _, v := range values {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n%s %d\n", v.name, v.help, v.name, v.typ, v.name, v.val); err != nil {
			return err
		}
	}
	return nil
}
