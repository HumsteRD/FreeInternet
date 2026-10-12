//go:build live

// Проверка настоящего Cloudflare Worker пользователя:
//
//	$env:FI_WORKER="fi-tg.логин.workers.dev"; go test -tags live -run LiveWorker -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func liveWorker(t *testing.T) string {
	t.Helper()
	worker := os.Getenv("FI_WORKER")
	if worker == "" {
		t.Skip("FI_WORKER не задан")
	}
	return worker
}

// TestLiveWorkerVersion: воркер отвечает и его код не устарел.
func TestLiveWorkerVersion(t *testing.T) {
	worker := liveWorker(t)
	ver, err := WorkerVersion(context.Background(), http.DefaultClient, worker)
	if err != nil {
		t.Fatal(err)
	}
	if ver < WorkerVersionWeb {
		t.Fatalf("версия воркера %d, нужна %d", ver, WorkerVersionWeb)
	}
	t.Logf("версия воркера: %d", ver)
}

// TestLiveWorkerEcho: в каком виде воркер получает двоичные сообщения (Cloudflare отдаёт Blob).
func TestLiveWorkerEcho(t *testing.T) {
	worker := liveWorker(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ws, err := dialWS(ctx, net.JoinHostPort(worker, "443"), worker, "/?echo=1")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.close()
	ws.send([]byte{0x16, 0x03, 0x01, 0x00, 0xff})
	ws.conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	_, op, payload, err := ws.readFrame()
	t.Logf("ответ воркера: op=%d %q %v", op, payload, err)
}

// TestLiveWorkerWebTLS: через воркер проходит TLS с сайтами Telegram для мини-приложений.
func TestLiveWorkerWebTLS(t *testing.T) {
	s := &Server{Worker: liveWorker(t)}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, host := range []string{"telegram.org", "t.me", "core.telegram.org", "telegra.ph"} {
		conn, err := s.dialWorkerTCP(ctx, host, 443)
		if err != nil {
			t.Errorf("%s: %v", host, err)
			continue
		}
		hctx, hcancel := context.WithTimeout(ctx, 8*time.Second)
		err = tls.Client(conn, &tls.Config{ServerName: host}).HandshakeContext(hctx)
		hcancel()
		conn.Close()
		if err != nil {
			t.Errorf("TLS %s через воркер: %v", host, err)
		}
	}
}

// TestLiveWorkerDC: MTProto до дата-центров только через воркер.
func TestLiveWorkerDC(t *testing.T) {
	srv := &Server{Secret: make([]byte, 16), Worker: liveWorker(t), PoolSize: -1}
	srv.wsUpstream = srv.dialWorker
	for _, dc := range []int16{2, 4} {
		start := time.Now()
		if err := askPQ(srv, dc); err != nil {
			t.Errorf("DC%d через воркер: %v", dc, err)
			continue
		}
		t.Logf("DC%d через воркер: %s", dc, time.Since(start).Round(time.Millisecond))
	}
}
