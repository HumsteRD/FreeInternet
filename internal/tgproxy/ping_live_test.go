//go:build live

// Отвечает ли воркер на ping WebSocket: от этого зависит, можно ли по ping узнавать о мёртвом соединении.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; go test -tags live -run LivePing -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"net"
	"os"
	"testing"
	"time"
)

func TestLivePing(t *testing.T) {
	bind := net.ParseIP(os.Getenv("FI_BIND"))
	if bind == nil {
		t.Skip("FI_BIND не задан")
	}
	wsLocalAddr = &net.TCPAddr{IP: bind}
	defer func() { wsLocalAddr = nil }()
	worker := liveWorker(t)
	srv := &Server{Worker: worker}
	ips := srv.resolve(context.Background(), worker, 0)
	if len(ips) == 0 {
		t.Fatal("адрес воркера не найден")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ws, err := dialWS(ctx, net.JoinHostPort(ips[0], "443"), worker, "/?host=telegram.org&port=443")
	if err != nil {
		t.Fatal(errText(err))
	}
	defer ws.close()
	for i := range 3 {
		start := time.Now()
		if err := ws.writeFrame(opPing, []byte("fi")); err != nil {
			t.Fatal(err)
		}
		ws.conn.SetReadDeadline(time.Now().Add(3 * time.Second))
		_, op, payload, err := ws.readFrame()
		if err != nil {
			t.Fatalf("ping %d: ответа нет: %v", i+1, err)
		}
		t.Logf("ping %d: ответ с кодом %#x %q за %v", i+1, op, payload, time.Since(start).Round(time.Millisecond))
		if op != opPong {
			t.Fatalf("ждали pong")
		}
		time.Sleep(300 * time.Millisecond)
	}
}
