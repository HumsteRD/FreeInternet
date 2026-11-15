package tgproxy

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"testing"
	"time"
)

func TestIsCloudflareIP(t *testing.T) {
	for ip, want := range map[string]bool{
		"104.16.132.229":       true, // www.cloudflare.com
		"162.159.137.232":      true, // discord.com
		"172.66.0.227":         true, // x.com
		"188.114.97.3":         true,
		"2606:4700::6810:84e5": true,
		"140.82.121.4":         false, // github.com
		"142.250.185.78":       false, // google.com
		"149.154.167.99":       false, // telegram.org
		"172.63.255.255":       false, // сразу перед 172.64.0.0/13
		"172.72.0.1":           false, // сразу после
		"127.0.0.1":            false,
	} {
		if got := IsCloudflareIP(net.ParseIP(ip)); got != want {
			t.Errorf("IsCloudflareIP(%s) = %v", ip, got)
		}
	}
	if IsCloudflareIP(nil) {
		t.Error("пустой адрес принят за Cloudflare")
	}
}

// TestBehindCloudflare: сайт, все адреса которого принадлежат Cloudflare, воркер не откроет —
// FI узнаёт это заранее и помнит, не спрашивая DNS каждый раз.
func TestBehindCloudflare(t *testing.T) {
	lookups := map[string]int{}
	s := &Server{LookupIPs: func(_ context.Context, host string) []string {
		lookups[host]++
		switch host {
		case "discord.example":
			return []string{"162.159.137.232", "162.159.128.233"}
		case "mixed.example": // часть адресов не у Cloudflare: воркер дотянется
			return []string{"162.159.137.232", "140.82.121.4"}
		case "soundcloud.example":
			return []string{"18.66.122.5"}
		}
		return nil
	}}
	ctx := context.Background()
	for host, want := range map[string]bool{
		"discord.example":    true,
		"mixed.example":      false,
		"soundcloud.example": false,
		"nowhere.example":    false, // адрес не нашёлся — судить не по чему
		"104.16.1.1":         true,
		"8.8.8.8":            false,
	} {
		if got := s.BehindCloudflare(ctx, host); got != want {
			t.Errorf("BehindCloudflare(%s) = %v", host, got)
		}
	}
	if !s.BehindCloudflare(ctx, "Discord.Example.") || lookups["discord.example"] != 1 {
		t.Errorf("сайт за Cloudflare должен помниться: запросов DNS %d", lookups["discord.example"])
	}
	if got := s.KnownBehindCloudflare([]string{"soundcloud.example", "discord.example"}); !slices.Equal(got, []string{"discord.example"}) {
		t.Errorf("помнятся недоступными: %v", got)
	}
}

// refusingWorker — воркер, которому Cloudflare не даёт подключиться к сайту: WebSocket
// открывается, и сразу приходит закрытие с причиной. hits считает обращения.
func refusingWorker(t *testing.T, hits *int) {
	t.Helper()
	edgeWorkerWith(t, &refusing{}, "", func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Upgrade") != "websocket" {
			return false
		}
		*hits++
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return true
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
		rw.Flush()
		peer := &wsConn{conn: conn, br: rw.Reader}
		reason := "tcp: proxy request failed, cannot connect to the specified address. It looks like you might be trying to connect to"
		peer.writeFrame(opClose, append([]byte{0x03, 0xF3}, reason...)) // 1011
		time.Sleep(50 * time.Millisecond)
		return true
	})
}

