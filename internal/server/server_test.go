package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"veilink/internal/auth"
	"veilink/internal/config"
	"veilink/internal/core"
	"veilink/internal/protocol"
)

type mockDevice struct {
	reads  chan []byte
	writes chan []byte
	closed chan struct{}
	once   sync.Once
}

func newMockDevice() *mockDevice {
	return &mockDevice{reads: make(chan []byte, 8), writes: make(chan []byte, 8), closed: make(chan struct{})}
}
func (d *mockDevice) ReadPacket(dst []byte) (int, error) {
	select {
	case p := <-d.reads:
		return copy(dst, p), nil
	case <-d.closed:
		return 0, io.EOF
	}
}
func (d *mockDevice) WritePacket(p []byte) error { d.writes <- append([]byte(nil), p...); return nil }
func (d *mockDevice) Name() (string, error)      { return "mock0", nil }
func (d *mockDevice) Close() error               { d.once.Do(func() { close(d.closed) }); return nil }

func TestTunnelAuthenticationAndPacketExchange(t *testing.T) {
	token, _ := auth.Generate()
	hash, _ := auth.Hash(token)
	cfg := testServerConfig(hash)
	cfg.MaxClients = 2
	dev := newMockDevice()
	srv, err := New(cfg, dev, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	httpServer := httptest.NewServer(http.HandlerFunc(srv.tunnel))
	defer httpServer.Close()

	req, _ := http.NewRequest(http.MethodGet, httpServer.URL, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unauthenticated status=%d", resp.StatusCode)
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	payload, _ := protocol.MarshalControl(protocol.Hello{ClientName: "ignored", SessionID: "session-test", MTU: 1280})
	hello, _ := protocol.Encode(protocol.TypeHello, 0, payload)
	if err := conn.Write(ctx, websocket.MessageBinary, hello); err != nil {
		t.Fatal(err)
	}
	typ, raw, err := conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary {
		t.Fatalf("welcome: typ=%v err=%v", typ, err)
	}
	frame, err := protocol.Decode(raw)
	if err != nil || frame.Type != protocol.TypeWelcome {
		t.Fatalf("welcome frame: %#v %v", frame, err)
	}
	var welcome protocol.Welcome
	if err := protocol.UnmarshalControl(frame.Payload, &welcome); err != nil {
		t.Fatal(err)
	}
	if welcome.Address != "10.77.0.2" || welcome.SessionID != "session-test" {
		t.Fatalf("welcome=%#v", welcome)
	}

	outbound := testIPv4([4]byte{10, 77, 0, 2}, [4]byte{8, 8, 8, 8}, []byte{1})
	wire, _ := protocol.Encode(protocol.TypePacket, 0, outbound)
	if err := conn.Write(ctx, websocket.MessageBinary, wire); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-dev.writes:
		if string(got) != string(outbound) {
			t.Fatal("TUN packet changed")
		}
	case <-ctx.Done():
		t.Fatal("packet was not written to TUN")
	}

	deadline := time.Now().Add(time.Second)
	for srv.metrics.ActiveSessions.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	responsePacket := testIPv4([4]byte{8, 8, 8, 8}, [4]byte{10, 77, 0, 2}, []byte{2})
	if !srv.hub.Dispatch(netip.MustParseAddr("10.77.0.2"), responsePacket) {
		t.Fatal("hub dispatch failed")
	}
	typ, raw, err = conn.Read(ctx)
	if err != nil || typ != websocket.MessageBinary {
		t.Fatalf("read response: %v", err)
	}
	frame, err = protocol.Decode(raw)
	if err != nil || string(frame.Payload) != string(responsePacket) {
		t.Fatalf("response mismatch: %v", err)
	}
}

func TestTunnelRejectsSourceSpoofing(t *testing.T) {
	token, _ := auth.Generate()
	hash, _ := auth.Hash(token)
	cfg := testServerConfig(hash)
	srv, _ := New(cfg, newMockDevice(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	httpServer := httptest.NewServer(http.HandlerFunc(srv.tunnel))
	defer httpServer.Close()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+token)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(httpServer.URL, "http"), &websocket.DialOptions{HTTPHeader: header})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	payload, _ := protocol.MarshalControl(protocol.Hello{SessionID: "spoof-test", MTU: 1280})
	hello, _ := protocol.Encode(protocol.TypeHello, 0, payload)
	_ = conn.Write(ctx, websocket.MessageBinary, hello)
	_, _, _ = conn.Read(ctx)
	spoof := testIPv4([4]byte{10, 77, 0, 99}, [4]byte{8, 8, 8, 8}, nil)
	wire, _ := protocol.Encode(protocol.TypePacket, 0, spoof)
	_ = conn.Write(ctx, websocket.MessageBinary, wire)
	_, _, err = conn.Read(ctx)
	if err == nil {
		t.Fatal("spoofing connection stayed open")
	}
	if srv.metrics.Invalid.Load() != 1 {
		t.Fatalf("invalid metric=%d", srv.metrics.Invalid.Load())
	}
}

func TestNewRejectsMalformedHash(t *testing.T) {
	cfg := testServerConfig("sha256:00")
	_, err := New(cfg, newMockDevice(), slog.Default())
	if err == nil || !strings.Contains(err.Error(), "token_sha256") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPacketLoopAndRunLifecycle(t *testing.T) {
	token, _ := auth.Generate()
	hash, _ := auth.Hash(token)
	cfg := testServerConfig(hash)
	cfg.Listen = "127.0.0.1:0"
	cfg.MetricsListen = "127.0.0.1:0"
	dev := newMockDevice()
	srv, _ := New(cfg, dev, slog.New(slog.NewTextHandler(io.Discard, nil)))
	_, peerCancel := context.WithCancel(context.Background())
	defer peerCancel()
	peer := &core.Peer{Name: "alice", Addr: netip.MustParseAddr("10.77.0.2"), Send: make(chan []byte, 1), Cancel: peerCancel}
	if !srv.hub.Register(peer) {
		t.Fatal("peer rejected")
	}
	loopCtx, loopCancel := context.WithCancel(context.Background())
	loopDone := make(chan error, 1)
	go func() { loopDone <- srv.packetLoop(loopCtx) }()
	want := testIPv4([4]byte{8, 8, 8, 8}, [4]byte{10, 77, 0, 2}, []byte{4})
	dev.reads <- want
	select {
	case got := <-peer.Send:
		if string(got) != string(want) {
			t.Fatal("packet loop changed packet")
		}
	case <-time.After(time.Second):
		t.Fatal("packet loop did not dispatch")
	}
	loopCancel()
	_ = dev.Close()
	select {
	case <-loopDone:
	case <-time.After(time.Second):
		t.Fatal("packet loop did not stop")
	}

	dev2 := newMockDevice()
	srv2, _ := New(cfg, dev2, slog.New(slog.NewTextHandler(io.Discard, nil)))
	runCtx, runCancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv2.Run(runCtx) }()
	select {
	case <-srv2.Ready():
	case <-time.After(2 * time.Second):
		t.Fatal("server did not become ready")
	}
	runCancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestHTTPUtilityHandlers(t *testing.T) {
	srv := &Server{ready: make(chan struct{})}
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		want    int
		path    string
	}{{"health", srv.health, 204, "/healthz"}, {"not ready", srv.readiness, 503, "/readyz"}, {"decoy", decoy, 200, "/"}, {"decoy missing", decoy, 404, "/missing"}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, tc.path, nil)
			w := httptest.NewRecorder()
			tc.handler(w, r)
			if w.Code != tc.want {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
	close(srv.ready)
	w := httptest.NewRecorder()
	srv.readiness(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if w.Code != 204 {
		t.Fatalf("ready status=%d", w.Code)
	}
	wrapped := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	w = httptest.NewRecorder()
	wrapped.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/", nil))
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("security header missing")
	}
}

func testIPv4(src, dst [4]byte, payload []byte) []byte {
	p := make([]byte, 20+len(payload))
	p[0] = 0x45
	p[2], p[3] = byte(len(p)>>8), byte(len(p))
	copy(p[12:16], src[:])
	copy(p[16:20], dst[:])
	copy(p[20:], payload)
	return p
}

func testServerConfig(hash string) config.Server {
	return config.Server{
		Listen: "127.0.0.1:18080", MetricsListen: "127.0.0.1:19090", TunnelPath: "/assets/v1/stream",
		Network: "10.77.0.0/24", Gateway: "10.77.0.1", Interface: "veilink0", MTU: 1280,
		Handshake: config.Duration{Duration: 2 * time.Second}, Idle: config.Duration{Duration: 20 * time.Second}, Shutdown: config.Duration{Duration: time.Second}, MaxClients: 1,
		Clients: []config.ServerClient{{Name: "alice", Address: "10.77.0.2", TokenSHA256: hash}},
	}
}
