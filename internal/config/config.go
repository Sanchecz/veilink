package config

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
	"veilink/internal/auth"
)

type Server struct {
	Listen        string         `yaml:"listen"`
	MetricsListen string         `yaml:"metrics_listen"`
	TunnelPath    string         `yaml:"tunnel_path"`
	Network       string         `yaml:"network"`
	Gateway       string         `yaml:"gateway"`
	Interface     string         `yaml:"interface"`
	MTU           int            `yaml:"mtu"`
	Handshake     Duration       `yaml:"handshake_timeout"`
	Idle          Duration       `yaml:"idle_timeout"`
	Shutdown      Duration       `yaml:"shutdown_timeout"`
	MaxClients    int            `yaml:"max_clients"`
	Clients       []ServerClient `yaml:"clients"`
	Log           Log            `yaml:"log"`
}

type ServerClient struct {
	Name        string `yaml:"name"`
	Address     string `yaml:"address"`
	TokenSHA256 string `yaml:"token_sha256"`
}

type Client struct {
	ServerURL   string   `yaml:"server_url"`
	Token       string   `yaml:"token"`
	Name        string   `yaml:"name"`
	Interface   string   `yaml:"interface"`
	MTU         int      `yaml:"mtu"`
	DialTimeout Duration `yaml:"dial_timeout"`
	KeepAlive   Duration `yaml:"keepalive"`
	Reconnect   Duration `yaml:"reconnect"`
	DNS         []string `yaml:"dns"`
	BlockIPv6   bool     `yaml:"block_ipv6"`
	Log         Log      `yaml:"log"`
}

type Log struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type Duration struct{ time.Duration }

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	v, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalYAML() (any, error) { return d.String(), nil }

func LoadServer(path string) (Server, error) {
	var cfg Server
	if err := loadStrict(path, &cfg, 0o640); err != nil {
		return cfg, err
	}
	applyServerDefaults(&cfg)
	return cfg, cfg.Validate()
}

func LoadClient(path string) (Client, error) {
	var cfg Client
	if err := loadStrict(path, &cfg, 0o600); err != nil {
		return cfg, err
	}
	applyClientDefaults(&cfg)
	return cfg, cfg.Validate()
}

func loadStrict(path string, out any, maxPerm os.FileMode) error {
	clean := filepath.Clean(path)
	f, err := os.Open(clean)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	defer f.Close()
	if runtime.GOOS != "windows" {
		info, statErr := f.Stat()
		if statErr != nil {
			return fmt.Errorf("stat config: %w", statErr)
		}
		if info.Mode().Perm()&^maxPerm != 0 {
			return fmt.Errorf("config permissions %04o are too broad; maximum is %04o", info.Mode().Perm(), maxPerm)
		}
	}
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("decode config: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("config must contain exactly one YAML document")
	}
	return nil
}

