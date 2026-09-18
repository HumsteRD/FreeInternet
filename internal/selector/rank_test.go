package selector

import (
	"slices"
	"testing"

	"fi/internal/probe"
)

func TestRank(t *testing.T) {
	run := func(name string, statuses ...probe.Status) Run {
		var results []probe.Result
		for i, s := range statuses {
			results = append(results, probe.Result{
				Target: probe.Target{Name: "цель", Group: "g", Kind: probe.KindTLS, Host: string(rune('a' + i)), Weight: 1},
				Status: s,
			})
		}
		return NewRun(name, results)
	}
	runs := []Run{
		run(Baseline, probe.OK, probe.OK, probe.OK), // без обхода в рейтинг не входит, даже лучший
		run("медленная", probe.OK, probe.Slow, probe.TLSReset),
		run("лучшая", probe.OK, probe.OK, probe.TLSReset),
		{Strategy: "не запустилась", Error: "winws завершился"},
		run("худшая", probe.TLSReset, probe.TLSReset, probe.TLSReset),
	}
	if got := Rank(runs, 2); !slices.Equal(got, []string{"лучшая", "медленная"}) {
		t.Errorf("Rank(2) = %q", got)
	}
	if got := Rank(runs, 10); len(got) != 3 {
		t.Errorf("Rank(10) = %q", got)
	}
}
