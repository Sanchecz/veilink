package server

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"veilink/internal/auth"
	"veilink/internal/config"
	"veilink/internal/core"
	"veilink/internal/device"
	"veilink/internal/metrics"
	"veilink/internal/protocol"
)

type identity struct {
	name string
	addr netip.Addr
}

type Server struct {
	cfg       config.Server
	dev       device.Device
	log       *slog.Logger
	metrics   *metrics.Counters
	hub       *core.Hub
	ident     map[[sha256.Size]byte]identity
	writeMu   sync.Mutex
	ready     chan struct{}
	readyOnce sync.Once
}

func New(cfg config.Server, dev device.Device, logger *slog.Logger) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("server config: %w", err)
	}
	ident := make(map[[sha256.Size]byte]identity, len(cfg.Clients))
	for _, c := range cfg.Clients {
		sum, err := auth.ParseHash(c.TokenSHA256)
		if err != nil {
			return nil, fmt.Errorf("decode token hash for %s: %w", c.Name, err)
		}
		addr, _ := netip.ParseAddr(c.Address)
		ident[sum] = identity{name: c.Name, addr: addr}
	}
	m := &metrics.Counters{}
	return &Server{cfg: cfg, dev: dev, log: logger, metrics: m, hub: core.NewHub(m, cfg.MaxClients), ident: ident, ready: make(chan struct{})}, nil
}

func (s *Server) Metrics() *metrics.Counters { return s.metrics }
func (s *Server) Ready() <-chan struct{}     { return s.ready }

func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 3)
	go func() { errCh <- s.packetLoop(ctx) }()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.readiness)
	mux.HandleFunc(s.cfg.TunnelPath, s.tunnel)
	mux.HandleFunc("/", decoy)
	httpServer := &http.Server{
		Addr:              s.cfg.Listen,
		Handler:           securityHeaders(mux),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	listener, err := net.Listen("tcp", s.cfg.Listen)
	if err != nil {
		_ = s.dev.Close()
		return fmt.Errorf("listen tunnel HTTP: %w", err)
	}
	s.readyOnce.Do(func() { close(s.ready) })
	go func() {
		s.log.Info("tunnel listener ready", "address", listener.Addr().String())
		errCh <- httpServer.Serve(listener)
	}()

	metricsMux := http.NewServeMux()
	metricsMux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		_ = s.metrics.WritePrometheus(w)
	})
	metricsServer := &http.Server{Addr: s.cfg.MetricsListen, Handler: metricsMux, ReadHeaderTimeout: 3 * time.Second, IdleTimeout: 30 * time.Second}
	metricsListener, err := net.Listen("tcp", s.cfg.MetricsListen)
	if err != nil {
		_ = httpServer.Close()
		_ = s.dev.Close()
		return fmt.Errorf("listen metrics HTTP: %w", err)
	}
	go func() { errCh <- metricsServer.Serve(metricsListener) }()

	select {
	case <-ctx.Done():
		s.hub.CloseAll()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.cfg.Shutdown.Duration)
		defer cancel()
		err1 := httpServer.Shutdown(shutdownCtx)
		err2 := metricsServer.Shutdown(shutdownCtx)
		_ = s.dev.Close()
		return errors.Join(err1, err2)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) || errors.Is(err, context.Canceled) {
			return nil
		}
		_ = httpServer.Close()
		_ = metricsServer.Close()
		_ = s.dev.Close()
		return err
	}
}

func (s *Server) packetLoop(ctx context.Context) error {
	buf := make([]byte, protocol.MaxPacketSize)
	for {
		n, err := s.dev.ReadPacket(buf)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("read TUN: %w", err)
		}
		if n <= 0 || n > len(buf) {
			s.metrics.Invalid.Add(1)
			continue
		}
		_, dst, err := protocol.IPv4SourceDestination(buf[:n])
		if err != nil {
			s.metrics.Invalid.Add(1)
			continue
		}
		s.metrics.PacketsFromTun.Add(1)
		s.metrics.BytesFromTun.Add(int64(n))
		s.hub.Dispatch(dst, buf[:n])
	}
}

