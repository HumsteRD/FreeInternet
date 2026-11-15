package tgproxy

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

// Обычные тесты не ходят в сеть: замер входов в Cloudflare подменён «ничего не ответило».
func TestMain(m *testing.M) {
	if os.Getenv("FI_LIVE_EDGES") == "" {
		edgePing = func(context.Context, string, string) (time.Duration, bool) { return 0, false }
	}
	os.Exit(m.Run())
}

func TestPickEdges(t *testing.T) {
	oldPing, oldCheck := edgePing, edgeCheck
	defer func() { edgePing, edgeCheck = oldPing, oldCheck }()
	rtt := map[string]time.Duration{"1.1.1.1": 70 * time.Millisecond, "2.2.2.2": 30 * time.Millisecond, "3.3.3.3": 40 * time.Millisecond,
		"4.4.4.4": 50 * time.Millisecond, "5.5.5.5": 20 * time.Millisecond, "6.6.6.6": 80 * time.Millisecond}
	edgePing = func(_ context.Context, ip, host string) (time.Duration, bool) {
		if ip == "3.3.3.3" {
			return 0, false // рукопожатие не сошлось
		}
		d, ok := rtt[ip]
		return d, ok
	}
	edgeCheck = func(_ context.Context, ip, host string) (string, error) {
		if ip == "5.5.5.5" {
			// Самый быстрый, но Cloudflare на нём отвечает за воркер отказом 1034.
			return "", &statusError{code: 403, body: "error code: 1034"}
		}
		return map[string]string{"2.2.2.2": "DME"}[ip], nil
	}
	got, checks := pickEdges(context.Background(), "w.example", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "5.5.5.5", "6.6.6.6", "9.9.9.9"})
	var ips []string
	for _, e := range got {
		ips = append(ips, e.IP)
	}
	if !slices.Equal(ips, []string{"2.2.2.2", "4.4.4.4", "1.1.1.1"}) || got[0].Colo != "DME" || got[2].Colo != "" || checks != 5 {
		t.Fatalf("входы: %+v", got)
	}
}

func TestEdgesMeasureInBackground(t *testing.T) {
	oldPing, oldCheck := edgePing, edgeCheck
	defer func() { edgePing, edgeCheck = oldPing, oldCheck }()
	edgePing = func(context.Context, string, string) (time.Duration, bool) { return 10 * time.Millisecond, true }
	edgeCheck = func(context.Context, string, string) (string, error) { return "ARN", nil }

	s := &Server{}
	if got := s.edges("w.example"); got != nil {
		t.Fatalf("до замера соединение не ждёт и идёт по DNS: %v", got)
	}
	waitEdges(t, s, "w.example")
	if got := s.edges("w.example"); len(got) != edgeKeep {
		t.Fatalf("после замера: %v", got)
	}
	if got := s.edges("other.example"); got != nil {
		t.Fatal("входы проверены для другого домена — сертификат мог не подойти")
	}
	if got := s.edges(""); got != nil {
		t.Fatalf("без воркера быстрых входов нет: %v", got)
	}

	s.resetEdges()
	s.dnsCache = map[string]dnsEntry{"w.example": {ips: []string{"5.5.5.5", s.Edges()[0].IP}, at: time.Now()}}
	got := s.cfAddrs(context.Background(), "w.example", []string{s.Edges()[0].IP})
	if !slices.Equal(got, []string{s.Edges()[0].IP, "5.5.5.5"}) {
		t.Fatalf("быстрый вход первым, адреса из DNS — следом, без повторов: %v", got)
	}
}

func waitEdges(t *testing.T, s *Server, host string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.edge.mu.Lock()
		done := s.edge.host == host && !s.edge.busy && len(s.edge.list) > 0
		s.edge.mu.Unlock()
		if done {
			return
		}
		s.edges(host)
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("входы в Cloudflare не измерены")
}

