//go:build !linux && !windows

package platform

import (
	"context"
	"errors"
)

func SetupClient(context.Context, ClientOptions) (Cleanup, error) {
	return nil, errors.New("client route management is currently supported on Linux and Windows only")
}

func SetupServer(context.Context, ServerOptions) (Cleanup, error) {
	return nil, errors.New("server mode is supported on Linux VDS only")
}
