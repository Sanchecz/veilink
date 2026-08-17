package core

import (
	"context"
	"net/netip"
	"sync"

	"veilink/internal/metrics"
)

type Peer struct {
	Name   string
	Addr   netip.Addr
	Send   chan []byte
	Cancel context.CancelFunc
}

type Hub struct {
	mu      sync.RWMutex
	peers   map[netip.Addr]*Peer
	metrics *metrics.Counters
	max     int
}

func NewHub(m *metrics.Counters, maxPeers int) *Hub {
	return &Hub{peers: make(map[netip.Addr]*Peer), metrics: m, max: maxPeers}
}

// Register installs a peer and cancels any older session for the same address.
func (h *Hub) Register(peer *Peer) bool {
	h.mu.Lock()
	old := h.peers[peer.Addr]
	if old == nil && len(h.peers) >= h.max {
		h.mu.Unlock()
		return false
	}
	h.peers[peer.Addr] = peer
	h.mu.Unlock()
	if old != nil {
		old.Cancel()
	} else {
		h.metrics.ActiveSessions.Add(1)
	}
	h.metrics.Accepted.Add(1)
	return true
}

func (h *Hub) Unregister(peer *Peer) {
	h.mu.Lock()
	if h.peers[peer.Addr] == peer {
		delete(h.peers, peer.Addr)
		h.metrics.ActiveSessions.Add(-1)
	}
	h.mu.Unlock()
}

func (h *Hub) Dispatch(addr netip.Addr, packet []byte) bool {
	h.mu.RLock()
	peer := h.peers[addr]
	h.mu.RUnlock()
	if peer == nil {
		h.metrics.Dropped.Add(1)
		return false
	}
	copyOfPacket := append([]byte(nil), packet...)
	select {
	case peer.Send <- copyOfPacket:
		return true
	default:
		h.metrics.Dropped.Add(1)
		return false
	}
}

func (h *Hub) CloseAll() {
	h.mu.RLock()
	peers := make([]*Peer, 0, len(h.peers))
	for _, peer := range h.peers {
		peers = append(peers, peer)
	}
	h.mu.RUnlock()
	for _, peer := range peers {
		peer.Cancel()
	}
}