func TestStatusErrorFromEdge(t *testing.T) {
	cases := []struct {
		body string
		edge bool
		code int
	}{
		{"error code: 1034", true, 1034},
		{"<html>\r\n<head><title>403 Forbidden</title></head>", true, 0},
		{"<!DOCTYPE html><title>Attention Required! | Cloudflare</title>", true, 0},
		{"без ключа FI можно только сайты Telegram — скопируйте код воркера из FI", false, 0},
		{"", false, 0},
	}
	for _, c := range cases {
		se := &statusError{code: 403, body: c.body}
		if se.fromEdge() != c.edge || se.cfCode() != c.code {
			t.Errorf("%q: отказал Cloudflare = %v, код %d; ждали %v и %d", c.body, se.fromEdge(), se.cfCode(), c.edge, c.code)
		}
	}
}

// refusing — адреса поддельного Cloudflare, на которых он отказывает воркеру.
type refusing struct {
	mu  sync.Mutex
	ips []string
}

func (r *refusing) set(ips ...string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ips = ips
}

func (r *refusing) has(ip string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.ips, ip)
}

// edgeWorker — поддельный Cloudflare на двух адресах, 127.0.0.1 и 127.0.0.2: каждый — свой
// «вход». На адресах из refuse он отвечает за воркер отказом 1034, как «чужие» адреса
// Cloudflare, на остальных — как воркер FI, который пускает к сайту allowed.
func edgeWorker(t *testing.T, refuse *refusing, allowed string) {
	t.Helper()
	edgeWorkerWith(t, refuse, allowed, nil)
}

// edgeWorkerWith — как edgeWorker, но запрос сначала получает override: true — он ответил сам.
func edgeWorkerWith(t *testing.T, refuse *refusing, allowed string, override func(http.ResponseWriter, *http.Request) bool) {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		local, _, _ := net.SplitHostPort(r.Context().Value(http.LocalAddrContextKey).(net.Addr).String())
		switch {
		case override != nil && override(w, r):
		case refuse.has(local):
			w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "error code: 1034")
		case r.Header.Get("Upgrade") != "websocket":
			w.Header().Set("CF-RAY", "a58e5b4938eaec57-DME")
			io.WriteString(w, "FI worker: работает (версия 6, с ключом)")
		case r.URL.Query().Get("host") != allowed:
			w.WriteHeader(http.StatusForbidden)
			io.WriteString(w, "без ключа FI можно только сайты Telegram — скопируйте код воркера из FI")
		default:
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
				"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
			rw.Flush()
			io.Copy(io.Discard, conn)
		}
	})
	srv := httptest.NewTLSServer(handler) // 127.0.0.1
	t.Cleanup(srv.Close)
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	second, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.2", port))
	if err != nil {
		t.Skip("второй адрес 127.0.0.2 недоступен:", err)
	}
	srv2 := &http.Server{Handler: handler, TLSConfig: srv.TLS.Clone()}
	go srv2.ServeTLS(second, "", "")
	t.Cleanup(func() { srv2.Close() })

	certs := x509.NewCertPool()
	certs.AddCert(srv.Certificate())
	oldCAs, oldPort := rootCAs, workerPort
	rootCAs, workerPort = certs, port
	t.Cleanup(func() { rootCAs, workerPort = oldCAs, oldPort })
}

