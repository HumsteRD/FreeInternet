package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"
	"time"

	"fi/internal/config"
	"fi/internal/lists"
	"fi/internal/probe"
)

func pauseDaemon(t *testing.T) *Daemon {
	t.Helper()
	stopped, cancel := context.WithCancel(context.Background())
	cancel() // проверки после переключения не ходят в сеть
	old := antiCheat
	antiCheat = func() string { return "" }
	t.Cleanup(func() { antiCheat = old })
	d := &Daemon{
		dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runCtx: stopped, prober: probe.New(), http: &http.Client{},
	}
	d.cfg.Enabled = true
	return d
}

func TestPause(t *testing.T) {
	d := pauseDaemon(t)
	if d.Pause(0) == nil || d.Pause(25*60) == nil {
		t.Fatal("пауза — от минуты до суток")
	}
	if !d.cfg.Enabled {
		t.Fatal("неверная пауза не должна выключать обход")
	}
	if err := d.Pause(15); err != nil {
		t.Fatal(err)
	}
	if d.cfg.Enabled || d.cfg.PausedUntil.IsZero() {
		t.Fatalf("на паузе: enabled=%v до %v", d.cfg.Enabled, d.cfg.PausedUntil)
	}

	d.resumeIfDue(false)
	if d.cfg.Enabled {
		t.Fatal("срок паузы ещё не вышел")
	}
	d.cfg.PausedUntil = time.Now().Add(-time.Second)
	d.resumeIfDue(true)
	if d.cfg.Enabled {
		t.Fatal("во время игры с античитом запуск застрял бы в драйвере — ждём её конца")
	}
	d.resumeIfDue(false) // запуск движка не удастся (набора нет), но обход должен стать включённым
	if !d.cfg.Enabled || !d.cfg.PausedUntil.IsZero() {
		t.Fatalf("пауза закончилась: enabled=%v до %v", d.cfg.Enabled, d.cfg.PausedUntil)
	}
}

func TestSetEnabledCancelsPauseAndRespectsGame(t *testing.T) {
	d := pauseDaemon(t)
	if err := d.Pause(60); err != nil {
		t.Fatal(err)
	}
	antiCheat = func() string { return "EasyAntiCheat" }
	if err := d.SetEnabled(true); err == nil || d.cfg.Enabled {
		t.Fatalf("включение во время игры застряло бы в драйвере: err=%v enabled=%v", err, d.cfg.Enabled)
	}
	if err := d.SetEnabled(false); err != nil {
		t.Fatalf("выключить можно и во время игры: %v", err)
	}
	if !d.cfg.PausedUntil.IsZero() {
		t.Fatal("выключение вручную отменяет автовключение")
	}
}

func TestSetIpset(t *testing.T) {
	d := pauseDaemon(t)
	if d.SetIpset("whatever") == nil {
		t.Fatal("неизвестный режим принят")
	}
	if d.SetIpset("loaded") == nil || d.cfg.Ipset == lists.IpsetLoaded {
		t.Fatal("без списка адресов в наборе режим ничего бы не менял")
	}
	if err := d.SetIpset("any"); err != nil || d.cfg.Ipset != lists.IpsetAny {
		t.Fatalf("режим any: %v %v", err, d.cfg.Ipset)
	}
	antiCheat = func() string { return "BattlEye" }
	if d.SetIpset("none") == nil {
		t.Fatal("во время игры перезапускать обход нельзя")
	}
}

func TestPresets(t *testing.T) {
	d := pauseDaemon(t)
	d.cfg.AddSite(config.Site{Host: "twitter.com", Note: "мой"})
	if d.SetPreset("nope", true) == nil {
		t.Fatal("неизвестный набор принят")
	}
	if err := d.SetPreset("social", true); err != nil {
		t.Fatal(err)
	}
	if st := d.presetStatusLocked(); !st[0].On || st[1].On {
		t.Fatalf("включён только первый набор: %+v", st)
	}
	if main := d.cfg.MainSites(); !slices.Contains(main, "x.com") || slices.Contains(main, "twimg.com") {
		t.Fatalf("проверять надо только главный домен набора: %v", main)
	}

	if err := d.SetPreset("social", false); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.cfg.MainSites(), []string{"twitter.com"}) || len(d.cfg.Sites) != 1 {
		t.Fatalf("после выключения набора должен остаться только свой сайт: %+v", d.cfg.Sites)
	}
	if st := d.presetStatusLocked(); st[0].On {
		t.Fatal("набор выключен")
	}

	// Каждый набор — настоящие домены: их примет проверка адресов.
	for _, p := range presets {
		if len(p.Hosts) == 0 || p.Sub == "" {
			t.Errorf("набор %s пуст", p.ID)
		}
		for _, h := range p.Hosts {
			if got, err := NormalizeHost(h); err != nil || got != h {
				t.Errorf("набор %s: домен %q → %q, %v", p.ID, h, got, err)
			}
		}
	}
}
