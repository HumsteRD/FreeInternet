//go:build live

// Пропускает ли Cloudflare двусторонний поток HTTP/2 к воркеру: запрос с телом, которое ещё не
// закончено, и ответ, который уже идёт. От этого зависит, можно ли пересылать данные без
// WebSocket — силами самого Cloudflare, не тратя процессорное время воркера.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; go test -tags live -run LiveDuplex -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveDuplex(t *testing.T) {
	bind := net.ParseIP(os.Getenv("FI_BIND"))
	if bind == nil {
		t.Skip("FI_BIND не задан")
	}
	worker := liveWorker(t)
	srv := &Server{Worker: worker}
	ips := srv.resolve(context.Background(), worker, 0)
	if len(ips) == 0 {
		t.Fatal("адрес воркера не найден")
	}
	tr := &http.Transport{
		ForceAttemptHTTP2: true,
		DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{LocalAddr: &net.TCPAddr{IP: bind}}
			raw, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(ips[0], "443"))
			if err != nil {
				return nil, err
			}
			conn := tls.Client(raw, &tls.Config{ServerName: worker, NextProtos: []string{"h2"}})
			if err := conn.HandshakeContext(ctx); err != nil {
				raw.Close()
				return nil, err
			}
			return conn, nil
		},
	}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr}

	pr, pw := io.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+worker+"/", pr)
	req.Header.Set("Content-Type", "application/octet-stream")
	if ct := os.Getenv("FI_DUPLEX_TYPE"); ct != "" { // например application/grpc: его Cloudflare может не копить
		req.Header.Set("Content-Type", ct)
		req.Header.Set("TE", "trailers")
	}
	go pw.Write([]byte("начало тела; конец не отправлен")) // тело остаётся открытым
	start := time.Now()
	type result struct {
		resp *http.Response
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := client.Do(req)
		done <- result{resp, err}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("запрос с открытым телом: %v", errText(r.err))
		}
		body, _ := io.ReadAll(io.LimitReader(r.resp.Body, 200))
		t.Logf("ответ пришёл за %v, пока тело запроса ещё открыто: %s HTTP %d %q",
			time.Since(start).Round(time.Millisecond), r.resp.Proto, r.resp.StatusCode, body)
		r.resp.Body.Close()
	case <-time.After(8 * time.Second):
		t.Logf("за 8 с ответа нет: Cloudflare ждёт конца тела запроса. Закрываю тело…")
		pw.Close()
		select {
		case r := <-done:
			if r.err == nil {
				t.Logf("после закрытия тела ответ пришёл через %v от начала: HTTP %d — запрос копится целиком",
					time.Since(start).Round(time.Millisecond), r.resp.StatusCode)
				r.resp.Body.Close()
			} else {
				t.Logf("после закрытия тела: %v", errText(r.err))
			}
		case <-time.After(8 * time.Second):
			t.Log("и после закрытия тела ответа нет")
		}
	}
	pw.Close()
}
