//go:build live

// Почему обрывается загрузка через воркер. Воркер читает с сайта без оглядки на то, успевает ли
// FI принимать: если сайт отдаёт быстрее, чем идёт канал до FI, данные копятся в воркере. Тест
// качает один и тот же файл порциями разного размера (запросы Range по одному соединению): малые
// порции накопиться не дают. Если малые порции доходят всегда, а большие обрываются — дело
// в накоплении, а не в провайдере.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; $env:FI_LIVE_EDGES="1"
//	$env:FI_BACKLOG_URL="https://dl.google.com/…/big.msi"; go test -tags live -run LiveBacklog -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLiveBacklog(t *testing.T) {
	bind, key, target := net.ParseIP(os.Getenv("FI_BIND")), os.Getenv("FI_WORKER_KEY"), os.Getenv("FI_BACKLOG_URL")
	if bind == nil || key == "" || target == "" || os.Getenv("FI_LIVE_EDGES") == "" {
		t.Skip("FI_BIND, FI_WORKER_KEY, FI_BACKLOG_URL или FI_LIVE_EDGES не задан")
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

	runs, _ := strconv.Atoi(os.Getenv("FI_BACKLOG_RUNS"))
	runs = max(runs, 4)
	const total = 12 << 20 // сколько скачать за одно соединение
	sizes := []int64{128 << 10, 512 << 10, 2 << 20, 12 << 20}
	if v := os.Getenv("FI_BACKLOG_SIZES"); v != "" { // размеры порций в килобайтах через запятую
		sizes = nil
		for _, s := range strings.Split(v, ",") {
			kb, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			sizes = append(sizes, kb<<10)
		}
	}
	h1 := os.Getenv("FI_BACKLOG_H1") != "" // HTTP/1.1: без окна HTTP/2, сайт шлёт сколько примет TCP
	for _, size := range sizes {
		ok := 0
		var bad []string
		for range runs {
			tr := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			if h1 {
				tr.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			}
			client := &http.Client{Transport: tr}
			var got int64
			var fail string
			start := time.Now()
			for off := int64(0); off < total && fail == ""; off += size {
				rctx, stop := context.WithTimeout(ctx, 12*time.Second)
				req, _ := http.NewRequestWithContext(rctx, http.MethodGet, target, nil)
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+size-1))
				resp, err := client.Do(req)
				if err != nil {
					fail = firstLine(strings.ReplaceAll(errText(err), target, ""))
					stop()
					break
				}
				n, err := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				stop()
				got += n
				if err != nil || n < size {
					fail = fmt.Sprintf("порция оборвана на %d КБ: %v", n>>10, err)
				}
			}
			tr.CloseIdleConnections()
			if fail == "" {
				ok++
			} else {
				bad = append(bad, fmt.Sprintf("%.1f МБ за %.1f с (%s)", float64(got)/1e6, time.Since(start).Seconds(), fail))
			}
		}
		t.Logf("порции по %d КБ, HTTP/1.1=%v: дошло %d из %d; обрывы: %v", size>>10, h1, ok, runs, bad)
	}
}
