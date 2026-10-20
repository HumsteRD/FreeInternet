package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"fi/internal/probe"
)

func TestQUICOnlyFailureTurnsQUICOff(t *testing.T) {
	quicFail := []probe.Result{
		{Target: probe.Target{Name: "YouTube: сайт", Group: "youtube", Kind: probe.KindPage}, Status: probe.OK},
		{Target: probe.Target{Name: "YouTube: QUIC", Group: "youtube", Kind: probe.KindQUIC}, Status: probe.QUICFail},
	}
	dpi := append([]probe.Result{{Target: probe.Target{Name: "Discord: сайт", Group: "discord", Kind: probe.KindPage}, Status: probe.TLSReset}}, quicFail...)
	if !quicOnly(quicFail) || quicOnly(dpi) || quicOnly(quicFail[:1]) {
		t.Fatal("quicOnly: только QUIC — да; QUIC вместе с другой поломкой или без поломок — нет")
	}

	var applied []bool
	old := setBrowserQUIC
	setBrowserQUIC = func(allowed bool) error { applied = append(applied, allowed); return nil }
	defer func() { setBrowserQUIC = old }()

	stopped, cancel := context.WithCancel(context.Background())
	cancel() // перепроверка после выключения QUIC не ходит в сеть
	d := &Daemon{
		dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runCtx: stopped, prober: probe.New(), http: &http.Client{},
	}
	d.cfg.QUICAuto = true
	if !d.noteQUIC(quicFail) || d.cfg.BrowserQUICOff {
		t.Fatal("после одной проверки QUIC ещё не выключаем: проверка мигает")
	}
	d.noteQUIC(dpi) // настоящая поломка сбрасывает счёт
	d.noteQUIC(quicFail)
	if d.cfg.BrowserQUICOff {
		t.Fatal("счёт должен был начаться заново")
	}
	d.noteQUIC(quicFail)
	if !d.cfg.BrowserQUICOff || len(applied) != 1 || applied[0] {
		t.Fatalf("после двух проверок подряд QUIC в браузерах должен выключиться: off=%v applied=%v", d.cfg.BrowserQUICOff, applied)
	}
	d.tasks.Wait()

	if got := dropQUIC([]probe.Target{quicFail[0].Target, quicFail[1].Target}); len(got) != 1 || got[0].Kind == probe.KindQUIC {
		t.Fatalf("dropQUIC: %v", got)
	}
}
