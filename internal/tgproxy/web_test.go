package tgproxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
	pac := PAC("127.0.0.1:1454")
	if !strings.Contains(pac, `dnsDomainIs(host, ".telegram.org")) return "PROXY 127.0.0.1:1454; DIRECT"`) || !strings.Contains(pac, `return "DIRECT";`) {
		t.Fatalf("PAC:\n%s", pac)
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
