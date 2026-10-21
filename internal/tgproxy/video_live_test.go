//go:build live

// Видеосервер Google и скорость через воркер (нужен ключ FI):
//
//	$env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; go test -tags live -run LiveWorkerVideo -v ./internal/tgproxy
package tgproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestLiveWorkerVideo(t *testing.T) {
	key := os.Getenv("FI_WORKER_KEY")
	if key == "" {
		t.Skip("FI_WORKER_KEY не задан")
	}
	s := &Server{Worker: liveWorker(t), WorkerKey: key}
	fetch := func(host, path, rng string) {
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		start := time.Now()
		conn, err := s.dialWorkerTCP(ctx, host, 443)
		if err != nil {
			t.Logf("%s: воркер: %v", host, err)
			return
		}
		defer conn.Close()
		tc := tls.Client(conn, &tls.Config{ServerName: host})
		tc.SetDeadline(time.Now().Add(35 * time.Second))
		if err := tc.Handshake(); err != nil {
			t.Logf("%s: TLS через воркер не прошёл за %v: %v", host, time.Since(start).Round(time.Millisecond), err)
			return
		}
		hs := time.Since(start)
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		req.Write(tc)
		resp, err := http.ReadResponse(bufio.NewReader(tc), req)
		if err != nil {
			t.Logf("%s: TLS за %v, ответа нет: %v", host, hs.Round(time.Millisecond), err)
			return
		}
		dl := time.Now()
		n, _ := io.Copy(io.Discard, resp.Body)
		secs := time.Since(dl).Seconds()
		t.Logf("%s: TLS за %v, HTTP %d, %d КБ за %.1f с (%.1f Мбит/с)", host, hs.Round(time.Millisecond), resp.StatusCode, n>>10, secs, float64(n)*8/1e6/max(secs, 0.001))
	}
	fetch("rr1---sn-n8v7kn76.googlevideo.com", "/generate_204", "")
	fetch("rr1---sn-n8v7zns6.googlevideo.com", "/generate_204", "")
	fetch("dl.google.com", "/dl/chrome/install/googlechromestandaloneenterprise64.msi", "bytes=0-10485759")
}
