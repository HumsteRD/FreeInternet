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

// pipeBuffer — буфер канала в каждую сторону. Без него запись завершается, только когда
// получатель прочитал всё до байта, а декодер JSON останавливается на конце значения и не
// забирает перевод строки: при длине запроса 2^n+1 клиент ждал службу, а служба — клиента.
const pipeBuffer = 64 << 10

// Listen создаёт канал службы.
func Listen() (net.Listener, error) { return listen(PipeName) }

func listen(name string) (net.Listener, error) {
	return winio.ListenPipe(name, &winio.PipeConfig{
		SecurityDescriptor: pipeSDDL,
		InputBufferSize:    pipeBuffer,
		OutputBufferSize:   pipeBuffer,
	})
}

// Dial подключается к службе.
func Dial(ctx context.Context) (net.Conn, error) {
	return winio.DialPipeContext(ctx, PipeName)
}
