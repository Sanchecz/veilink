package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"veilink/internal/auth"
	"veilink/internal/config"
	"veilink/internal/device"
	"veilink/internal/platform"
	"veilink/internal/protocol"
)

type fakeDevice struct {
	reads  chan []byte
	writes chan []byte
	closed chan struct{}
	once   sync.Once
}

func newFakeDevice() *fakeDevice {
	return &fakeDevice{reads: make(chan []byte, 8), writes: make(chan []byte, 8), closed: make(chan struct{})}
}
func (d *fakeDevice) ReadPacket(dst []byte) (int, error) {
	select {
	case p := <-d.reads:
		return copy(dst, p), nil
	case <-d.closed:
		return 0, io.EOF
	}
}
func (d *fakeDevice) WritePacket(p []byte) error { d.writes <- append([]byte(nil), p...); return nil }
func (d *fakeDevice) Name() (string, error)      { return "fake0", nil }
func (d *fakeDevice) Close() error               { d.once.Do(func() { close(d.closed) }); return nil }

func TestReadTunFiltersSpoofedSource(t *testing.T) {
	c := New(config.Client{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dev := newFakeDevice()
	out := make(chan []byte, 2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.readTun(ctx, dev, out, netip.MustParseAddr("10.77.0.2"), 1280)
	dev.reads <- clientIPv4([4]byte{10, 77, 0, 99}, [4]byte{8, 8, 8, 8}, nil)
	want := clientIPv4([4]byte{10, 77, 0, 2}, [4]byte{8, 8, 8, 8}, []byte{1})
	dev.reads <- want
	select {
	case got := <-out:
		if string(got) != string(want) {
			t.Fatal("unexpected packet passed filter")
		}
	case <-time.After(time.Second):
		t.Fatal("valid packet was not forwarded")
	}
	select {
	case <-out:
		t.Fatal("more than one packet forwarded")
	case <-time.After(20 * time.Millisecond):
	}
}

func TestRunSessionExchangesPackets(t *testing.T) {
	assigned := netip.MustParseAddr("10.77.0.2")
	responsePacket := clientIPv4([4]byte{8, 8, 8, 8}, [4]byte{10, 77, 0, 2}, []byte{9})
	serverErr := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		typ, raw, err := conn.Read(ctx)
		if err != nil || typ != websocket.MessageBinary {
			serverErr <- err
			return
		}
		frame, err := protocol.Decode(raw)
		if err != nil || frame.Type != protocol.TypePacket {
			serverErr <- err
			return
		}
		wire, _ := protocol.Encode(protocol.TypePacket, 0, responsePacket)
		serverErr <- conn.Write(ctx, websocket.MessageBinary, wire)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	c := New(config.Client{KeepAlive: config.Duration{Duration: time.Second}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	dev := newFakeDevice()
	out := make(chan []byte, 1)
	out <- clientIPv4([4]byte{10, 77, 0, 2}, [4]byte{1, 1, 1, 1}, []byte{7})
	done := make(chan error, 1)
	go func() { done <- c.runSession(ctx, conn, dev, out, assigned, 1280) }()
	select {
	case got := <-dev.writes:
		if string(got) != string(responsePacket) {
			t.Fatal("response packet changed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response not written to TUN")
	}
	if err := <-serverErr; err != nil {
		t.Fatalf("websocket server: %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session did not stop after cancellation")
	}
}

func TestRunSessionRejectsWrongDestination(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		wire, _ := protocol.Encode(protocol.TypePacket, 0, clientIPv4([4]byte{8, 8, 8, 8}, [4]byte{10, 77, 0, 99}, nil))
		_ = conn.Write(r.Context(), websocket.MessageBinary, wire)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	c := New(config.Client{KeepAlive: config.Duration{Duration: time.Second}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err = c.runSession(ctx, conn, newFakeDevice(), make(chan []byte), netip.MustParseAddr("10.77.0.2"), 1280)
	if err == nil || !strings.Contains(err.Error(), "destination") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRunConnectsTLS13SetsUpAndCleansUp(t *testing.T) {
	token, _ := auth.Generate()
	serverDone := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 1 {
			t.Errorf("websocket handshake used HTTP/%d", r.ProtoMajor)
		}
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.NotFound(w, r)
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		typ, raw, err := conn.Read(r.Context())
		if err != nil || typ != websocket.MessageBinary {
			return
		}
		frame, err := protocol.Decode(raw)
		if err != nil || frame.Type != protocol.TypeHello {
			return
		}
		var hello protocol.Hello
		if protocol.UnmarshalControl(frame.Payload, &hello) != nil {
			return
		}
		payload, _ := protocol.MarshalControl(protocol.Welcome{Address: "10.77.0.2", Gateway: "10.77.0.1", MTU: 1280, SessionID: hello.SessionID})
		welcome, _ := protocol.Encode(protocol.TypeWelcome, 0, payload)
		if conn.Write(r.Context(), websocket.MessageBinary, welcome) != nil {
			return
		}
		_, _, _ = conn.Read(r.Context())
		close(serverDone)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	server.StartTLS()
	defer server.Close()
	pool := x509.NewCertPool()
	pool.AddCert(server.Certificate())
	cfg := config.Client{ServerURL: "wss" + strings.TrimPrefix(server.URL, "https") + "/assets/v1/stream", Token: token, Name: "test", Interface: "veilink0", MTU: 1280, DialTimeout: config.Duration{Duration: 2 * time.Second}, KeepAlive: config.Duration{Duration: time.Second}, Reconnect: config.Duration{Duration: time.Second}}
	c := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	c.rootCAs = pool
	dev := newFakeDevice()
	c.open = func(name string, mtu int) (device.Device, error) {
		if name != "veilink0" || mtu != 1280 {
			t.Errorf("open(%q,%d)", name, mtu)
		}
		return dev, nil
	}
	setupCalled := make(chan struct{})
	var cleaned atomic.Bool
	c.setup = func(_ context.Context, o platform.ClientOptions) (platform.Cleanup, error) {
		if o.Address != netip.MustParseAddr("10.77.0.2") || o.Gateway != netip.MustParseAddr("10.77.0.1") {
			t.Errorf("setup options: %#v", o)
		}
		close(setupCalled)
		return func(context.Context) error { cleaned.Store(true); return nil }, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-setupCalled:
	case <-time.After(3 * time.Second):
		t.Fatal("client did not set up tunnel")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("client did not stop")
	}
	if !cleaned.Load() {
		t.Fatal("route cleanup was not called")
	}
	select {
	case <-dev.closed:
	default:
		t.Fatal("device was not closed")
	}
}

func clientIPv4(src, dst [4]byte, payload []byte) []byte {
	p := make([]byte, 20+len(payload))
	p[0] = 0x45
	p[2], p[3] = byte(len(p)>>8), byte(len(p))
	copy(p[12:16], src[:])
	copy(p[16:20], dst[:])
	copy(p[20:], payload)
	return p
}
