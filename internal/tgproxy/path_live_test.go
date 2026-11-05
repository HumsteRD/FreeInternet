//go:build live

// Путь Telegram через воркер мимо VPN: откуда воркер выходит в сеть, сколько стоит рукопожатие
// до серверов Telegram и какая скорость загрузки.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; go test -tags live -run LiveWorkerPath -v ./internal/tgproxy
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
	"strings"
	"testing"
	"time"
)

func TestLiveWorkerPath(t *testing.T) {
	key, bind := os.Getenv("FI_WORKER_KEY"), net.ParseIP(os.Getenv("FI_BIND"))
	if key == "" || bind == nil {
		t.Skip("FI_WORKER_KEY или FI_BIND не задан")
	}
	wsLocalAddr = &net.TCPAddr{IP: bind}
	defer func() { wsLocalAddr = nil }()
	s := &Server{Worker: liveWorker(t), WorkerKey: key}
	if ips := os.Getenv("FI_EDGE"); ips != "" { // адреса Cloudflare вместо DNS
		s.dnsCache = map[string]dnsEntry{s.Worker: {ips: strings.Split(ips, ","), at: time.Now()}}
	}

	get := func(host, path string, limit int64) (time.Duration, time.Duration, int64, string) {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		defer cancel()
		start := time.Now()
		conn, err := s.dialWorkerTCP(ctx, host, 443)
		if err != nil {
			return 0, 0, 0, err.Error()
		}
		defer conn.Close()
		ws := time.Since(start)
		tc := tls.Client(conn, &tls.Config{ServerName: host})
		tc.SetDeadline(time.Now().Add(20 * time.Second))
		if err := tc.Handshake(); err != nil {
			return ws, 0, 0, err.Error()
		}
		hs := time.Since(start) - ws
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+path, nil)
		req.Header.Set("User-Agent", "curl/8")
		if limit > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=0-%d", limit-1))
		}
		req.Write(tc)
		resp, err := http.ReadResponse(bufio.NewReader(tc), req)
		if err != nil {
			return ws, hs, 0, err.Error()
		}
		defer resp.Body.Close()
		if limit == 0 {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			return ws, hs, int64(len(body)), string(body)
		}
		if loc := resp.Header.Get("Location"); loc != "" {
			return ws, hs, 0, "→ " + loc
		}
		n, _ := io.Copy(io.Discard, io.LimitReader(resp.Body, limit))
		return ws, hs, n, resp.Status
	}

	_, _, _, body := get("ipinfo.io", "/json", 0)
	var where []string
	for _, f := range []string{`"city"`, `"country"`, `"org"`} {
		if i := strings.Index(body, f); i >= 0 {
			line, _, _ := strings.Cut(body[i:], "\n")
			where = append(where, strings.TrimSuffix(strings.TrimSpace(line), ","))
		}
	}
	t.Logf("воркер выходит в сеть: %s", strings.Join(where, " "))
	_, _, _, trace := get("www.cloudflare.com", "/cdn-cgi/trace", 0)
	for _, line := range strings.Split(trace, "\n") {
		if strings.HasPrefix(line, "colo=") {
			t.Logf("воркер работает в дата-центре Cloudflare: %s", line)
		}
	}
	for range 3 {
		ws, hs, _, errText := get("telegram.org", "/", 0)
		t.Logf("telegram.org: WebSocket до воркера %v, рукопожатие TLS через воркер %v %s", ws.Round(time.Millisecond), hs.Round(time.Millisecond), firstLine(errText))
	}
	// Большой файл Telegram: установщик Desktop (адрес — из перенаправления telegram.org/dl).
	if host, path := os.Getenv("FI_DL_HOST"), os.Getenv("FI_DL_PATH"); host != "" {
		start := time.Now()
		ws, hs, n, status := get(host, path, 30<<20)
		secs := time.Since(start).Seconds() - ws.Seconds() - hs.Seconds()
		t.Logf("загрузка через воркер: %s, %d МБ за %.1f с — %.0f Мбит/с", status, n>>20, secs, float64(n)*8/1e6/max(secs, 0.01))
	}
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	if len(line) > 60 {
		line = line[:60]
	}
	return line
}

// Какие входы в Cloudflare FI выберет сам и насколько с ними быстрее открывается соединение.
//
//	$env:FI_LIVE_EDGES="1"; $env:FI_BIND="192.168.1.2"; … go test -tags live -run LiveEdges -v ./internal/tgproxy
func TestLiveEdges(t *testing.T) {
	key, bind := os.Getenv("FI_WORKER_KEY"), net.ParseIP(os.Getenv("FI_BIND"))
	if key == "" || bind == nil || os.Getenv("FI_LIVE_EDGES") == "" {
		t.Skip("FI_WORKER_KEY, FI_BIND или FI_LIVE_EDGES не задан")
	}
	wsLocalAddr = &net.TCPAddr{IP: bind}
	defer func() { wsLocalAddr = nil }()
	worker := liveWorker(t)
	start := time.Now()
	edges := pickEdges(context.Background(), worker, edgeCandidates)
	t.Logf("замер за %v: %+v", time.Since(start).Round(time.Millisecond), edges)

	measure := func(name string, s *Server) {
		var total time.Duration
		for range 4 {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			start := time.Now()
			conn, err := s.dialWorkerTCP(ctx, "telegram.org", 443)
			if err != nil {
				cancel()
				t.Logf("%s: %v", name, err)
				return
			}
			tc := tls.Client(conn, &tls.Config{ServerName: "telegram.org"})
			tc.SetDeadline(time.Now().Add(10 * time.Second))
			err = tc.Handshake()
			total += time.Since(start)
			conn.Close()
			cancel()
			if err != nil {
				t.Logf("%s: %v", name, err)
				return
			}
		}
		t.Logf("%s: соединение + TLS до telegram.org в среднем %v", name, (total / 4).Round(time.Millisecond))
	}
	dns := &Server{Worker: worker, WorkerKey: key}
	dns.edge.host, dns.edge.at = worker, time.Now() // без быстрых входов: только DNS
	measure("адреса из DNS", dns)
	fast := &Server{Worker: worker, WorkerKey: key}
	fast.edge.host, fast.edge.at, fast.edge.list = worker, time.Now(), edges
	measure("быстрые входы", fast)
}
