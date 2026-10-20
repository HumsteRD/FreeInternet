package tgproxy

import (
	"testing"
	"time"
)

func TestNetworkChangedDropsOldPath(t *testing.T) {
	d := &fakeDialer{}
	p := newPool(2, d.dial)
	defer p.close()
	s := &Server{pool: p}
	k := poolKey{dc: 2}
	p.use(k)
	waitFor(t, "пул наполнился", func() bool { return p.ready() == 2 })
	old := d.all()

	active := &fakeUp{}
	s.track(active, true)
	s.dnsCache = map[string]dnsEntry{"kws2.web.telegram.org": {ips: []string{"198.18.0.5"}, at: time.Now()}}
	s.markFailed("kws2.web.telegram.org@149.154.167.99")
	s.setPreferred("cf")

	s.NetworkChanged()

	for _, c := range old {
		if !c.closed.Load() {
			t.Fatal("соединение из пула, открытое через прежнюю сеть, не закрыто")
		}
	}
	if !active.closed.Load() {
		t.Fatal("активное соединение не закрыто — Telegram ждал бы его таймаута")
	}
	if s.dnsCache != nil || s.coolingDown("kws2.web.telegram.org@149.154.167.99") || s.preferred() != "" {
		t.Fatal("адреса и паузы прежней сети не забыты")
	}
	waitFor(t, "пул открыл новые соединения", func() bool { return p.ready() == 2 && len(d.all()) == 4 })
}

func TestWebProxyNetworkChanged(t *testing.T) {
	p := &WebProxy{}
	p.markBlocked("telegram.org")
	p.NetworkChanged()
	if p.directBlocked("telegram.org") {
		t.Fatal("после смены сети сайт снова надо пробовать напрямую")
	}
}
