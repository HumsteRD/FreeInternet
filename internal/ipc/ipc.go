// Package ipc — обмен между службой и окном: на каждое соединение один JSON-запрос
// и один ответ. Долгие операции служба выполняет в фоне, а окно опрашивает статус.
package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"time"
)

type Request struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// Handler выполняет метод; ошибка уходит клиенту текстом.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

// RemoteError — ошибка, которую вернула служба.
type RemoteError struct{ Message string }

func (e *RemoteError) Error() string { return e.Message }

const (
	maxRequest = 1 << 20
	ioTimeout  = 10 * time.Second
)

// Serve принимает соединения, пока не отменён ctx.
func Serve(ctx context.Context, l net.Listener, h Handler) error {
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go serveConn(ctx, conn, h)
	}
}

func serveConn(ctx context.Context, conn net.Conn, h Handler) {
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(ioTimeout))
	var req Request
	if err := json.NewDecoder(io.LimitReader(conn, maxRequest)).Decode(&req); err != nil {
		writeResponse(conn, nil, fmt.Errorf("неверный запрос: %w", err))
		return
	}
	result, err := h(ctx, req.Method, req.Params)
	writeResponse(conn, result, err)
}

func writeResponse(conn net.Conn, result any, err error) {
	var resp Response
	if err != nil {
		resp.Error = err.Error()
	} else if result != nil {
		data, merr := json.Marshal(result)
		if merr != nil {
			resp.Error = merr.Error()
		} else {
			resp.Result = data
		}
	}
	conn.SetWriteDeadline(time.Now().Add(ioTimeout))
	json.NewEncoder(conn).Encode(resp)
}

// Call отправляет запрос по conn, закрывает соединение и декодирует результат в result.
func Call(ctx context.Context, conn net.Conn, method string, params, result any) error {
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}

	req := Request{Method: method}
	if params != nil {
		data, err := json.Marshal(params)
		if err != nil {
			return err
		}
		req.Params = data
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return err
	}
	if resp.Error != "" {
		return &RemoteError{resp.Error}
	}
	if result != nil && len(resp.Result) > 0 {
		return json.Unmarshal(resp.Result, result)
	}
	return nil
}
