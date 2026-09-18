package selector

import (
	"testing"
	"time"

	"fi/internal/probe"
)

func result(group string, weight int, st probe.Status, d time.Duration) probe.Result {
	return probe.Result{Target: probe.Target{Group: group, Weight: weight}, Status: st, Duration: d}
}

func TestDropUnreachable(t *testing.T) {
	dead := probe.Target{Name: "dead", Group: "hosting", Host: "dead.example", Weight: 1}
	alive := probe.Target{Name: "alive", Group: "hosting", Host: "alive.example", Weight: 1}
	res := func(tg probe.Target, st probe.Status) probe.Result { return probe.Result{Target: tg, Status: st} }
	runs := []Run{
		NewRun(Baseline, []probe.Result{res(dead, probe.TCPFail), res(alive, probe.TLSReset)}),
		NewRun("A", []probe.Result{res(dead, probe.TLSTimeout), res(alive, probe.OK)}),
		{Strategy: "broken", Error: "не запустилась"},
	}

	scored, dropped := DropUnreachable(runs)
	if len(dropped) != 1 || dropped[0].Host != "dead.example" {
		t.Fatalf("dropped = %+v", dropped)
	}
	if g, _ := scored[1].Group("hosting"); g.Total != 1 || g.Passed != 1 {
		t.Errorf("A hosting = %+v, want 1/1", g)
	}
	if scored[2].Error == "" {
		t.Error("прогон с ошибкой потерян")
	}
}

func TestBestForGroup(t *testing.T) {
	runs := []Run{
		NewRun(Baseline, []probe.Result{
			result("youtube", 3, probe.TLSReset, 0),
			result("youtube", 1, probe.OK, time.Second),
			result("discord", 2, probe.OK, time.Second),
		}),
		NewRun("A", []probe.Result{
			result("youtube", 3, probe.OK, 2*time.Second),
			result("youtube", 1, probe.Slow, time.Second),
			result("discord", 2, probe.OK, 500*time.Millisecond),
		}),
		NewRun("B", []probe.Result{
			result("youtube", 3, probe.OK, time.Second),
			result("youtube", 1, probe.Slow, time.Second),
			result("discord", 2, probe.TLSTimeout, 0),
		}),
	}

	if best, g, _ := BestForGroup(runs, "youtube"); best.Strategy != "B" || g.Score != 3.5 {
		t.Errorf("youtube: %s %.1f, want B 3.5 (равная оценка с A, но быстрее)", best.Strategy, g.Score)
	}
	if best, _, _ := BestForGroup(runs, "discord"); best.Strategy != Baseline {
		t.Errorf("discord: %s, want %s (работает и без обхода)", best.Strategy, Baseline)
	}
	if best, _ := BestOverall(runs); best.Strategy != "A" {
		t.Errorf("overall: %s, want A", best.Strategy)
	}
}
