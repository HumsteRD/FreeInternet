//go:build live

// Локальный прокси FI для видеосерверов YouTube: напрямую с адреса FI_BIND (мимо VPN), а если
// сервер молчит — через воркер. Работает FI_SECONDS секунд на 127.0.0.1:1460.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; go test -tags live -run LiveFallbackProxy -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"fi/internal/probe"
)

func TestLiveFallbackProxy(t *testing.T) {
	bind, key := os.Getenv("FI_BIND"), os.Getenv("FI_WORKER_KEY")
	if bind == "" || key == "" {
		t.Skip("FI_BIND или FI_WORKER_KEY не задан")
	}
	secs, _ := strconv.Atoi(os.Getenv("FI_SECONDS"))
	srv := &Server{Worker: liveWorker(t), WorkerKey: key}
	var direct, worker atomic.Int32
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(bind)}}
	p := &WebProxy{
		Addr:     "127.0.0.1:1460",
		Server:   srv,
		Fallback: func() []string { return []string{"googlevideo.com"} },
		dialDirect: func(ctx context.Context, addr string) (net.Conn, error) {
			host, port, _ := net.SplitHostPort(addr)
			ip, err := probe.LookupDoH(ctx, host) // системный DNS сейчас у VPN
			if err != nil {
				return nil, err
			}
			direct.Add(1)
			t.Logf("напрямую %s (%s)", host, ip)
			return d.DialContext(ctx, "tcp4", net.JoinHostPort(ip, port))
		},
		dialWorker: func(ctx context.Context, host string, port int) (net.Conn, error) {
			worker.Add(1)
			t.Logf("ЧЕРЕЗ ВОРКЕР %s", host)
			return srv.dialWorkerTCP(ctx, host, port)
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(max(secs, 30))*time.Second)
	defer cancel()
	p.ListenAndServe(ctx)
	t.Logf("соединений напрямую: %d, через воркер: %d", direct.Load(), worker.Load())
}
