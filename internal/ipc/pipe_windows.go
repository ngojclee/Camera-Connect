//go:build windows

package ipc

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

func listenPipe(pipeName string) (net.Listener, error) {
	return winio.ListenPipe(pipeName, nil)
}

func dialPipe(ctx context.Context, pipeName string) (net.Conn, error) {
	return winio.DialPipeContext(ctx, pipeName)
}
