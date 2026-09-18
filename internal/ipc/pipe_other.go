//go:build !windows

package ipc

import (
	"context"
	"net"
	"os"
)

const socketPath = "/run/fi.sock"

// Listen создаёт сокет службы.
func Listen() (net.Listener, error) {
	os.Remove(socketPath)
	l, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	return l, os.Chmod(socketPath, 0o666)
}

// Dial подключается к службе.
func Dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	return d.DialContext(ctx, "unix", socketPath)
}
