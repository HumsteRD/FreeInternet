//go:build live

// Замирает ли загрузка через воркер. Провайдер может «замораживать» соединения с адресами
// Cloudflare после первых килобайт: TCP остаётся открытым, а данные перестают приходить. Обход
// должен это снимать — тест считает, в скольких соединениях из FI_FREEZE_RUNS загрузка дошла до
// FI_FREEZE_MB мегабайт, а в скольких замерла.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; $env:FI_LIVE_EDGES="1"
//	$env:FI_FREEZE_URL="https://dl.google.com/…/big.msi"; go test -tags live -run LiveFreeze -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveFreeze(t *testing.T) {
	bind, key, target := net.ParseIP(os.Getenv("FI_BIND")), os.Getenv("FI_WORKER_KEY"), os.Getenv("FI_FREEZE_URL")
	if bind == nil || key == "" || target == "" || os.Getenv("FI_LIVE_EDGES") == "" {
		t.Skip("FI_BIND, FI_WORKER_KEY, FI_FREEZE_URL или FI_LIVE_EDGES не задан")
	}
	runs, _ := strconv.Atoi(os.Getenv("FI_FREEZE_RUNS"))
	runs = max(runs, 8)
	megabytes, _ := strconv.ParseFloat(os.Getenv("FI_FREEZE_MB"), 64)
	if megabytes <= 0 {
		megabytes = 3
	}
	limit := int64(megabytes * 1e6)
	stallWait := 6 * time.Second
	if secs, _ := strconv.Atoi(os.Getenv("FI_FREEZE_STALL")); secs > 0 {
		stallWait = time.Duration(secs) * time.Second
	}
	wsLocalAddr = &net.TCPAddr{IP: bind}
	defer func() { wsLocalAddr = nil }()
	worker := liveWorker(t)
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}

	srv := &Server{Worker: worker, WorkerKey: key}
	srv.edge.host, srv.edge.at = worker, time.Now()
	if ips := os.Getenv("FI_EDGE"); ips != "" { // через эти адреса Cloudflare вместо адресов из DNS
		for _, ip := range strings.Split(ips, ",") {
			srv.edge.list = append(srv.edge.list, Edge{IP: strings.TrimSpace(ip)})
		}
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	p := &WebProxy{Addr: addr, Server: srv, ViaWorker: func() []string { return []string{u.Hostname()} }}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.ListenAndServe(ctx)
	for !p.Listening() {
		time.Sleep(10 * time.Millisecond)
	}
	proxyURL, _ := url.Parse("http://" + addr)

	ok := 0
	var frozen, failed []string
	for range runs {
		// Каждая попытка — новое соединение с Cloudflare: заморозка привязана к соединению.
		client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL), DisableKeepAlives: true}}
		rctx, stop := context.WithTimeout(ctx, 15*time.Second)
		req, _ := http.NewRequestWithContext(rctx, http.MethodGet, target, nil)
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			failed = append(failed, firstLine(errText(err)))
			stop()
			continue
		}
		var n int64
		stall := time.AfterFunc(stallWait, stop) // столько секунд без данных — замерло
		buf := make([]byte, 64<<10)
		for n < limit {
			m, err := resp.Body.Read(buf)
			n += int64(m)
			stall.Reset(stallWait)
			if err != nil {
				break
			}
		}
		stall.Stop()
		resp.Body.Close()
		stop()
		if n >= limit {
			ok++
		} else {
			frozen = append(frozen, fmt.Sprintf("%.2f МБ за %.1f с", float64(n)/1e6, time.Since(start).Seconds()))
		}
	}
	t.Logf("через воркер, %d соединений по %.0f МБ: дошло %d, замерло %d %v, не открылось %d %v",
		runs, megabytes, ok, len(frozen), frozen, len(failed), failed)
	fmt.Println("ИТОГ", "дошло", ok, "замерло", len(frozen), "не открылось", len(failed), "из", runs) // строка для сценария замера
}