// TestWorkerRefusalRemembered: адрес сайта не попал в известные диапазоны Cloudflare, но воркер
// подключиться к нему не смог. FI это запоминает и за этим сайтом к воркеру больше не ходит —
// ни запросов из суточного лимита, ни ожидания.
func TestWorkerRefusalRemembered(t *testing.T) {
	hits := 0
	refusingWorker(t, &hits)
	s := &Server{
		Worker:    "example.com",
		dnsCache:  map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
		LookupIPs: func(context.Context, string) []string { return []string{"203.0.113.7"} },
	}
	ctx := context.Background()
	conn, err := s.dialWorkerTCP(ctx, "cf.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Read(make([]byte, 16))
	conn.Close()
	if !errors.Is(err, errWorkerRefused) {
		t.Fatalf("отказ Cloudflare не узнан: %v", err)
	}
	if _, err := s.dialWorkerTCP(ctx, "cf.example", 443); !errors.Is(err, errWorkerRefused) || hits != 1 {
		t.Fatalf("повторное обращение ушло к воркеру: запросов %d, %v", hits, err)
	}
	if !s.WorkerCantReach(ctx, "cf.example") {
		t.Fatal("сайт должен помниться недоступным для воркера")
	}
	// Адрес сайта не у Cloudflare: это «сервер не отвечает», а не «сайт стоит за Cloudflare» —
	// человеку нельзя говорить про Cloudflare то, чего нет.
	if s.BehindCloudflare(ctx, "cf.example") || len(s.KnownBehindCloudflare([]string{"cf.example"})) != 0 {
		t.Fatal("сайт с адресом не из Cloudflare назван сайтом за Cloudflare")
	}
	// Другие сайты это не задевает.
	if cant, _ := s.workerCantReach("other.example"); cant {
		t.Fatal("отказ распространился на другой сайт")
	}
}

// TestWorkerRefusalBehindCloudflare: воркер отказал, и адреса сайта — из диапазонов Cloudflare:
// это надолго, и окно может сказать об этом прямо.
func TestWorkerRefusalBehindCloudflare(t *testing.T) {
	hits := 0
	refusingWorker(t, &hits)
	s := &Server{
		Worker:    "example.com",
		dnsCache:  map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
		LookupIPs: func(context.Context, string) []string { return []string{"162.159.137.232"} },
	}
	ctx := context.Background()
	conn, err := s.dialWorkerTCP(ctx, "cf.example", 443)
	if err != nil {
		t.Fatal(err)
	}
	conn.Read(make([]byte, 16))
	conn.Close()
	// Адрес сайта уточняется в фоне.
	for range 100 {
		if len(s.KnownBehindCloudflare([]string{"cf.example"})) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := s.KnownBehindCloudflare([]string{"cf.example"}); len(got) != 1 {
		t.Fatal("сайт за Cloudflare не узнан после отказа воркера")
	}
	if _, err := s.dialWorkerTCP(ctx, "cf.example", 443); !errors.Is(err, ErrBehindCloudflare) || hits != 1 {
		t.Fatalf("повторное обращение: запросов к воркеру %d, %v", hits, err)
	}
}

// TestAppProxyDirectBehindCloudflare: программа, запущенная через Cloudflare, идёт к сайту за
// Cloudflare напрямую — воркер его открыть не может, а программа не должна из-за этого ломаться.
func TestAppProxyDirectBehindCloudflare(t *testing.T) {
	site, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer site.Close()
	go func() {
		for {
			conn, err := site.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				buf := make([]byte, 64)
				n, _ := conn.Read(buf)
				conn.Write(append([]byte("напрямую:"), buf[:n]...))
			}()
		}
	}()
	_, portText, _ := net.SplitHostPort(site.Addr().String())
	port, _ := strconv.Atoi(portText)

	hits := 0
	refusingWorker(t, &hits)
	s := &Server{
		Worker:    "example.com",
		dnsCache:  map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
		LookupIPs: func(context.Context, string) []string { return []string{"127.0.0.1"} },
	}
	p := &AppProxy{Server: s}
	srv := httptest.NewServer(p)
	defer srv.Close()
	target := net.JoinHostPort("cf.example", portText)

	// Первое соединение уходит к воркеру, и тот отказывает уже после «200»: программа получает
	// обрыв и повторяет запрос.
	if code, got := tunnel(t, srv, target); code != http.StatusOK || got != "" || hits != 1 {
		t.Fatalf("первое соединение: %d %q, запросов к воркеру %d", code, got, hits)
	}
	// Повтор идёт напрямую и работает.
	if code, got := tunnel(t, srv, target); code != http.StatusOK || got != "напрямую:ping" || hits != 1 {
		t.Fatalf("повтор: %d %q, запросов к воркеру %d", code, got, hits)
	}
	if st := p.Stats(); st.Direct != 1 || st.Conns != 2 {
		t.Fatalf("счётчики: %+v", st)
	}

	// Сайт, про который известно заранее (адреса из диапазонов Cloudflare), к воркеру не идёт вовсе.
	s.noteCantReach("known.example", time.Hour, true)
	if conn, err := p.open(context.Background(), "known.example", port); err != nil || hits != 1 {
		t.Fatalf("известный сайт за Cloudflare: %v, запросов к воркеру %d", err, hits)
	} else {
		io.WriteString(conn, "x")
		conn.Close()
	}
}

// TestVerifyPump: быстрая пересылка в воркере не сработала, а простая работает — FI дальше
// просит простую, и сайты открываются, как с прежним кодом воркера.
func TestVerifyPump(t *testing.T) {
	s := &Server{Worker: "w.example"}
	if err := s.verifyPump(func(bool) error { return nil }); err != nil || s.PlainPump() {
		t.Fatalf("всё работает: %v, простая пересылка %v", err, s.PlainPump())
	}
	broken := errors.New("воркер закрыл соединение")
	if err := s.verifyPump(func(plain bool) error {
		if plain {
			return nil
		}
		return broken
	}); err != nil || !s.PlainPump() {
		t.Fatalf("быстрая не работает, простая работает: %v, простая пересылка %v", err, s.PlainPump())
	}
	// Не проходит никак — дело не в способе пересылки: способ не меняется, ошибка наружу.
	if err := s.verifyPump(func(bool) error { return broken }); !errors.Is(err, broken) || !s.PlainPump() {
		t.Fatalf("не работает никак: %v, простая пересылка %v", err, s.PlainPump())
	}
	// Воркер починили (новый код): проверка возвращает быструю пересылку.
	if err := s.verifyPump(func(bool) error { return nil }); err != nil || s.PlainPump() {
		t.Fatalf("после починки: %v, простая пересылка %v", err, s.PlainPump())
	}
}

// TestPlainPumpAsksWorker: с простой пересылкой FI передаёт воркеру p=0, без неё — нет.
func TestPlainPumpAsksWorker(t *testing.T) {
	var queries []string
	edgeWorkerWith(t, &refusing{}, "site.example", func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Upgrade") == "websocket" {
			queries = append(queries, r.URL.Query().Get("p"))
		}
		return false
	})
	s := &Server{
		Worker:   "example.com",
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}
	for _, plain := range []bool{false, true} {
		s.plainPump.Store(plain)
		conn, err := s.dialWorkerTCP(context.Background(), "site.example", 443)
		if err != nil {
			t.Fatal(err)
		}
		conn.Close()
	}
	if !slices.Equal(queries, []string{"", "0"}) {
		t.Fatalf("параметр p в запросах к воркеру: %q", queries)
	}
}
