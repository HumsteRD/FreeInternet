//go:build live

package tgproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
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
	for _, host := range []string{"telegram.org", "telegra.ph", "fragment.com", "tdesktop.com", "core.telegram.org"} {
		conn, err := s.dialWorkerTCP(ctx, host, 443)
		if err != nil {
			t.Logf("%s: dial: %v", host, err)
			continue
		}
		tc := tls.Client(conn, &tls.Config{ServerName: host})
		hctx, cancel := context.WithTimeout(ctx, 8*time.Second)
		err = tc.HandshakeContext(hctx)
		cancel()
		t.Logf("TLS %s через воркер: %v", host, err)
		conn.Close()
	}
	for _, target := range []struct {
		host string
		port int
	}{{"telegram.org", 443}} {
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

func TestLiveWorkerDC(t *testing.T) {
	worker := os.Getenv("FI_WORKER")
	if worker == "" {
		t.Skip("FI_WORKER не задан")
	}
	srv := &Server{Secret: make([]byte, 16), Worker: worker, PoolSize: -1}
	srv.wsUpstream = srv.dialWorker // только воркер
	for _, dc := range []int16{2, 4} {
		start := time.Now()
		err := askPQ(srv, dc)
		t.Logf("DC%d через воркер: %v (%s)", dc, err, time.Since(start).Round(time.Millisecond))
	}
}

func TestLiveWorkerFallback(t *testing.T) {
	worker := os.Getenv("FI_WORKER")
	if worker == "" {
		t.Skip("FI_WORKER не задан")
	}
	srv := &Server{Secret: make([]byte, 16), Worker: worker, PoolSize: -1}
	for i := range 3 {
		start := time.Now()
		err := askPQ(srv, 2)
		t.Logf("попытка %d: %v (%s), воркер отложен: %v", i+1, err, time.Since(start).Round(time.Millisecond), srv.workerPaused())
	}
}

// dialWSNoProto — как dialWS, но без Sec-WebSocket-Protocol: проверяем, не в нём ли дело.
func dialWSNoProto(ctx context.Context, addr, host, path string) (*wsConn, error) {
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}})
	if err := conn.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	key := "dGhlIHNhbXBsZSBub25jZQ=="
	io.WriteString(conn, "GET "+path+" HTTP/1.1\r\nHost: "+host+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: "+key+"\r\nSec-WebSocket-Version: 13\r\n\r\n")
	br := bufio.NewReaderSize(conn, 64<<10)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 101 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return &wsConn{conn: conn, br: br, host: host, raw: true}, nil
}

func TestLiveWorkerNoProto(t *testing.T) {
	worker := os.Getenv("FI_WORKER")
	if worker == "" {
		t.Skip("FI_WORKER не задан")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ws, err := dialWSNoProto(ctx, net.JoinHostPort(worker, "443"), worker, "/?host=tdesktop.com&port=443")
	if err != nil {
		t.Fatal(err)
	}
	tc := tls.Client(&wsStream{ws: ws}, &tls.Config{ServerName: "tdesktop.com"})
	err = tc.HandshakeContext(ctx)
	t.Logf("TLS tdesktop.com без Sec-WebSocket-Protocol: %v", err)
}

func TestLiveWorkerEcho(t *testing.T) {
	worker := os.Getenv("FI_WORKER")
	if worker == "" {
		t.Skip("FI_WORKER не задан")
	}
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
