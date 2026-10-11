package tgproxy

import (
	"context"
	"errors"
	"testing"
	"time"
)

func routeAfter(wait time.Duration, up upstream, err error) func(context.Context, poolKey) (upstream, error) {
	return func(ctx context.Context, _ poolKey) (upstream, error) {
		select {
		case <-time.After(wait):
			return up, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func TestRaceSecondWinsWhenFirstHangs(t *testing.T) {
	first, second := &fakeUp{}, &fakeUp{}
	start := time.Now()
	up, viaSecond, err := raceRoutes(context.Background(), poolKey{dc: 2}, [2]func(context.Context, poolKey) (upstream, error){
		routeAfter(time.Minute, first, nil),
		routeAfter(10*time.Millisecond, second, nil),
	})
	if err != nil || up != second || !viaSecond {
		t.Fatalf("up=%v second=%v err=%v, want второй путь", up, viaSecond, err)
	}
	if took := time.Since(start); took > raceDelay+time.Second {
		t.Fatalf("ждали %s: второй путь должен стартовать через raceDelay", took)
	}
}

func TestRaceSecondStartsAtOnceWhenFirstFails(t *testing.T) {
	second := &fakeUp{}
	start := time.Now()
	up, viaSecond, err := raceRoutes(context.Background(), poolKey{dc: 2}, [2]func(context.Context, poolKey) (upstream, error){
		routeAfter(0, nil, errors.New("таймаут")),
		routeAfter(0, second, nil),
	})
	if err != nil || up != second || !viaSecond {
		t.Fatalf("up=%v second=%v err=%v", up, viaSecond, err)
	}
	if took := time.Since(start); took >= raceDelay {
		t.Fatalf("ждали %s: после ошибки первого второй не должен ждать raceDelay", took)
	}
}

func TestRaceLateWinnerIsClosed(t *testing.T) {
	first, late := &fakeUp{}, &fakeUp{}
	block := make(chan struct{})
	up, _, err := raceRoutes(context.Background(), poolKey{dc: 2}, [2]func(context.Context, poolKey) (upstream, error){
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
	_, _, err := raceRoutes(context.Background(), poolKey{dc: 2}, [2]func(context.Context, poolKey) (upstream, error){
		routeAfter(0, nil, errors.New("прямой")),
		routeAfter(0, nil, errNoCF),
	})
	if err == nil || errors.Is(err, errNoCF) {
		t.Fatalf("err=%v: нужна ошибка прямого пути без errNoCF", err)
	}
}

func TestDNSFailureIsRetriedSoon(t *testing.T) {
	if (dnsEntry{}).ttl() != dnsFailTTL || (dnsEntry{ips: []string{"1.1.1.1"}}).ttl() != dnsCacheTTL {
		t.Fatal("неудачный поиск адреса должен помниться меньше удачного")
	}
}
