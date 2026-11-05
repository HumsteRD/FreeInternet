package autoselect

import (
	"testing"
	"time"

	"fi/internal/probe"
	"fi/internal/selector"
)

func TestScreenTargets(t *testing.T) {
	yt := probe.Target{Name: "YouTube", Group: "youtube", Host: "www.youtube.com", Weight: 2}
	closed := probe.Target{Name: "Instagram", Group: "blocked", Host: "www.instagram.com", Weight: 1}
	gone := probe.Target{Name: "старый сайт", Group: "user", Host: "gone.example", Weight: 1}
	dpi := probe.Target{Name: "X", Group: "blocked", Host: "x.com", Weight: 1}
	baseline := selector.NewRun(selector.Baseline, []probe.Result{
		{Target: yt, Status: probe.TLSTimeout},
		{Target: closed, Status: probe.TCPFail},
		{Target: gone, Status: probe.DNSFail},
		{Target: dpi, Status: probe.TLSReset},
	})
	got := screenTargets([]probe.Target{yt, closed, gone, dpi}, baseline)
	if len(got) != 2 || got[0] != yt || got[1] != dpi {
		t.Fatalf("в отборе должны остаться только цели, которые может открыть стратегия: %+v", got)
	}
}

func TestScreenProber(t *testing.T) {
	p := probe.New()
	q := screenProber(p)
	if q.Retries != 0 || q.ConnectTimeout >= p.ConnectTimeout || q.MaxBytes >= p.MaxBytes || q.StallTimeout > 3*time.Second {
		t.Fatalf("отбор должен быть короче полной проверки: %+v", q)
	}
	if p.Retries == 0 {
		t.Fatal("отбор не должен менять полную проверку")
	}
}
