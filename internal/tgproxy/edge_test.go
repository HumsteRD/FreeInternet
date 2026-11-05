package tgproxy

import (
	"context"
	"errors"
	"os"
	"slices"
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
	rtt := map[string]time.Duration{"1.1.1.1": 70 * time.Millisecond, "2.2.2.2": 30 * time.Millisecond, "3.3.3.3": 40 * time.Millisecond, "4.4.4.4": 50 * time.Millisecond}
	edgePing = func(_ context.Context, ip, host string) (time.Duration, bool) {
		if ip == "3.3.3.3" {
			return 0, false // быстрый, но воркер через него не отдаётся: рукопожатие не сошлось
		}
		d, ok := rtt[ip]
		return d, ok
	}
	edgeCheck = func(_ context.Context, ip, host string) (string, error) {
		if ip == "1.1.1.1" {
			return "", errors.New("trace не ответил") // код дата-центра — только для окна
		}
		return map[string]string{"2.2.2.2": "DME"}[ip], nil
	}
	got := pickEdges(context.Background(), "w.example", []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4", "9.9.9.9"})
	var ips []string
	for _, e := range got {
		ips = append(ips, e.IP)
	}
	if !slices.Equal(ips, []string{"2.2.2.2", "4.4.4.4", "1.1.1.1"}) || got[0].Colo != "DME" || got[2].Colo != "" {
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
	deadline := time.Now().Add(2 * time.Second)
	for len(s.edges("w.example")) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := s.edges("w.example"); len(got) != edgeKeep {
		t.Fatalf("после замера: %v", got)
	}
	if got := s.edges("other.example"); got != nil {
		t.Fatal("входы проверены для другого домена — сертификат мог не подойти")
	}

	s.resetEdges()
	s.dnsCache = map[string]dnsEntry{"w.example": {ips: []string{"5.5.5.5", s.Edges()[0].IP}, at: time.Now()}}
	got := s.cfAddrs(context.Background(), "w.example", []string{s.Edges()[0].IP})
	if !slices.Equal(got, []string{s.Edges()[0].IP, "5.5.5.5"}) {
		t.Fatalf("быстрый вход первым, адреса из DNS — следом, без повторов: %v", got)
	}
}
