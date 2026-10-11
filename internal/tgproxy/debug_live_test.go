//go:build live

package tgproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveWorkerDebug(t *testing.T) {
	worker := os.Getenv("FI_WORKER")
	s := &Server{Worker: worker}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ver, err := WorkerVersion(ctx, http.DefaultClient, worker)
	t.Logf("версия воркера: %d, %v", ver, err)
	if req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+worker+"/?check=telegram.org", nil); err == nil {
		if resp, err := http.DefaultClient.Do(req); err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			t.Logf("проверка воркером telegram.org: HTTP %d\n%s", resp.StatusCode, body)
		}
	}
	for _, target := range []struct {
		host string
		port int
	}{{"telegram.org", 443}, {"t.me", 443}} {
		conn, err := s.dialWorkerTCP(ctx, target.host, target.port)
		if err != nil {
			t.Logf("%s: dial: %v", target.host, err)
			continue
		}
		ws := conn.(*wsStream).ws
		// Отправим ClientHello и посмотрим, что придёт первым кадром.
		go func() {
			tc := tls.Client(conn, &tls.Config{ServerName: target.host})
			tc.HandshakeContext(ctx)
		}()
		ws.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
		for i := 0; i < 3; i++ {
			fin, op, payload, err := ws.readFrame()
			if err != nil {
				t.Logf("%s: кадр %d: ошибка %v", target.host, i, err)
				break
			}
			t.Logf("%s: кадр %d: fin=%v op=%d len=%d head=%q", target.host, i, fin, op, len(payload), payload[:min(len(payload), 40)])
			if op == opClose {
				break
			}
		}
		conn.Close()
	}
}
