package ipc

import (
	"context"
	"net"

	"github.com/Microsoft/go-winio"
)

const PipeName = `\\.\pipe\fi`

// pipeSDDL: система и администраторы — полный доступ, вошедшие в систему пользователи —
// чтение и запись, чтобы окно в трее работало без прав администратора.
const pipeSDDL = "D:P(A;;GA;;;SY)(A;;GA;;;BA)(A;;GRGW;;;IU)"

// Listen создаёт канал службы.
func Listen() (net.Listener, error) {
	return winio.ListenPipe(PipeName, &winio.PipeConfig{SecurityDescriptor: pipeSDDL})
}

// Dial подключается к службе.
func Dial(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeContext(ctx, PipeName)
}