func (s *Server) tunnel(w http.ResponseWriter, r *http.Request) {
	id, ok := s.authenticate(r)
	if !ok {
		s.metrics.AuthRejected.Add(1)
		http.NotFound(w, r)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		s.log.Debug("websocket upgrade failed", "error", err)
		return
	}
	conn.SetReadLimit(protocol.MaxPacketSize + protocol.HeaderSize)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	defer func() {
		if err := conn.CloseNow(); err != nil {
			s.log.Debug("websocket close failed", "error", err)
		}
	}()

	helloCtx, helloCancel := context.WithTimeout(ctx, s.cfg.Handshake.Duration)
	typ, raw, err := conn.Read(helloCtx)
	helloCancel()
	if err != nil || typ != websocket.MessageBinary {
		return
	}
	frame, err := protocol.Decode(raw)
	if err != nil || frame.Type != protocol.TypeHello {
		return
	}
	var hello protocol.Hello
	if err := protocol.UnmarshalControl(frame.Payload, &hello); err != nil || hello.SessionID == "" || len(hello.SessionID) > 128 || hello.MTU < 576 || hello.MTU > 1500 {
		return
	}

	welcomePayload, err := protocol.MarshalControl(protocol.Welcome{Address: id.addr.String(), Gateway: s.cfg.Gateway, MTU: min(s.cfg.MTU, hello.MTU), SessionID: hello.SessionID})
	if err != nil {
		s.log.Error("encode welcome payload", "error", err)
		return
	}
	welcome, err := protocol.Encode(protocol.TypeWelcome, 0, welcomePayload)
	if err != nil {
		s.log.Error("encode welcome frame", "error", err)
		return
	}
	writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
	err = conn.Write(writeCtx, websocket.MessageBinary, welcome)
	writeCancel()
	if err != nil {
		return
	}

	peerCtx, peerCancel := context.WithCancel(ctx)
	defer peerCancel()
	peer := &core.Peer{Name: id.name, Addr: id.addr, Send: make(chan []byte, 256), Cancel: peerCancel}
	if !s.hub.Register(peer) {
		_ = conn.Close(websocket.StatusTryAgainLater, "capacity reached")
		return
	}
	defer s.hub.Unregister(peer)
	s.log.Info("client connected", "client", id.name, "address", id.addr.String())
	defer s.log.Info("client disconnected", "client", id.name, "address", id.addr.String())

	writeErr := make(chan error, 1)
	go func() { writeErr <- s.writeLoop(peerCtx, conn, peer.Send) }()
	for {
		select {
		case <-peerCtx.Done():
			return
		case <-writeErr:
			return
		default:
		}
		readCtx, readCancel := context.WithTimeout(peerCtx, s.cfg.Idle.Duration)
		typ, raw, err = conn.Read(readCtx)
		readCancel()
		if err != nil || typ != websocket.MessageBinary {
			return
		}
		frame, err = protocol.Decode(raw)
		if err != nil || frame.Type != protocol.TypePacket {
			s.metrics.Invalid.Add(1)
			return
		}
		src, _, err := protocol.IPv4SourceDestination(frame.Payload)
		if err != nil || src != id.addr || len(frame.Payload) > s.cfg.MTU {
			s.metrics.Invalid.Add(1)
			return
		}
		s.writeMu.Lock()
		err = s.dev.WritePacket(frame.Payload)
		s.writeMu.Unlock()
		if err != nil {
			return
		}
		s.metrics.PacketsToTun.Add(1)
		s.metrics.BytesToTun.Add(int64(len(frame.Payload)))
	}
}

func (s *Server) writeLoop(ctx context.Context, conn *websocket.Conn, packets <-chan []byte) error {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case packet := <-packets:
			frame, err := protocol.Encode(protocol.TypePacket, 0, packet)
			if err != nil {
				return err
			}
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = conn.Write(writeCtx, websocket.MessageBinary, frame)
			cancel()
			if err != nil {
				return err
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := conn.Ping(pingCtx)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

func (s *Server) authenticate(r *http.Request) (identity, bool) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return identity{}, false
	}
	token := strings.TrimPrefix(header, "Bearer ")
	sum := sha256.Sum256([]byte(token))
	id, ok := s.ident[sum]
	return id, ok
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }
func (s *Server) readiness(w http.ResponseWriter, _ *http.Request) {
	select {
	case <-s.ready:
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "not ready", http.StatusServiceUnavailable)
	}
}

func decoy(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte("<!doctype html><html lang=en><meta charset=utf-8><meta name=viewport content='width=device-width'><title>Welcome</title><body><h1>Welcome</h1><p>Service is online.</p></body></html>"))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
		next.ServeHTTP(w, r)
	})
}
