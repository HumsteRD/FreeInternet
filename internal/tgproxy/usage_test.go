package tgproxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// clock подменяет часы учёта расхода.
func clock(t *testing.T, at time.Time) *time.Time {
	t.Helper()
	now := at
	old := usageNow
	usageNow = func() time.Time { return now }
	t.Cleanup(func() { usageNow = old })
	return &now
}

func TestUsageCountsPerUTCDay(t *testing.T) {
	now := clock(t, time.Date(2026, 11, 15, 23, 50, 0, 0, time.UTC))
	s := &Server{Worker: "w.example"}
	s.noteRequests(3)
	if u := s.Usage(); u.Requests != 3 || u.Day != "2026-11-15" || !u.ResetAt.Equal(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("расход: %+v", u)
	}
	// Служба перезапустилась: счёт продолжается, а не начинается с нуля.
	s2 := &Server{Worker: "w.example"}
	s2.SeedUsage(s.Usage())
	s2.noteRequests(1)
	if u := s2.Usage(); u.Requests != 4 {
		t.Fatalf("после перезапуска: %+v", u)
	}
	// Наступили новые сутки по UTC — Cloudflare счётчик обнулил, и FI тоже.
	*now = now.Add(15 * time.Minute)
	if u := s2.Usage(); u.Requests != 0 || u.Day != "2026-11-16" {
		t.Fatalf("новые сутки: %+v", u)
	}
	// Вчерашний счёт в новые сутки не переносится.
	s3 := &Server{}
	s3.SeedUsage(WorkerUsage{Day: "2026-11-15", Requests: 900})
	if u := s3.Usage(); u.Requests != 0 {
		t.Fatalf("вчерашний счёт перенесён: %+v", u)
	}
}

// TestWorkerLimit: Cloudflare ответил «лимит исчерпан» — к воркеру до полуночи UTC не ходим,
// Telegram идёт запасным путём, а сайтам говорим понятно, когда воркер вернётся.
func TestWorkerLimit(t *testing.T) {
	now := clock(t, time.Date(2026, 11, 15, 20, 0, 0, 0, time.UTC))
	hits := 0
	refuse := &refusing{}
	edgeWorkerWith(t, refuse, "telegram.org", func(w http.ResponseWriter, r *http.Request) bool {
		hits++
		w.WriteHeader(http.StatusTooManyRequests)
		io.WriteString(w, "error code: 1027")
		return true
	})
	s := &Server{
		Worker:   "example.com",
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}
	_, err := s.dialWorkerTCP(context.Background(), "telegram.org", 443)
	if err == nil || !strings.Contains(err.Error(), "лимит") {
		t.Fatalf("исчерпанный лимит: %v", err)
	}
	u := s.Usage()
	if u.LimitedUntil.IsZero() || u.LimitedUntil.Before(time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)) || u.Requests != 1 {
		t.Fatalf("расход после отказа: %+v", u)
	}
	// Пока лимит исчерпан, запросы к воркеру не тратятся ни сайтами, ни Telegram.
	if _, err := s.dialWorkerTCP(context.Background(), "telegram.org", 443); err == nil || hits != 1 {
		t.Fatalf("повторный запрос ушёл к воркеру: запросов %d, %v", hits, err)
	}
	if _, err := s.dialWorker(context.Background(), poolKey{dc: 2}); err == nil || hits != 1 {
		t.Fatalf("Telegram пошёл к воркеру с исчерпанным лимитом: запросов %d, %v", hits, err)
	}
	for _, r := range s.routeOrder() {
		if r.name == "worker" {
			t.Fatal("воркер с исчерпанным лимитом остался среди путей Telegram")
		}
	}
	// После полуночи UTC воркер снова в деле.
	*now = time.Date(2026, 11, 16, 0, 2, 0, 0, time.UTC)
	if s.workerLimited() || s.routeOrder()[0].name != "worker" {
		t.Fatalf("после полуночи UTC: лимит %v, пути %v", s.workerLimited(), s.routeOrder()[0].name)
	}
}

// TestWorkerSaving: запросов осталось мало — Telegram сперва пробует другие пути.
func TestWorkerSaving(t *testing.T) {
	clock(t, time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC))
	s := &Server{Worker: "w.example"}
	if s.routeOrder()[0].name != "worker" {
		t.Fatal("свой воркер должен идти первым")
	}
	s.noteRequests(int(workerSaveShare*WorkerDailyLimit) + 1)
	routes := s.routeOrder()
	if routes[0].name == "worker" || routes[len(routes)-1].name != "worker" {
		t.Fatalf("на исходе лимита воркер — последним: первый %s, последний %s", routes[0].name, routes[len(routes)-1].name)
	}
}

// TestCutDetected: соединение через воркер закрылось без кадра закрытия посреди передачи —
// это обрыв со стороны Cloudflare. Обычный конец передачи и простой обрывом не считаются.
func TestCutDetected(t *testing.T) {
	clock(t, time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC))
	mode := "cut"
	edgeWorkerWith(t, &refusing{}, "telegram.org", func(w http.ResponseWriter, r *http.Request) bool {
		if r.Header.Get("Upgrade") != "websocket" {
			return false
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return true
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
		rw.Flush()
		peer := &wsConn{conn: conn, br: rw.Reader}
		switch mode {
		case "cut": // данные шли — и соединение пропало
			peer.writeFrame(opBinary, make([]byte, 2*cutMinBytes))
		case "small": // оборвалось на мелочи: это не загрузка
			peer.writeFrame(opBinary, make([]byte, 100))
		case "closed": // сайт закрыл соединение сам: воркер прислал кадр закрытия
			peer.writeFrame(opBinary, make([]byte, 2*cutMinBytes))
			peer.writeFrame(opClose, []byte{0x03, 0xE8})
			time.Sleep(50 * time.Millisecond)
		}
		return true
	})
	s := &Server{
		Worker:   "example.com",
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}
	drain := func() {
		conn, err := s.dialWorkerTCP(context.Background(), "telegram.org", 443)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, conn)
		conn.Close()
	}
	drain()
	if u := s.Usage(); u.Cuts != 1 || u.LastCut.IsZero() {
		t.Fatalf("обрыв посреди передачи не замечен: %+v", u)
	}
	mode = "small"
	drain()
	mode = "closed"
	drain()
	if u := s.Usage(); u.Cuts != 1 {
		t.Fatalf("за обрыв принято обычное закрытие: %+v", u)
	}
	if u := s.Usage(); u.Requests != 3 {
		t.Fatalf("запросов к воркеру %d, ждали 3", u.Requests)
	}
}

var _ = httptest.NewServer // тесты выше пользуются поддельным Cloudflare из edge_test.go
