//go:build !windows

package ipc

import (
	"context"
	"fmt"
	"net"
)

func listenPipe(pipeName string) (net.Listener, error) {
	return nil, fmt.Errorf("named pipes are only supported on windows: %s", pipeName)
}

func dialPipe(ctx context.Context, pipeName string) (net.Conn, error) {
	return nil, fmt.Errorf("named pipes are only supported on windows: %s", pipeName)
}
