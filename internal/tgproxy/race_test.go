package tgproxy

import (
	"context"
	"errors"
	"testing"
	"time"
)

type dialFunc = func(context.Context, poolKey) (upstream, error)

func routeAfter(wait time.Duration, up upstream, err error) dialFunc {
	return func(ctx context.Context, _ poolKey) (upstream, error) {
		select {
		case <-time.After(wait):
			return up, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func TestRaceNextWinsWhenFirstHangs(t *testing.T) {
	first, second := &fakeUp{}, &fakeUp{}
	start := time.Now()
	up, winner, err := raceRoutes(context.Background(), poolKey{dc: 2}, []dialFunc{
		routeAfter(time.Minute, first, nil),
		routeAfter(10*time.Millisecond, second, nil),
	})
	if err != nil || up != second || winner != 1 {
		t.Fatalf("up=%v winner=%d err=%v, want второй путь", up, winner, err)
	}
	if took := time.Since(start); took > raceDelay+time.Second {
		t.Fatalf("ждали %s: второй путь должен стартовать через raceDelay", took)
	}
}

func TestRaceThreeRoutesStaggered(t *testing.T) {
	third := &fakeUp{}
	start := time.Now()
	up, winner, err := raceRoutes(context.Background(), poolKey{dc: 2}, []dialFunc{
		routeAfter(time.Minute, &fakeUp{}, nil), // воркер завис
		routeAfter(time.Minute, &fakeUp{}, nil), // адрес Telegram закрыт
		routeAfter(10*time.Millisecond, third, nil),
	})
	if err != nil || up != third || winner != 2 {
		t.Fatalf("up=%v winner=%d err=%v", up, winner, err)
	}
	if took := time.Since(start); took > 2*raceDelay+time.Second {
		t.Fatalf("третий путь стартовал через %s, ждали около 2×raceDelay", took)
	}
}

func TestRaceNextStartsAtOnceWhenFirstFails(t *testing.T) {
	second := &fakeUp{}
	start := time.Now()
	up, winner, err := raceRoutes(context.Background(), poolKey{dc: 2}, []dialFunc{
		routeAfter(0, nil, errors.New("таймаут")),
		routeAfter(0, second, nil),
	})
	if err != nil || up != second || winner != 1 {
		t.Fatalf("up=%v winner=%d err=%v", up, winner, err)
	}
	if took := time.Since(start); took >= raceDelay {
		t.Fatalf("ждали %s: после ошибки первого второй не должен ждать raceDelay", took)
	}
}

func TestRaceLateWinnerIsClosed(t *testing.T) {
	first, late := &fakeUp{}, &fakeUp{}
	block := make(chan struct{})
	up, _, err := raceRoutes(context.Background(), poolKey{dc: 2}, []dialFunc{
		func(context.Context, poolKey) (upstream, error) { <-block; return late, nil },
		routeAfter(0, first, nil),
	})
	if err != nil || up != first {
		t.Fatalf("up=%v err=%v", up, err)
	}
	close(block) // первый путь всё-таки открылся — уже после победы второго
	deadline := time.Now().Add(2 * time.Second)
	for !late.closed.Load() {
		if time.Now().After(deadline) {
			t.Fatal("опоздавшее соединение не закрыто")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestRaceBothFail(t *testing.T) {
	_, _, err := raceRoutes(context.Background(), poolKey{dc: 2}, []dialFunc{
		routeAfter(0, nil, errors.New("прямой")),
		routeAfter(0, nil, errNoCF),
	})
	if err == nil || errors.Is(err, errNoCF) {
		t.Fatalf("err=%v: нужна ошибка прямого пути без errNoCF", err)
	}
}

func TestPreferredRouteGoesFirst(t *testing.T) {
	s := &Server{Worker: "w.example.workers.dev"}
	names := func() (out []string) {
		for _, r := range s.routeOrder() {
			out = append(out, r.name)
		}
		return
	}
	if got := names(); got[0] != "worker" || len(got) != 3 {
		t.Fatalf("по умолчанию: %v", got)
	}
	s.setPreferred("cf")
	if got := names(); got[0] != "cf" || got[1] != "worker" || got[2] != "ws" {
		t.Fatalf("недавний победитель первым: %v", got)
	}
}

func TestDNSFailureIsRetriedSoon(t *testing.T) {
	if (dnsEntry{}).ttl() != dnsFailTTL || (dnsEntry{ips: []string{"1.1.1.1"}}).ttl() != dnsCacheTTL {
		t.Fatal("неудачный поиск адреса должен помниться меньше удачного")
	}
}
