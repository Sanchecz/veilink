package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/netip"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"veilink/internal/auth"
	"veilink/internal/client"
	"veilink/internal/config"
	"veilink/internal/device"
	"veilink/internal/logging"
	"veilink/internal/platform"
	"veilink/internal/server"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a command is required")
	}
	switch args[0] {
	case "server":
		return runServer(args[1:])
	case "client":
		return runClient(args[1:])
	case "validate":
		return runValidate(args[1:])
	case "token":
		return runToken(args[1:])
	case "version":
		fmt.Printf("veilink %s (commit=%s date=%s go=%s os=%s arch=%s)\n", version, commit, date, runtime.Version(), runtime.GOOS, runtime.GOARCH)
		return nil
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("server", flag.ContinueOnError)
	path := fs.String("config", "/etc/veilink/server.yaml", "server configuration path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadServer(*path)
	if err != nil {
		return err
	}
	logger, err := logging.New(cfg.Log, os.Stdout)
	if err != nil {
		return err
	}
	dev, err := device.Open(cfg.Interface, cfg.MTU)
	if err != nil {
		return err
	}
	prefix, _ := netip.ParsePrefix(cfg.Network)
	gateway, _ := netip.ParseAddr(cfg.Gateway)
	setupCtx, setupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	cleanup, err := platform.SetupServer(setupCtx, platform.ServerOptions{Interface: cfg.Interface, Gateway: gateway, Prefix: prefix, MTU: cfg.MTU})
	setupCancel()
	if err != nil {
		_ = dev.Close()
		return err
	}
	defer func() {
		cleanCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = cleanup(cleanCtx)
	}()
	srv, err := server.New(cfg, dev, logger.With("component", "server"))
	if err != nil {
		_ = dev.Close()
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("starting Veilink server", "version", version, "interface", cfg.Interface)
	return srv.Run(ctx)
}

func runClient(args []string) error {
	fs := flag.NewFlagSet("client", flag.ContinueOnError)
	path := fs.String("config", defaultClientConfig(), "client configuration path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.LoadClient(*path)
	if err != nil {
		return err
	}
	logger, err := logging.New(cfg.Log, os.Stdout)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger.Info("starting Veilink client", "version", version, "interface", cfg.Interface)
	return client.New(cfg, logger.With("component", "client")).Run(ctx)
}

func runValidate(args []string) error {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	kind := fs.String("type", "", "configuration type: server or client")
	path := fs.String("config", "", "configuration path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("--config is required")
	}
	switch *kind {
	case "server":
		_, err := config.LoadServer(*path)
		if err != nil {
			return err
		}
	case "client":
		_, err := config.LoadClient(*path)
		if err != nil {
			return err
		}
	default:
		return errors.New("--type must be server or client")
	}
	fmt.Println("configuration is valid")
	return nil
}

func runToken(args []string) error {
	fs := flag.NewFlagSet("token", flag.ContinueOnError)
	jsonOutput := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	token, err := auth.Generate()
	if err != nil {
		return err
	}
	hash, err := auth.Hash(token)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(map[string]string{"token": token, "token_sha256": hash})
	}
	fmt.Println("token:", token)
	fmt.Println("token_sha256:", hash)
	fmt.Fprintln(os.Stderr, "Store the token in the client config and only token_sha256 on the server.")
	return nil
}

func defaultClientConfig() string {
	if runtime.GOOS == "windows" {
		if dir, err := os.UserConfigDir(); err == nil {
			return dir + `\Veilink\client.yaml`
		}
	}
	return "/etc/veilink/client.yaml"
}

func usage() {
	fmt.Fprintln(os.Stderr, `Veilink - TLS-protected full-tunnel VPN

Usage:
  veilink server   --config PATH
  veilink client   --config PATH
  veilink validate --type server|client --config PATH
  veilink token    [--json]
  veilink version`)
}
