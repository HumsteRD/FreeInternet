//go:build !windows

package probe

import "syscall"

func isResetErrno(e syscall.Errno) bool {
	return e == syscall.ECONNRESET || e == syscall.ECONNABORTED
}
