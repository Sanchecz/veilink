package metrics

import (
	"bytes"
	"strings"
	"testing"
)

func TestWritePrometheus(t *testing.T) {
	var c Counters
	c.Accepted.Store(2)
	c.ActiveSessions.Store(1)
	var b bytes.Buffer
	if err := c.WritePrometheus(&b); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"veilink_active_sessions 1", "veilink_sessions_accepted_total 2", "# TYPE veilink_invalid_packets_total counter"} {
		if !strings.Contains(b.String(), expected) {
			t.Errorf("missing %q", expected)
		}
	}
}
