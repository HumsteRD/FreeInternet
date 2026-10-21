package tgproxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func TestIsTelegramWeb(t *testing.T) {
	for host, want := range map[string]bool{
		"telegram.org": true, "core.telegram.org": true, "t.me": true, "TELEGRAM.ORG.": true,
		"nottelegram.org": false, "telegram.org.evil.com": false, "example.com": false,
	} {
		if got := IsTelegramWeb(host); got != want {
			t.Errorf("IsTelegramWeb(%q) = %v", host, got)
		}
	}
	pac := PAC("127.0.0.1:1454", nil, "")
	if !strings.Contains(pac, `dnsDomainIs(host, ".telegram.org")) return "PROXY 127.0.0.1:1454; DIRECT"`) || !strings.Contains(pac, `return "DIRECT";`) {
		t.Fatalf("PAC:\n%s", pac)
	}
	// Сайты через воркер и прежний прокси (VPN-клиента) для остального.
	pac = PAC("127.0.0.1:1454", []string{"soundcloud.com", `evil"); alert(1); ("`}, "127.0.0.1:10809")
	if !strings.Contains(pac, `dnsDomainIs(host, ".soundcloud.com")) return "PROXY 127.0.0.1:1454; PROXY 127.0.0.1:10809; DIRECT"`) ||
		!strings.Contains(pac, `return "PROXY 127.0.0.1:10809; DIRECT";`) || strings.Contains(pac, "alert") {
		t.Fatalf("PAC с сайтами через воркер:\n%s", pac)
	}
	if pac := PAC("127.0.0.1:1454", nil, `x"; alert(1)`); strings.Contains(pac, "alert") {
		t.Fatalf("прежний прокси попал в PAC без проверки:\n%s", pac)
	}
}

func TestWorkerCode(t *testing.T) {
	docs, err := os.ReadFile("../../docs/cf-worker.js")
	if err != nil {
		t.Fatal(err)
	}
	if strings.ReplaceAll(string(docs), "\r\n", "\n") != strings.ReplaceAll(workerSource, "\r\n", "\n") {
		t.Fatal("internal/tgproxy/cf-worker.js отличается от docs/cf-worker.js — скопируйте docs/cf-worker.js")
	}
	code := WorkerCode("k3y")
	if !strings.Contains(code, `const KEY = "k3y";`) || strings.Contains(code, workerKeyLine) {
		t.Fatal("ключ не вставлен в код воркера")
	}
}

func TestWebProxyViaWorkerSites(t *testing.T) {
	directCalls := 0
	p := &WebProxy{
		Server:    &Server{Worker: "fi.example.workers.dev"},
		ViaWorker: func() []string { return []string{"soundcloud.com"} },
		dialDirect: func(context.Context, string) (net.Conn, error) {
			directCalls++
			return echoUpstream("direct:"), nil
		},
		dialWorker: func(_ context.Context, host string, _ int) (net.Conn, error) {
			return echoUpstream("worker:" + host + ":"), nil
		},
	}
	srv := httptest.NewServer(p)
	defer srv.Close()
	if code, got := tunnel(t, srv, "api-v2.soundcloud.com:443"); code != 200 || got != "worker:api-v2.soundcloud.com:ping" {
		t.Fatalf("сайт через воркер: %d %q", code, got)
	}
	if directCalls != 0 {
		t.Fatal("сайт через воркер пробовали напрямую — получили бы «недоступно в вашей стране»")
	}
	if code, _ := tunnel(t, srv, "example.com:443"); code != http.StatusForbidden {
		t.Fatalf("сайт не из списка: %d", code)
	}
	resp, err := http.Get(srv.URL + SitesPACPath + "?fallback=127.0.0.1:10809")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "soundcloud.com") || !strings.Contains(string(body), "PROXY 127.0.0.1:10809") {
		t.Fatalf("PAC для браузеров:\n%s", body)
	}
}

// echoUpstream — «сайт»: отвечает тем же, что получил, с префиксом.
func echoUpstream(prefix string) net.Conn {
	ours, theirs := net.Pipe()
	go func() {
		defer theirs.Close()
		buf := make([]byte, 64)
		n, _ := theirs.Read(buf)
		theirs.Write(append([]byte(prefix), buf[:n]...))
	}()
	return ours
}

