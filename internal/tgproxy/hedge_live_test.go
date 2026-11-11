//go:build live

// Свежие ролики YouTube на живой сети: вход в Cloudflare, который отказывает воркеру, не ломает
// путь через воркер, а закрытый провайдером видеосервер открывается без трёхсекундного ожидания.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; $env:FI_LIVE_EDGES="1"
//	$env:FI_VIDEO="rr4---sn-ixh7yn7e.googlevideo.com,rr1---sn-hpg5avbpj-n8ms.googlevideo.com"
//	go test -tags live -run LiveVideoFallback -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"fi/internal/probe"
)

// refusingEdges — адреса Cloudflare, которые 2026-11-11 отвечали за воркер отказом 403.
var refusingEdges = []string{"172.65.50.139", "104.16.123.96", "141.101.90.17", "172.64.32.17"}

func TestLiveVideoFallback(t *testing.T) {
	bind, key := net.ParseIP(os.Getenv("FI_BIND")), os.Getenv("FI_WORKER_KEY")
	if bind == nil || key == "" || os.Getenv("FI_LIVE_EDGES") == "" {
		t.Skip("FI_BIND, FI_WORKER_KEY или FI_LIVE_EDGES не задан")
	}
	wsLocalAddr = &net.TCPAddr{IP: bind}
	defer func() { wsLocalAddr = nil }()
	worker := liveWorker(t)

	// 1. Замер входов вместе с отказывающими адресами: в быстрые они попасть не должны.
	start := time.Now()
	edges := pickEdges(context.Background(), worker, append(append([]string{}, edgeCandidates...), refusingEdges...))
	t.Logf("замер за %v: %+v", time.Since(start).Round(time.Millisecond), edges)
	if len(edges) == 0 {
		t.Fatal("ни один вход не прошёл проверку воркером")
	}
	for _, e := range edges {
		for _, bad := range refusingEdges {
			if e.IP == bad {
				t.Fatalf("отказывающий вход %s выбран быстрым", bad)
			}
		}
	}

	// 2. Худший случай 0.4.0: первым в быстрых стоит отказывающий вход.
	srv := &Server{Worker: worker, WorkerKey: key, LookupFallback: probe.LookupDoH}
	srv.edge.host, srv.edge.at = worker, time.Now()
	srv.edge.list = append([]Edge{{IP: refusingEdges[0]}}, edges...)

	var direct, viaWorker atomic.Int32
	d := net.Dialer{LocalAddr: &net.TCPAddr{IP: bind}}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	p := &WebProxy{
		Addr:     addr,
		Server:   srv,
		Fallback: func() []string { return []string{"googlevideo.com"} },
		dialDirect: func(ctx context.Context, addr string) (net.Conn, error) {
			host, port, _ := net.SplitHostPort(addr)
			ip, err := probe.LookupDoH(ctx, host) // системный DNS сейчас у VPN
			if err != nil {
				return nil, err
			}
			direct.Add(1)
			return d.DialContext(ctx, "tcp4", net.JoinHostPort(ip, port))
		},
		dialWorker: func(ctx context.Context, host string, port int) (net.Conn, error) {
			viaWorker.Add(1)
			return srv.dialWorkerTCP(ctx, host, port)
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.ListenAndServe(ctx)
	for !p.Listening() {
		time.Sleep(10 * time.Millisecond)
	}

	proxyURL, _ := url.Parse("http://" + addr)
	get := func(host string) (int, time.Duration, error) {
		client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
		start := time.Now()
		resp, err := client.Get("https://" + host + "/generate_204")
		if err != nil {
			return 0, time.Since(start), err
		}
		resp.Body.Close()
		return resp.StatusCode, time.Since(start), nil
	}
	for _, host := range strings.Split(os.Getenv("FI_VIDEO"), ",") {
		if host = strings.TrimSpace(host); host == "" {
			continue
		}
		for i := range 3 {
			d0, w0 := direct.Load(), viaWorker.Load()
			code, took, err := get(host)
			t.Logf("%s, запрос %d: HTTP %d за %v (напрямую +%d, через воркер +%d) %v",
				host, i+1, code, took.Round(time.Millisecond), direct.Load()-d0, viaWorker.Load()-w0, errText(err))
			if err != nil || code != http.StatusNoContent {
				t.Errorf("%s не открылся", host)
			}
		}
	}
	t.Logf("быстрые входы после работы: %+v", srv.Edges())
	for _, e := range srv.Edges() {
		if e.IP == refusingEdges[0] {
			t.Errorf("отказавший вход %s остался в быстрых", e.IP)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), os.Getenv("FI_WORKER"), "<worker>")
}
