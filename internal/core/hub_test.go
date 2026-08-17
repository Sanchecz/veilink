package core

import (
	"context"
	"net/netip"
	"testing"

	"veilink/internal/metrics"
)

func TestHubCapacityDispatchAndReplacement(t *testing.T) {
	m := &metrics.Counters{}
	h := NewHub(m, 1)
	cancelled := false
	p1 := &Peer{Name: "one", Addr: netip.MustParseAddr("10.77.0.2"), Send: make(chan []byte, 1), Cancel: func() { cancelled = true }}
	if !h.Register(p1) {
		t.Fatal("first peer rejected")
	}
	p2 := &Peer{Name: "two", Addr: netip.MustParseAddr("10.77.0.3"), Send: make(chan []byte, 1), Cancel: func() {}}
	if h.Register(p2) {
		t.Fatal("capacity limit ignored")
	}
	if !h.Dispatch(p1.Addr, []byte{1, 2, 3}) {
		t.Fatal("dispatch failed")
	}
	packet := <-p1.Send
	packet[0] = 9
	replacement := &Peer{Name: "replacement", Addr: p1.Addr, Send: make(chan []byte, 1), Cancel: func() {}}
	if !h.Register(replacement) || !cancelled {
		t.Fatal("replacement did not cancel old peer")
	}
	if m.ActiveSessions.Load() != 1 {
		t.Fatalf("active sessions=%d", m.ActiveSessions.Load())
	}
	h.Unregister(p1)
	if m.ActiveSessions.Load() != 1 {
		t.Fatal("old peer unregistered replacement")
	}
	h.Unregister(replacement)
	if m.ActiveSessions.Load() != 0 {
		t.Fatal("replacement was not unregistered")
	}
	_ = context.Canceled
}

func TestHubBackpressureDrops(t *testing.T) {
	m := &metrics.Counters{}
	h := NewHub(m, 1)
	p := &Peer{Addr: netip.MustParseAddr("10.77.0.2"), Send: make(chan []byte, 1), Cancel: func() {}}
	h.Register(p)
	h.Dispatch(p.Addr, []byte{1})
	if h.Dispatch(p.Addr, []byte{2}) {
		t.Fatal("full queue accepted packet")
	}
	if m.Dropped.Load() != 1 {
		t.Fatalf("drops=%d", m.Dropped.Load())
	}
}
