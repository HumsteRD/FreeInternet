package tgproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestAppProxy: программа, запущенная через FI, ходит на любые сайты — и всё идёт через воркер.
func TestAppProxy(t *testing.T) {
	var dialed []string
	p := &AppProxy{
		dial: func(_ context.Context, host string, port int) (net.Conn, error) {
			dialed = append(dialed, fmt.Sprintf("%s:%d", host, port))
			if host == "closed.example" {
				return nil, errors.New("воркер не смог подключиться")
			}
			if port == 80 { // обычный HTTP: «сайт» отвечает на запрос
				ours, theirs := net.Pipe()
				go func() {
					defer theirs.Close()
					req, err := http.ReadRequest(bufio.NewReader(theirs))
					if err != nil {
						return
					}
					body := "путь " + req.URL.RequestURI() + ", сайт " + req.Host + ", прокси-заголовок " + req.Header.Get("Proxy-Connection")
					fmt.Fprintf(theirs, "HTTP/1.1 200 OK\r\nContent-Length: %d\r\nX-Site: yes\r\n\r\n%s", len(body), body)
				}()
				return ours, nil
			}
			return echoUpstream("worker:"), nil
		},
	}
	srv := httptest.NewServer(p)
	defer srv.Close()

	// HTTPS: туннель к любому сайту и любому порту.
	if code, got := tunnel(t, srv, "api.example.org:443"); code != 200 || got != "worker:ping" {
		t.Fatalf("туннель: %d %q", code, got)
	}
	if code, got := tunnel(t, srv, "game.example.org:8443"); code != 200 || got != "worker:ping" {
		t.Fatalf("туннель на другой порт: %d %q", code, got)
	}
	if code, _ := tunnel(t, srv, "closed.example:443"); code != http.StatusBadGateway {
		t.Fatalf("сайт не открылся: %d, ждали 502", code)
	}

	// Обычный HTTP: запрос пересылается сайту без заголовков прокси.
	proxyURL, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	req, _ := http.NewRequest(http.MethodGet, "http://update.example.org/check?v=1", nil)
	req.Header.Set("Proxy-Connection", "keep-alive")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("X-Site") != "yes" || string(body) != "путь /check?v=1, сайт update.example.org, прокси-заголовок " {
		t.Fatalf("обычный HTTP: %d %q", resp.StatusCode, body)
	}

	want := []string{"api.example.org:443", "game.example.org:8443", "closed.example:443", "update.example.org:80"}
	if strings.Join(dialed, " ") != strings.Join(want, " ") {
		t.Fatalf("через воркер открыто %v, ждали %v", dialed, want)
	}
	if st := p.Stats(); st.Conns != 3 || st.Failed != 1 || st.LastUsed.IsZero() || !strings.Contains(st.LastError, "не смог") {
		t.Fatalf("счётчики: %+v", st)
	}
}

// TestAppProxyLocalDirect: свой компьютер и домашняя сеть — напрямую: воркеру до них не достать.
func TestAppProxyLocalDirect(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "локальный сервер") }))
	defer local.Close()
	p := &AppProxy{dial: func(context.Context, string, int) (net.Conn, error) {
		return nil, errors.New("локальный адрес ушёл через воркер")
	}}
	srv := httptest.NewServer(p)
	defer srv.Close()
	proxyURL, _ := url.Parse(srv.URL)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get(local.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "локальный сервер" {
		t.Fatalf("локальный адрес: %d %q", resp.StatusCode, body)
	}
	for host, want := range map[string]bool{"localhost": true, "192.168.1.10": true, "10.0.0.5": true, "printer.local": true, "example.com": false, "8.8.8.8": false} {
		if got := localTarget(host); got != want {
			t.Errorf("localTarget(%q) = %v", host, got)
		}
	}
}
