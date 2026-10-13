//go:build live

// Долгий поток через воркер: выдерживает ли бесплатный Cloudflare Worker музыку и видео.
//
//	$env:FI_WORKER="…"; go test -tags live -run LiveWorkerStream -v -timeout 5m ./internal/tgproxy
package tgproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestLiveWorkerStream(t *testing.T) {
	p := &WebProxy{
		Server:     &Server{Worker: liveWorker(t)},
		dialDirect: func(context.Context, string) (net.Conn, error) { return nil, errors.New("только воркер") },
	}
	srv := httptest.NewServer(p)
	defer srv.Close()
	proxyURL, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://telegram.org/dl/desktop/win64", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	t.Logf("%s, размер %d МБ", resp.Request.URL, resp.ContentLength>>20)

	start := time.Now()
	var total int64
	buf := make([]byte, 256<<10)
	next := 10 * time.Second
	for {
		n, err := resp.Body.Read(buf)
		total += int64(n)
		if el := time.Since(start); el >= next {
			t.Logf("%3.0f с: %d МБ, %.1f МБ/с", el.Seconds(), total>>20, float64(total)/el.Seconds()/(1<<20))
			next += 10 * time.Second
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("поток оборвался через %s на %d МБ: %v", time.Since(start).Round(time.Second), total>>20, err)
		}
	}
	el := time.Since(start)
	t.Logf("готово: %d МБ за %s, %.1f МБ/с", total>>20, el.Round(time.Second), float64(total)/el.Seconds()/(1<<20))
}
