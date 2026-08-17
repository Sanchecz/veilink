//go:build linux

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type linuxRoute struct {
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
}

func SetupClient(ctx context.Context, o ClientOptions) (Cleanup, error) {
	orig, err := linuxDefaultRoute(ctx, o.Interface)
	if err != nil {
		return nil, err
	}
	var completed []func(context.Context) error
	rollback := func(cleanCtx context.Context) error {
		var errs []error
		for i := len(completed) - 1; i >= 0; i-- {
			errs = append(errs, completed[i](cleanCtx))
		}
		return errors.Join(errs...)
	}
	fail := func(err error) (Cleanup, error) { _ = rollback(context.Background()); return nil, err }

	if err := run(ctx, "ip", "addr", "replace", o.Address.String()+"/32", "dev", o.Interface); err != nil {
		return fail(err)
	}
	completed = append(completed, func(c context.Context) error {
		return runIgnoreMissing(c, "ip", "addr", "del", o.Address.String()+"/32", "dev", o.Interface)
	})
	if err := run(ctx, "ip", "link", "set", "dev", o.Interface, "mtu", strconv.Itoa(o.MTU), "up"); err != nil {
		return fail(err)
	}
	if err := run(ctx, "ip", "route", "replace", o.ServerIP.String()+"/32", "via", orig.Gateway, "dev", orig.Dev); err != nil {
		return fail(err)
	}
	completed = append(completed, func(c context.Context) error {
		return runIgnoreMissing(c, "ip", "route", "del", o.ServerIP.String()+"/32", "via", orig.Gateway, "dev", orig.Dev)
	})
	if err := run(ctx, "ip", "route", "replace", o.Gateway.String()+"/32", "dev", o.Interface, "scope", "link"); err != nil {
		return fail(err)
	}
	completed = append(completed, func(c context.Context) error {
		return runIgnoreMissing(c, "ip", "route", "del", o.Gateway.String()+"/32", "dev", o.Interface)
	})
	for _, prefix := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		if err := run(ctx, "ip", "route", "replace", prefix, "via", o.Gateway.String(), "dev", o.Interface); err != nil {
			return fail(err)
		}
		p := prefix
		completed = append(completed, func(c context.Context) error {
			return runIgnoreMissing(c, "ip", "route", "del", p, "via", o.Gateway.String(), "dev", o.Interface)
		})
	}
	if o.BlockIPv6 {
		for _, prefix := range []string{"::/1", "8000::/1"} {
			if err := run(ctx, "ip", "-6", "route", "replace", prefix, "dev", o.Interface, "metric", "5"); err != nil {
				return fail(err)
			}
			p := prefix
			completed = append(completed, func(c context.Context) error {
				return runIgnoreMissing(c, "ip", "-6", "route", "del", p, "dev", o.Interface)
			})
		}
	}
	if len(o.DNS) > 0 {
		if _, err := exec.LookPath("resolvectl"); err != nil {
			return fail(errors.New("DNS was requested but resolvectl is unavailable"))
		}
		args := []string{"dns", o.Interface}
		for _, ip := range o.DNS {
			args = append(args, ip.String())
		}
		if err := run(ctx, "resolvectl", args...); err != nil {
			return fail(err)
		}
		if err := run(ctx, "resolvectl", "domain", o.Interface, "~."); err != nil {
			return fail(err)
		}
		completed = append(completed, func(c context.Context) error { return runIgnoreMissing(c, "resolvectl", "revert", o.Interface) })
	}
	return rollback, nil
}

func linuxDefaultRoute(ctx context.Context, exclude string) (linuxRoute, error) {
	out, err := exec.CommandContext(ctx, "ip", "-j", "-4", "route", "show", "default").Output()
	if err != nil {
		return linuxRoute{}, fmt.Errorf("read default route: %w", err)
	}
	var routes []linuxRoute
	if err := json.Unmarshal(out, &routes); err != nil {
		return linuxRoute{}, fmt.Errorf("decode default route: %w", err)
	}
	for _, r := range routes {
		if r.Dev != exclude && r.Dev != "" && r.Gateway != "" {
			return r, nil
		}
	}
	return linuxRoute{}, errors.New("no usable IPv4 default route found")
}

func SetupServer(ctx context.Context, o ServerOptions) (Cleanup, error) {
	if err := run(ctx, "ip", "addr", "replace", o.Gateway.String()+"/"+strconv.Itoa(o.Prefix.Bits()), "dev", o.Interface); err != nil {
		return nil, err
	}
	cleanup := func(c context.Context) error {
		return runIgnoreMissing(c, "ip", "addr", "del", o.Gateway.String()+"/"+strconv.Itoa(o.Prefix.Bits()), "dev", o.Interface)
	}
	if err := run(ctx, "ip", "link", "set", "dev", o.Interface, "mtu", strconv.Itoa(o.MTU), "up"); err != nil {
		_ = cleanup(context.Background())
		return nil, err
	}
	out, err := exec.CommandContext(ctx, "sysctl", "-n", "net.ipv4.ip_forward").Output()
	if err != nil || strings.TrimSpace(string(out)) != "1" {
		_ = cleanup(context.Background())
		return nil, errors.New("net.ipv4.ip_forward must be 1; enable it in the deployment sysctl file")
	}
	return cleanup, nil
}

func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v: %w: %s", name, args, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func runIgnoreMissing(ctx context.Context, name string, args ...string) error {
	_ = run(ctx, name, args...)
	return nil
}
