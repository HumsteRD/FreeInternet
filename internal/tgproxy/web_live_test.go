//go:build live

// Мини-приложения через настоящий воркер: CONNECT к telegram.org идёт только через воркер,
// а поверх — обычный HTTPS со сквозным TLS.
//
//	$env:FI_WORKER="имя.логин.workers.dev"; go test -tags live -run LiveWebProxy -v ./internal/tgproxy
package tgproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveWebProxyViaWorker(t *testing.T) {
	worker := os.Getenv("FI_WORKER")
	if worker == "" {
		t.Skip("FI_WORKER не задан")
	}
	p := &WebProxy{
		Server: &Server{Worker: worker},
		dialDirect: func(context.Context, string) (net.Conn, error) {
			return nil, errors.New("как будто адрес закрыт")
		},
	}
	srv := httptest.NewServer(p)
	defer srv.Close()

	start := time.Now()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	io.WriteString(conn, "CONNECT telegram.org:443 HTTP/1.1\r\nHost: telegram.org:443\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("CONNECT: %v %v", resp, err)
	}
	tc := tls.Client(&bufConn{Conn: conn, r: br}, &tls.Config{ServerName: "telegram.org"})
	if err := tc.Handshake(); err != nil {
		t.Fatalf("TLS с telegram.org через воркер: %v", err)
	}
	io.WriteString(tc, "GET /js/telegram-web-app.js HTTP/1.1\r\nHost: telegram.org\r\nConnection: close\r\n\r\n")
	body, _ := io.ReadAll(tc)
	if !strings.Contains(string(body), "200 OK") || !strings.Contains(string(body), "Telegram") {
		t.Fatalf("ответ: %.300s", body)
	}
	t.Logf("telegram-web-app.js через воркер: %d байт за %s", len(body), time.Since(start).Round(time.Millisecond))
}

type bufConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufConn) Read(p []byte) (int, error) { return c.r.Read(p) }
