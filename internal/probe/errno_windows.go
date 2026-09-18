package probe

import "syscall"

const (
	wsaeConnAborted syscall.Errno = 10053
	wsaeConnReset   syscall.Errno = 10054
)

func isResetErrno(e syscall.Errno) bool {
	return e == wsaeConnReset || e == wsaeConnAborted
}
