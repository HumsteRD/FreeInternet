//go:build live

// С какого IP и из какой страны воркер выходит в сеть (нужен ключ FI):
//
//	$env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; go test -tags live -run LiveWorkerEgress -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"crypto/tls"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLiveWorkerEgress(t *testing.T) {
	key := os.Getenv("FI_WORKER_KEY")
	if key == "" {
		t.Skip("FI_WORKER_KEY не задан")
	}
	s := &Server{Worker: liveWorker(t), WorkerKey: key}
	for range 3 {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		conn, err := s.dialWorkerTCP(ctx, "ipinfo.io", 443)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		tc := tls.Client(conn, &tls.Config{ServerName: "ipinfo.io"})
		tc.SetDeadline(time.Now().Add(15 * time.Second))
		io.WriteString(tc, "GET /json HTTP/1.1\r\nHost: ipinfo.io\r\nUser-Agent: curl/8\r\nConnection: close\r\n\r\n")
		body, _ := io.ReadAll(tc)
		conn.Close()
		cancel()
		text := string(body)
		var parts []string
		for _, f := range []string{`"ip"`, `"city"`, `"country"`, `"org"`} {
			if i := strings.Index(text, f); i >= 0 {
				line, _, _ := strings.Cut(text[i:], "\n")
				parts = append(parts, strings.TrimSuffix(strings.TrimSpace(line), ","))
			}
		}
		t.Logf("воркер выходит как: %s", strings.Join(parts, " "))
	}
}
