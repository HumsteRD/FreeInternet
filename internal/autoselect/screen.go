package autoselect

import (
	"context"
	"slices"
	"sort"
	"time"

	"fi/internal/probe"
	"fi/internal/selector"
	"fi/internal/strategy"
)

// Отбор. Полная проверка стратегии — 15–25 секунд: страницы читаются целиком, у сбоев есть
// повтор, а закрытые по адресу сайты ждут таймаута. На 26 стратегиях это 9 минут без обхода.
// Отбор гоняет все стратегии по тем же целям, но коротко: без повторов, с короткими таймаутами
// и без целей, которые без обхода не открылись по причине, которую стратегия не лечит (адрес
// закрыт, сайта нет в DNS). Полную проверку проходят только лидеры отбора.

// screenProber — короткие таймауты отбора.
func screenProber(p *probe.Prober) *probe.Prober {
	q := *p
	q.ConnectTimeout, q.StallTimeout, q.Retries, q.MaxBytes = 3*time.Second, 3*time.Second, 0, 64<<10
	return &q
}

// screenTargets — цели отбора: всё, кроме того, что в прогоне без обхода не открылось
// по причине, которую стратегия не исправит.
func screenTargets(targets []probe.Target, baseline selector.Run) []probe.Target {
	dead := map[probe.Target]bool{}
	for _, r := range baseline.Results {
		switch r.Status {
		case probe.TCPFail, probe.IPBlocked, probe.DNSFail, probe.DNSSpoof, probe.TLSCert, probe.HTTPBlocked:
			dead[r.Target] = true
		}
	}
	return slices.DeleteFunc(slices.Clone(targets), func(t probe.Target) bool { return dead[t] })
}

// screen прогоняет отбор и возвращает лидеров — в порядке оценки, лучший первый.
func screen(ctx context.Context, o Options, exe string, vars strategy.Vars, strategies []strategy.Strategy, baseline selector.Run, total int) []strategy.Strategy {
	short := o
	short.Prober = screenProber(o.Prober)
	short.Targets = screenTargets(o.Targets, baseline)
	type scored struct {
		s   strategy.Strategy
		run selector.Run
	}
	var runs []scored
	for i, s := range strategies {
		if ctx.Err() != nil {
			break
		}
		o.start(i+1, total, s.Name)
		run := measure(ctx, short, exe, s.Name, s.Expand(vars))
		o.done(i+1, total, run)
		if run.Error == "" {
			runs = append(runs, scored{s, run})
		}
	}
	sort.SliceStable(runs, func(i, j int) bool {
		a, _, ta := runs[i].run.Total()
		b, _, tb := runs[j].run.Total()
		if a != b {
			return a > b
		}
		return ta < tb
	})
	finalists := make([]strategy.Strategy, 0, o.Finalists)
	for _, r := range runs[:min(len(runs), o.Finalists)] {
		finalists = append(finalists, r.s)
	}
	return finalists
}