func applyServerDefaults(c *Server) {
	if c.Listen == "" {
		c.Listen = "127.0.0.1:8080"
	}
	if c.MetricsListen == "" {
		c.MetricsListen = "127.0.0.1:9090"
	}
	if c.TunnelPath == "" {
		c.TunnelPath = "/assets/v1/stream"
	}
	if c.Network == "" {
		c.Network = "10.77.0.0/24"
	}
	if c.Gateway == "" {
		c.Gateway = "10.77.0.1"
	}
	if c.Interface == "" {
		c.Interface = "veilink0"
	}
	if c.MTU == 0 {
		c.MTU = 1280
	}
	if c.Handshake.Duration == 0 {
		c.Handshake.Duration = 10 * time.Second
	}
	if c.Idle.Duration == 0 {
		c.Idle.Duration = 90 * time.Second
	}
	if c.Shutdown.Duration == 0 {
		c.Shutdown.Duration = 15 * time.Second
	}
	if c.MaxClients == 0 {
		c.MaxClients = 256
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
}

func applyClientDefaults(c *Client) {
	if c.Name == "" {
		c.Name = "client"
	}
	if c.Interface == "" {
		c.Interface = "veilink0"
	}
	if c.MTU == 0 {
		c.MTU = 1280
	}
	if c.DialTimeout.Duration == 0 {
		c.DialTimeout.Duration = 15 * time.Second
	}
	if c.KeepAlive.Duration == 0 {
		c.KeepAlive.Duration = 25 * time.Second
	}
	if c.Reconnect.Duration == 0 {
		c.Reconnect.Duration = 3 * time.Second
	}
	if c.Log.Level == "" {
		c.Log.Level = "info"
	}
	if c.Log.Format == "" {
		c.Log.Format = "json"
	}
}

func (c Server) Validate() error {
	var errs []error
	if err := validateListen(c.Listen); err != nil {
		errs = append(errs, fmt.Errorf("listen: %w", err))
	}
	if err := validateListen(c.MetricsListen); err != nil {
		errs = append(errs, fmt.Errorf("metrics_listen: %w", err))
	}
	if !strings.HasPrefix(c.TunnelPath, "/") || c.TunnelPath == "/" || strings.HasSuffix(c.TunnelPath, "/") || c.TunnelPath == "/healthz" || c.TunnelPath == "/readyz" || strings.ContainsAny(c.TunnelPath, "?#") {
		errs = append(errs, errors.New("tunnel_path must be an absolute URL path without query or fragment"))
	}
	prefix, err := netip.ParsePrefix(c.Network)
	if err != nil || !prefix.Addr().Is4() || !prefix.Addr().IsPrivate() || prefix.Bits() < 16 || prefix.Bits() > 30 || prefix != prefix.Masked() {
		errs = append(errs, errors.New("network must be a canonical private IPv4 /16 to /30 prefix"))
	}
	gateway, err := netip.ParseAddr(c.Gateway)
	if err != nil || !gateway.Is4() || (prefix.IsValid() && !prefix.Contains(gateway)) {
		errs = append(errs, errors.New("gateway must be an IPv4 address inside network"))
	}
	if c.MTU < 576 || c.MTU > 1500 {
		errs = append(errs, errors.New("mtu must be between 576 and 1500"))
	}
	if !validInterfaceName(c.Interface) {
		errs = append(errs, errors.New("interface contains unsupported characters"))
	}
	if c.MaxClients < 1 || c.MaxClients > 65535 {
		errs = append(errs, errors.New("max_clients must be between 1 and 65535"))
	}
	if c.Handshake.Duration < time.Second || c.Idle.Duration < 10*time.Second {
		errs = append(errs, errors.New("timeouts are unreasonably short"))
	}
	seenNames, seenIPs, seenHashes := map[string]bool{}, map[netip.Addr]bool{}, map[[32]byte]bool{}
	for i, client := range c.Clients {
		if client.Name == "" || seenNames[client.Name] {
			errs = append(errs, fmt.Errorf("clients[%d].name is empty or duplicated", i))
		}
		seenNames[client.Name] = true
		ip, ipErr := netip.ParseAddr(client.Address)
		if ipErr != nil || !ip.Is4() || (prefix.IsValid() && (!prefix.Contains(ip) || isNetworkOrBroadcast(prefix, ip))) || ip == gateway || seenIPs[ip] {
			errs = append(errs, fmt.Errorf("clients[%d].address is invalid, reserved, or duplicated", i))
		}
		seenIPs[ip] = true
		h, hashErr := auth.ParseHash(client.TokenSHA256)
		if hashErr != nil || seenHashes[h] {
			errs = append(errs, fmt.Errorf("clients[%d].token_sha256 is invalid or duplicated", i))
		}
		seenHashes[h] = true
	}
	if len(c.Clients) == 0 {
		errs = append(errs, errors.New("at least one client must be configured"))
	}
	return errors.Join(errs...)
}

func (c Client) Validate() error {
	var errs []error
	u, err := url.Parse(c.ServerURL)
	if err != nil || u.Scheme != "wss" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path == "" || u.Path == "/" || strings.HasSuffix(u.Path, "/") {
		errs = append(errs, errors.New("server_url must be a clean wss:// URL"))
	}
	if err := auth.Validate(c.Token); err != nil {
		errs = append(errs, fmt.Errorf("token: %w", err))
	}
	if c.MTU < 576 || c.MTU > 1500 {
		errs = append(errs, errors.New("mtu must be between 576 and 1500"))
	}
	if !validInterfaceName(c.Interface) {
		errs = append(errs, errors.New("interface contains unsupported characters"))
	}
	if c.DialTimeout.Duration < time.Second || c.KeepAlive.Duration < 5*time.Second || c.Reconnect.Duration < time.Second {
		errs = append(errs, errors.New("timeouts are unreasonably short"))
	}
	for _, s := range c.DNS {
		ip, parseErr := netip.ParseAddr(s)
		if parseErr != nil || !ip.Is4() {
			errs = append(errs, fmt.Errorf("dns %q must be an IPv4 address", s))
		}
	}
	return errors.Join(errs...)
}

func validInterfaceName(s string) bool {
	if len(s) < 1 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("._- ", r) {
			continue
		}
		return false
	}
	return true
}

func validateListen(s string) error {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return err
	}
	if port == "" {
		return errors.New("port is required")
	}
	if host == "localhost" {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("listener must use a loopback address; terminate TLS at the local reverse proxy")
	}
	return nil
}

func isNetworkOrBroadcast(prefix netip.Prefix, ip netip.Addr) bool {
	if !prefix.IsValid() || !ip.Is4() {
		return false
	}
	first := prefix.Masked().Addr().As4()
	last := first
	hostBits := 32 - prefix.Bits()
	mask := uint32(1<<hostBits) - 1
	v := uint32(first[0])<<24 | uint32(first[1])<<16 | uint32(first[2])<<8 | uint32(first[3])
	v |= mask
	binary.BigEndian.PutUint32(last[:], v)
	return ip == netip.AddrFrom4(first) || ip == netip.AddrFrom4(last)
}
