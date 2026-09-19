package autoselect

import (
	"testing"

	"fi/internal/probe"
	"fi/internal/selector"
)

func TestPick(t *testing.T) {
	res := func(host string, st probe.Status) probe.Result {
		return probe.Result{Target: probe.Target{Name: host, Group: "g", Host: host, Weight: 1}, Status: st}
	}
	baseline := selector.NewRun(selector.Baseline, []probe.Result{res("a", probe.OK), res("b", probe.TLSReset), res("dead", probe.TCPFail)})

	c, ok := Pick([]selector.Run{
		baseline,
		{Strategy: "broken", Error: "не запустилась"},
		selector.NewRun("A", []probe.Result{res("a", probe.OK), res("b", probe.OK), res("dead", probe.TCPFail)}),
	})
	if !ok || c.Best.Strategy != "A" || !c.Needed || len(c.Dropped) != 1 {
		t.Fatalf("Pick = %+v, %v", c, ok)
	}

	c, ok = Pick([]selector.Run{
		baseline,
		selector.NewRun("B", []probe.Result{res("a", probe.OK), res("b", probe.TLSReset), res("dead", probe.TCPFail)}),
	})
	if !ok || c.Needed {
		t.Errorf("стратегия не лучше сети без обхода, но Needed = %v", c.Needed)
	}

	if _, ok := Pick([]selector.Run{baseline, {Strategy: "broken", Error: "x"}}); ok {
		t.Error("ни одна стратегия не запустилась, но Pick вернул выбор")
	}
}

// TestPickSkipsInternetKillers: стратегия, при которой не открывается контрольный сайт,
// не выбирается, даже если остальное у неё открылось лучше.
func TestPickSkipsInternetKillers(t *testing.T) {
	res := func(host, group string, st probe.Status) probe.Result {
		return probe.Result{Target: probe.Target{Name: host, Group: group, Host: host, Weight: 1}, Status: st}
	}
	baseline := selector.NewRun(selector.Baseline, []probe.Result{res("a", "g", probe.OK), res("b", "g", probe.TLSReset), res("google", ReferenceGroup, probe.OK)})
	killer := selector.NewRun("ALT5", []probe.Result{res("a", "g", probe.OK), res("b", "g", probe.OK), res("google", ReferenceGroup, probe.TCPFail)})
	safe := selector.NewRun("EXP", []probe.Result{res("a", "g", probe.OK), res("b", "g", probe.TLSReset), res("google", ReferenceGroup, probe.OK)})

	c, ok := Pick([]selector.Run{baseline, killer, safe})
	if !ok || c.Best.Strategy != "EXP" || len(c.Broken) != 1 || c.Broken[0] != "ALT5" {
		t.Fatalf("Pick = %s, broken %v, %v", c.Best.Strategy, c.Broken, ok)
	}
}