// tunnel открывает CONNECT через прокси и возвращает ответ «сайта» на "ping".
func tunnel(t *testing.T, proxy *httptest.Server, target string) (int, string) {
	t.Helper()
	conn, err := net.Dial("tcp", proxy.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	io.WriteString(conn, "ping")
	reply, _ := io.ReadAll(br)
	return resp.StatusCode, string(reply)
}

func TestWebProxyDirectThenWorker(t *testing.T) {
	directOK := true
	p := &WebProxy{
		Addr:   "127.0.0.1:1454",
		Server: &Server{Worker: "fi.example.workers.dev"},
		dialDirect: func(context.Context, string) (net.Conn, error) {
			if directOK {
				return echoUpstream("direct:"), nil
			}
			return nil, errors.New("i/o timeout")
		},
		dialWorker: func(_ context.Context, host string, port int) (net.Conn, error) {
			return echoUpstream("worker:"), nil
		},
	}
	srv := httptest.NewServer(p)
	defer srv.Close()

	if code, got := tunnel(t, srv, "telegram.org:443"); code != 200 || got != "direct:ping" {
		t.Fatalf("напрямую: %d %q", code, got)
	}
	directOK = false
	if code, got := tunnel(t, srv, "telegram.org:443"); code != 200 || got != "worker:ping" {
		t.Fatalf("адрес закрыт — ждали воркер: %d %q", code, got)
	}
	directOK = true // сайт ещё на паузе: напрямую не пробуем, сразу воркер
	if _, got := tunnel(t, srv, "telegram.org:443"); got != "worker:ping" {
		t.Fatalf("после неудачи напрямую: %q", got)
	}
	if code, _ := tunnel(t, srv, "example.com:443"); code != http.StatusForbidden {
		t.Fatalf("чужой сайт: %d, ждали 403", code)
	}
	if code, _ := tunnel(t, srv, "telegram.org:22"); code != http.StatusForbidden {
		t.Fatalf("чужой порт: %d, ждали 403", code)
	}

	resp, err := http.Get(srv.URL + PACPath)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("PAC: %v %v", resp, err)
	}
	resp.Body.Close()
}

func TestWebProxyFallbackOnSilence(t *testing.T) {
	old := silentTimeout
	silentTimeout = 200 * time.Millisecond
	defer func() { silentTimeout = old }()

	silentHosts := map[string]bool{}
	p := &WebProxy{
		Server:   &Server{Worker: "fi.example.workers.dev"},
		Fallback: func() []string { return []string{"googlevideo.com"} },
		dialDirect: func(_ context.Context, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			if silentHosts[host] {
				return silentUpstream(), nil
			}
			return echoUpstream("direct:"), nil
		},
		dialWorker: func(context.Context, string, int) (net.Conn, error) { return echoUpstream("worker:"), nil },
	}
	srv := httptest.NewServer(p)
	defer srv.Close()

	hello := "\x16\x03\x01\x00\x04ping"
	if code, got := tunnelTLS(t, srv, "rr1---sn-cache.googlevideo.com:443", hello); code != 200 || got != "direct:"+hello {
		t.Fatalf("сервер отвечает напрямую: %d %q", code, got)
	}
	silentHosts["rr1---sn-closed.googlevideo.com"] = true
	if code, got := tunnelTLS(t, srv, "rr1---sn-closed.googlevideo.com:443", hello); code != 200 || got != "worker:"+hello {
		t.Fatalf("сервер молчит — то же приветствие должно уйти через воркер: %d %q", code, got)
	}
	if code, _ := tunnelTLS(t, srv, "example.com:443", hello); code != http.StatusForbidden {
		t.Fatalf("сайт не из списка: %d", code)
	}
	resp, err := http.Get(srv.URL + SitesPACPath)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), `dnsDomainIs(host, ".googlevideo.com")`) {
		t.Fatalf("видеосерверов нет в PAC для браузеров:\n%s", body)
	}
}

func TestReadTLSRecord(t *testing.T) {
	long := "\x16\x03\x01\x07\x00" + strings.Repeat("x", 0x700) // приветствие с ML-KEM — больше одного пакета
	got, err := readTLSRecord(bufio.NewReader(strings.NewReader(long + "rest")))
	if err != nil || string(got) != long {
		t.Fatalf("прочитано %d байт, %v", len(got), err)
	}
}

// silentUpstream — сервер, который принимает данные и никогда не отвечает.
func silentUpstream() net.Conn {
	ours, theirs := net.Pipe()
	go io.Copy(io.Discard, theirs)
	return ours
}

// tunnelTLS — как tunnel, но отправляет запись TLS.
func tunnelTLS(t *testing.T, proxy *httptest.Server, target, record string) (int, string) {
	t.Helper()
	conn, err := net.Dial("tcp", proxy.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, "CONNECT "+target+" HTTP/1.1\r\nHost: "+target+"\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, ""
	}
	io.WriteString(conn, record)
	reply, _ := io.ReadAll(br)
	return resp.StatusCode, string(reply)
}

func TestWebProxyWithoutWorker(t *testing.T) {
	p := &WebProxy{
		Server:     &Server{},
		dialDirect: func(context.Context, string) (net.Conn, error) { return nil, errors.New("i/o timeout") },
	}
	srv := httptest.NewServer(p)
	defer srv.Close()
	if code, _ := tunnel(t, srv, "t.me:443"); code != http.StatusBadGateway {
		t.Fatalf("без воркера и без прямого пути: %d, ждали 502", code)
	}
}