// TestRefusingEdgeIsNotWorkerVerdict — главное в 0.4.1: быстрый вход, на котором Cloudflare
// отвечает 403 за воркер, не выдаётся за отказ воркера. Соединение идёт через следующий адрес,
// а отказавший вход убирается.
func TestRefusingEdgeIsNotWorkerVerdict(t *testing.T) {
	const video = "rr4---sn-ixh7yn7e.googlevideo.com"
	refuse := &refusing{}
	refuse.set("127.0.0.2")
	edgeWorker(t, refuse, video)
	s := &Server{
		Worker:   "example.com",
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}
	s.edge = edgeState{host: "example.com", at: time.Now(), list: []Edge{{IP: "127.0.0.2", RTT: time.Millisecond}}}

	conn, err := s.dialWorkerTCP(context.Background(), video, 443)
	if err != nil {
		t.Fatalf("отказ входа в Cloudflare принят за отказ воркера: %v", err)
	}
	conn.Close()
	if got := s.Edges(); len(got) != 0 {
		t.Fatalf("отказавший вход остался в быстрых: %+v", got)
	}
	s.edge.mu.Lock()
	_, bad := s.edge.bad["127.0.0.2"]
	s.edge.mu.Unlock()
	if !bad {
		t.Fatal("отказавший вход не запомнен: следующий замер взял бы его снова")
	}

	// Отказ самого воркера — приговор, и с понятным текстом.
	_, err = s.dialWorkerTCP(context.Background(), "closed.example.org", 443)
	if err == nil || !strings.Contains(err.Error(), "не пускает") {
		t.Fatalf("отказ воркера: %v", err)
	}

	// Cloudflare отказывает на всех адресах — это не «вставьте код»: воркеру запрос не достался.
	refuse.set("127.0.0.1", "127.0.0.2")
	_, err = s.dialWorkerTCP(context.Background(), video, 443)
	if err == nil || strings.Contains(err.Error(), "не пускает") || !strings.Contains(err.Error(), "1034") {
		t.Fatalf("отказ Cloudflare на всех адресах: %v", err)
	}
}

// TestRefusingEdgeTelegram: путь в Telegram через воркер тоже обходит отказавший вход и
// убирает его, а адрес из DNS на паузу за это не ставит.
func TestRefusingEdgeTelegram(t *testing.T) {
	refuse := &refusing{}
	refuse.set("127.0.0.2")
	edgeWorker(t, refuse, "")
	s := &Server{
		Worker:   "example.com",
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}
	s.edge = edgeState{host: "example.com", at: time.Now(), list: []Edge{{IP: "127.0.0.2", RTT: time.Millisecond}}}
	up, err := s.dialWorker(context.Background(), poolKey{dc: 2})
	if err != nil {
		t.Fatalf("Telegram через воркер: %v", err)
	}
	up.close()
	if len(s.Edges()) != 0 || s.coolingDown("worker@127.0.0.1") {
		t.Fatalf("входы %+v, пауза адреса из DNS %v", s.Edges(), s.coolingDown("worker@127.0.0.1"))
	}
}

// TestCheckEdge: вход годен, только когда через него ответил сам воркер.
func TestCheckEdge(t *testing.T) {
	refuse := &refusing{}
	refuse.set("127.0.0.2")
	edgeWorker(t, refuse, "")
	colo, err := checkEdge(context.Background(), "127.0.0.1", "example.com")
	if err != nil || colo != "DME" {
		t.Fatalf("рабочий вход: %q, %v", colo, err)
	}
	var se *statusError
	if _, err := checkEdge(context.Background(), "127.0.0.2", "example.com"); !errors.As(err, &se) || !se.fromEdge() {
		t.Fatalf("вход с ошибкой 1034 принят за годный: %v", err)
	}
}

// TestSharedDomainsUseDNSOnly: общие домены Cloudflare идут по адресам из DNS — быстрые входы
// проверены только для своего воркера, а чужой домен тот же адрес может не обслуживать.
func TestSharedDomainsUseDNSOnly(t *testing.T) {
	refuse := &refusing{}
	refuse.set("127.0.0.2")
	edgeWorker(t, refuse, "")
	s := &Server{
		Worker:   "example.com",
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}
	// Быстрые входы отказывают: если бы dialHost начинал с них, до адреса из DNS он бы не дошёл.
	s.edge = edgeState{host: "example.com", at: time.Now(), list: []Edge{{IP: "127.0.0.2"}, {IP: "127.0.0.2"}}}
	ws, err := s.dialHost(context.Background(), "example.com", "/apiws")
	if err != nil {
		t.Fatalf("общий домен должен идти по адресу из DNS: %v", err)
	}
	ws.close()
}
