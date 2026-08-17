package client

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
	"veilink/internal/config"
	"veilink/internal/device"
	"veilink/internal/platform"
	"veilink/internal/protocol"
)

type opener func(string, int) (device.Device, error)
type setupFunc func(context.Context, platform.ClientOptions) (platform.Cleanup, error)

type Client struct {
	cfg     config.Client
	log     *slog.Logger
	open    opener
	setup   setupFunc
	rootCAs *x509.CertPool
}

func New(cfg config.Client, logger *slog.Logger) *Client {
	return &Client{cfg: cfg, log: logger, open: device.Open, setup: platform.SetupClient}
}

func (c *Client) Run(ctx context.Context) (runErr error) {
	serverIP, err := resolveServerIPv4(ctx, c.cfg.ServerURL)
	if err != nil {
		return err
	}
	var dev device.Device
	var cleanup platform.Cleanup
	var assigned, gateway netip.Addr
	var effectiveMTU int
	outbound := make(chan []byte, 512)
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if cleanup != nil {
			if err := cleanup(cleanCtx); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("clean up routes: %w", err))
			}
		}
		if dev != nil {
			if err := dev.Close(); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("close TUN: %w", err))
			}
		}
	}()

	for {
		conn, welcome, err := c.connect(ctx, serverIP)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.log.Warn("connection failed", "error", err, "retry_in", c.cfg.Reconnect.Duration)
			if !sleepContext(ctx, c.cfg.Reconnect.Duration) {
				return nil
			}
			continue
		}
		newAssigned, err := netip.ParseAddr(welcome.Address)
		if err != nil || !newAssigned.Is4() {
			return closeNowWithError(conn, errors.New("server returned invalid client address"))
		}
		newGateway, err := netip.ParseAddr(welcome.Gateway)
		if err != nil || !newGateway.Is4() {
			return closeNowWithError(conn, errors.New("server returned invalid gateway"))
		}
		if dev == nil {
			assigned, gateway, effectiveMTU = newAssigned, newGateway, min(c.cfg.MTU, welcome.MTU)
			dev, err = c.open(c.cfg.Interface, effectiveMTU)
			if err != nil {
				return closeNowWithError(conn, err)
			}
			name, err := dev.Name()
			if err != nil {
				return closeNowWithError(conn, fmt.Errorf("read TUN name: %w", err))
			}
			dns := make([]netip.Addr, 0, len(c.cfg.DNS))
			for _, raw := range c.cfg.DNS {
				ip, _ := netip.ParseAddr(raw)
				dns = append(dns, ip)
			}
			cleanup, err = c.setup(ctx, platform.ClientOptions{Interface: name, Address: assigned, Gateway: gateway, ServerIP: serverIP, MTU: effectiveMTU, DNS: dns, BlockIPv6: c.cfg.BlockIPv6})
			if err != nil {
				return closeNowWithError(conn, err)
			}
			go c.readTun(ctx, dev, outbound, assigned, effectiveMTU)
		} else if newAssigned != assigned || newGateway != gateway || min(c.cfg.MTU, welcome.MTU) != effectiveMTU {
			return closeNowWithError(conn, errors.New("server changed tunnel parameters during reconnect"))
		}
		c.log.Info("tunnel connected", "address", assigned.String(), "gateway", gateway.String(), "mtu", effectiveMTU)
		err = c.runSession(ctx, conn, dev, outbound, assigned, effectiveMTU)
		closeErr := conn.CloseNow()
		if ctx.Err() != nil {
			if closeErr != nil {
				return fmt.Errorf("close websocket: %w", closeErr)
			}
			return nil
		}
		err = errors.Join(err, closeErr)
		c.log.Warn("tunnel disconnected", "error", err, "retry_in", c.cfg.Reconnect.Duration)
		if !sleepContext(ctx, c.cfg.Reconnect.Duration) {
			return nil
		}
	}
}

