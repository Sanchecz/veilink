package platform

import (
	"context"
	"net/netip"
)

type ClientOptions struct {
	Interface string
	Address   netip.Addr
	Gateway   netip.Addr
	ServerIP  netip.Addr
	MTU       int
	DNS       []netip.Addr
	BlockIPv6 bool
}

type ServerOptions struct {
	Interface string
	Gateway   netip.Addr
	Prefix    netip.Prefix
	MTU       int
}

type Cleanup func(context.Context) error
