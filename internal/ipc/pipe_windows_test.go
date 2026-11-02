package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Microsoft/go-winio"
)

// Запросы и ответы длиной 2^n+1 байт вешали канал на 10 секунд и заканчивались ошибкой
// «The pipe has been ended», хотя служба уже выполнила команду.
func TestPipePowerOfTwoLengths(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\fi-test-%d`, os.Getpid())
	l, err := listen(name)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, func(_ context.Context, _ string, params json.RawMessage) (any, error) {
		var s string
		err := json.Unmarshal(params, &s)
		return s, err // ответ почти той же длины, что и запрос
	})

	const overhead = len(`{"method":"m","params":""}`)
	for _, n := range []int{64, 128, 256, 512, 1024, 4096, 32768} {
		// Диапазон такой, чтобы на 2^n+1 попали и запрос, и ответ: ответ на десяток байт короче.
		for length := n - 2; length <= n+16; length++ {
			callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
			conn, err := winio.DialPipeContext(callCtx, name)
			if err != nil {
				callCancel()
				t.Fatal(err)
			}
			pad := strings.Repeat("x", length-overhead)
			start := time.Now()
			var got string
			if err := Call(callCtx, conn, "m", pad, &got); err != nil || got != pad {
				t.Errorf("длина %d: %v", length, err)
			}
			if took := time.Since(start); took > 2*time.Second {
				t.Errorf("длина %d: ответ через %v", length, took)
			}
			callCancel()
		}
	}
}

// Служба прежней версии ещё может работать, когда окно уже обновилось: запрос окна не должен
// вешать её канал без буфера и на длине 2^n+1.
func TestPipeNoBufferOldService(t *testing.T) {
	name := fmt.Sprintf(`\\.\pipe\fi-test-old-%d`, os.Getpid())
	l, err := winio.ListenPipe(name, &winio.PipeConfig{SecurityDescriptor: pipeSDDL})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, func(context.Context, string, json.RawMessage) (any, error) { return "ok", nil })

	const overhead = len(`{"method":"m","params":""}`)
	for _, length := range []int{64, 65, 128, 129, 256, 257, 512, 513} {
		callCtx, callCancel := context.WithTimeout(ctx, 5*time.Second)
		conn, err := winio.DialPipeContext(callCtx, name)
		if err != nil {
			callCancel()
			t.Fatal(err)
		}
		start := time.Now()
		var got string
		if err := Call(callCtx, conn, "m", strings.Repeat("x", length-overhead), &got); err != nil || got != "ok" {
			t.Errorf("длина %d: %q, %v", length, got, err)
		}
		if took := time.Since(start); took > 2*time.Second {
			t.Errorf("длина %d: ответ через %v", length, took)
		}
		callCancel()
	}
}