func (c *Client) connect(ctx context.Context, serverIP netip.Addr) (*websocket.Conn, protocol.Welcome, error) {
	u, _ := url.Parse(c.cfg.ServerURL)
	port := u.Port()
	if port == "" {
		port = "443"
	}
	dialer := &net.Dialer{Timeout: c.cfg.DialTimeout.Duration, KeepAlive: c.cfg.KeepAlive.Duration}
	expectedHost := u.Hostname()
	transport := &http.Transport{ForceAttemptHTTP2: false, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: c.rootCAs, NextProtos: []string{"http/1.1"}}, DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
		host, requestedPort, err := net.SplitHostPort(address)
		if err != nil || !strings.EqualFold(host, expectedHost) || requestedPort != port {
			return nil, fmt.Errorf("refusing redirected dial target %q", address)
		}
		return dialer.DialContext(dialCtx, "tcp4", net.JoinHostPort(serverIP.String(), port))
	}}
	defer transport.CloseIdleConnections()
	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.cfg.Token)
	header.Set("User-Agent", "Mozilla/5.0")
	dialCtx, cancel := context.WithTimeout(ctx, c.cfg.DialTimeout.Duration)
	defer cancel()
	httpClient := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("redirects are not allowed for the tunnel endpoint")
	}}
	conn, response, err := websocket.Dial(dialCtx, c.cfg.ServerURL, &websocket.DialOptions{HTTPClient: httpClient, HTTPHeader: header, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		if response != nil {
			return nil, protocol.Welcome{}, fmt.Errorf("dial tunnel: HTTP %d: %w", response.StatusCode, err)
		}
		return nil, protocol.Welcome{}, fmt.Errorf("dial tunnel: %w", err)
	}
	conn.SetReadLimit(protocol.MaxPacketSize + protocol.HeaderSize)
	sessionID, err := randomSessionID()
	if err != nil {
		return nil, protocol.Welcome{}, closeNowWithError(conn, err)
	}
	payload, err := protocol.MarshalControl(protocol.Hello{ClientName: c.cfg.Name, SessionID: sessionID, MTU: c.cfg.MTU})
	if err != nil {
		return nil, protocol.Welcome{}, closeNowWithError(conn, err)
	}
	frame, err := protocol.Encode(protocol.TypeHello, 0, payload)
	if err != nil {
		return nil, protocol.Welcome{}, closeNowWithError(conn, err)
	}
	if err := conn.Write(dialCtx, websocket.MessageBinary, frame); err != nil {
		return nil, protocol.Welcome{}, closeNowWithError(conn, err)
	}
	typ, raw, err := conn.Read(dialCtx)
	if err != nil {
		return nil, protocol.Welcome{}, closeNowWithError(conn, fmt.Errorf("read welcome: %w", err))
	}
	if typ != websocket.MessageBinary {
		return nil, protocol.Welcome{}, closeNowWithError(conn, errors.New("welcome is not a binary websocket message"))
	}
	decoded, err := protocol.Decode(raw)
	if err != nil || decoded.Type != protocol.TypeWelcome {
		return nil, protocol.Welcome{}, closeNowWithError(conn, errors.New("server returned invalid welcome frame"))
	}
	var welcome protocol.Welcome
	if err := protocol.UnmarshalControl(decoded.Payload, &welcome); err != nil {
		return nil, protocol.Welcome{}, closeNowWithError(conn, err)
	}
	if welcome.SessionID != sessionID || welcome.MTU < 576 || welcome.MTU > 1500 {
		return nil, protocol.Welcome{}, closeNowWithError(conn, errors.New("server returned inconsistent tunnel parameters"))
	}
	return conn, welcome, nil
}

func closeNowWithError(conn *websocket.Conn, cause error) error {
	if err := conn.CloseNow(); err != nil {
		return errors.Join(cause, fmt.Errorf("close websocket: %w", err))
	}
	return cause
}

func (c *Client) readTun(ctx context.Context, dev device.Device, outbound chan<- []byte, assigned netip.Addr, mtu int) {
	buf := make([]byte, protocol.MaxPacketSize)
	for {
		n, err := dev.ReadPacket(buf)
		if err != nil {
			return
		}
		if n > mtu {
			continue
		}
		src, _, err := protocol.IPv4SourceDestination(buf[:n])
		if err != nil || src != assigned {
			continue
		}
		packet := append([]byte(nil), buf[:n]...)
		select {
		case outbound <- packet:
		case <-ctx.Done():
			return
		}
	}
}

func (c *Client) runSession(ctx context.Context, conn *websocket.Conn, dev device.Device, outbound <-chan []byte, assigned netip.Addr, mtu int) error {
	sessionCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 2)
	go func() {
		ticker := time.NewTicker(c.cfg.KeepAlive.Duration)
		defer ticker.Stop()
		for {
			select {
			case <-sessionCtx.Done():
				errCh <- sessionCtx.Err()
				return
			case packet := <-outbound:
				frame, encodeErr := protocol.Encode(protocol.TypePacket, 0, packet)
				if encodeErr != nil {
					errCh <- encodeErr
					return
				}
				writeCtx, writeCancel := context.WithTimeout(sessionCtx, 10*time.Second)
				err := conn.Write(writeCtx, websocket.MessageBinary, frame)
				writeCancel()
				if err != nil {
					errCh <- err
					return
				}
			case <-ticker.C:
				pingCtx, pingCancel := context.WithTimeout(sessionCtx, 10*time.Second)
				err := conn.Ping(pingCtx)
				pingCancel()
				if err != nil {
					errCh <- err
					return
				}
			}
		}
	}()
	go func() {
		for {
			readCtx, readCancel := context.WithTimeout(sessionCtx, 90*time.Second)
			typ, raw, err := conn.Read(readCtx)
			readCancel()
			if err != nil {
				errCh <- err
				return
			}
			if typ != websocket.MessageBinary {
				errCh <- errors.New("unexpected websocket message type")
				return
			}
			frame, err := protocol.Decode(raw)
			if err != nil || frame.Type != protocol.TypePacket || len(frame.Payload) > mtu {
				errCh <- errors.New("invalid packet frame")
				return
			}
			_, dst, err := protocol.IPv4SourceDestination(frame.Payload)
			if err != nil || dst != assigned {
				errCh <- errors.New("server packet destination does not match assigned address")
				return
			}
			if err := dev.WritePacket(frame.Payload); err != nil {
				errCh <- err
				return
			}
		}
	}()
	err := <-errCh
	cancel()
	return err
}

func resolveServerIPv4(ctx context.Context, rawURL string) (netip.Addr, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return netip.Addr{}, err
	}
	if ip, err := netip.ParseAddr(u.Hostname()); err == nil && ip.Is4() {
		return ip, nil
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(lookupCtx, "ip4", u.Hostname())
	if err != nil || len(addrs) == 0 {
		return netip.Addr{}, fmt.Errorf("resolve server IPv4 address: %w", err)
	}
	return addrs[0].Unmap(), nil
}

func randomSessionID() (string, error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
