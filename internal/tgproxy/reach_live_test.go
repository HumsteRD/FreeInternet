//go:build live

// До каких сайтов воркер дотягивается (нужен ключ FI). Воркер открывает TCP сам, и не ко всем
// адресам Cloudflare это позволяет — проверка показывает, какие сайты через него откроются:
//
//	$env:FI_REACH="discord.com,soundcloud.com,github.com"; go test -tags live -run LiveWorkerReach -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"fi/internal/probe"
)

func TestLiveWorkerReach(t *testing.T) {
	key, targets := os.Getenv("FI_WORKER_KEY"), os.Getenv("FI_REACH")
	if key == "" || targets == "" {
		t.Skip("FI_WORKER_KEY или FI_REACH не задан")
	}
	if bind := net.ParseIP(os.Getenv("FI_BIND")); bind != nil { // мимо VPN, через провайдера
		wsLocalAddr = &net.TCPAddr{IP: bind}
		defer func() { wsLocalAddr = nil }()
	}
	worker := liveWorker(t)
	s := &Server{Worker: worker, WorkerKey: key, LookupFallback: probe.LookupDoH}
	ok := 0
	for _, host := range strings.Split(targets, ",") {
		host = strings.TrimSpace(host)
		start := time.Now()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		conn, err := s.dialWorkerTCP(ctx, host, 443)
		if err == nil {
			tc := tls.Client(conn, &tls.Config{ServerName: host})
			tc.SetDeadline(time.Now().Add(10 * time.Second))
			err = tc.Handshake()
			conn.Close()
		}
		cancel()
		took := time.Since(start).Round(time.Millisecond)
		if err != nil {
			t.Logf("%-28s НЕТ  %v  %s", host, took, strings.ReplaceAll(err.Error(), worker, "<worker>"))
			continue
		}
		ok++
		t.Logf("%-28s да   %v", host, took)
	}
	if ok == 0 {
		t.Fatal("через воркер не открылся ни один сайт")
	}
}

// TestLiveAppProxy: программа, запущенная через Cloudflare, ходит через прокси программ. Обычный
// сайт идёт через воркер (и видит не российский адрес), сайт за Cloudflare — напрямую.
//
//	$env:FI_APP_URLS="https://ipinfo.io/json,https://discord.com/api/v9/gateway"; go test -tags live -run LiveAppProxy -v ./internal/tgproxy
func TestLiveAppProxy(t *testing.T) {
	key, urls := os.Getenv("FI_WORKER_KEY"), os.Getenv("FI_APP_URLS")
	if key == "" || urls == "" {
		t.Skip("FI_WORKER_KEY или FI_APP_URLS не задан")
	}
	if bind := net.ParseIP(os.Getenv("FI_BIND")); bind != nil {
		wsLocalAddr = &net.TCPAddr{IP: bind}
		defer func() { wsLocalAddr = nil }()
	}
	worker := liveWorker(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	p := &AppProxy{Addr: addr, Server: &Server{Worker: worker, WorkerKey: key, LookupFallback: probe.LookupDoH}}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.ListenAndServe(ctx)
	for !p.Stats().Listening {
		time.Sleep(10 * time.Millisecond)
	}
	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	for _, u := range strings.Split(urls, ",") {
		before := p.Stats()
		start := time.Now()
		resp, err := client.Get(strings.TrimSpace(u))
		took := time.Since(start).Round(time.Millisecond)
		if err != nil {
			t.Errorf("%s: %v  %s", u, took, strings.ReplaceAll(err.Error(), worker, "<worker>"))
			continue
		}
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		resp.Body.Close()
		path := "через воркер"
		if p.Stats().Direct > before.Direct {
			path = "напрямую (сайт за Cloudflare)"
		}
		t.Logf("%s: HTTP %d за %v, %s\n    %s", u, resp.StatusCode, took, path, strings.Join(strings.Fields(string(body)), " "))
	}
	t.Logf("счётчики: %+v", p.Stats())
}
