package probe

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"os"
	"syscall"
)

// Классификация идёт по типам и кодам ошибок, а не по тексту:
// на русской Windows системные сообщения локализованы.

func isTimeout(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, os.ErrDeadlineExceeded) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// isReset — соединение закрыто собеседником или посредником (RST или преждевременный FIN).
func isReset(err error) bool {
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var errno syscall.Errno
	return errors.As(err, &errno) && isResetErrno(errno)
}

func isCertError(err error) bool {
	var (
		hostErr    x509.HostnameError
		unknownErr x509.UnknownAuthorityError
		invalidErr x509.CertificateInvalidError
	)
	return errors.As(err, &hostErr) || errors.As(err, &unknownErr) || errors.As(err, &invalidErr)
}

// stageError — ошибка с уже определённым статусом.
type stageError struct {
	status Status
	err    error
}

func (e *stageError) Error() string { return e.err.Error() }
func (e *stageError) Unwrap() error { return e.err }

func (r *Result) fail(err error) {
	var se *stageError
	if errors.As(err, &se) {
		r.Status, r.Detail = se.status, se.err.Error()
		return
	}
	r.Status, r.Detail = Failed, err.Error()
}
