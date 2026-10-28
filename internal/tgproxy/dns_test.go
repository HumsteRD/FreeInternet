package tgproxy

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestResolveKeepsLastGoodAddress(t *testing.T) {
	s := &Server{LookupFallback: func(context.Context, string) (string, error) { return "", errors.New("DoH молчит") }}
	// Сеть только что сменилась: кэш стёрт, DNS ещё не отвечает, но адрес воркера известен.
	s.lastGood = map[string][]string{"worker.invalid": {"203.0.113.7"}}
	if got := s.resolve(context.Background(), "worker.invalid", 0); !slices.Equal(got, []string{"203.0.113.7"}) {
		t.Fatalf("без DNS прокси должен взять прошлый адрес: %v", got)
	}
	if e := s.dnsCache["worker.invalid"]; !e.stale || e.ttl() != dnsFailTTL {
		t.Fatal("старый адрес — ненадолго: DNS надо спросить снова, как только оживёт")
	}
	if got := s.resolve(context.Background(), "never-seen.invalid", 0); len(got) != 0 {
		t.Fatalf("адреса, которых не знали, взяться неоткуда: %v", got)
	}
}
