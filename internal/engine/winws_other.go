//go:build !windows

package engine

import (
	"context"
	"errors"
	"os"
)

var errUnsupported = errors.New("движок пока поддерживается только в Windows")

type Process struct{}

func Start(ctx context.Context, exe string, args []string) (*Process, error) {
	return nil, errUnsupported
}

func (p *Process) Stop() error { return nil }

func (p *Process) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

func (p *Process) Err() error { return errUnsupported }

func (p *Process) PID() uint32 { return 0 }

func IsElevated() bool { return os.Geteuid() == 0 }

func Conflicts() ([]string, error) { return nil, nil }
