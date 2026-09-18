// Package selector оценивает прогоны проверок и выбирает лучшую стратегию.
package selector

import (
	"fmt"
	"sort"
	"time"

	"fi/internal/probe"
)

// Baseline — имя прогона без обхода.
const Baseline = "без обхода"

// Run — результаты проверок при одной стратегии.
type Run struct {
	Strategy string         `json:"strategy"`
	Error    string         `json:"error,omitempty"`
	Results  []probe.Result `json:"results,omitempty"`
	Groups   []GroupScore   `json:"groups,omitempty"`
}

// GroupScore — оценка группы целей (сервиса).
type GroupScore struct {
	Group  string        `json:"group"`
	Score  float64       `json:"score"`
	Max    float64       `json:"max"`
	Passed int           `json:"passed"`
	Total  int           `json:"total"`
	Time   time.Duration `json:"time"` // суммарное время успешных проверок — решает при равной оценке
}

// credit — доля веса цели, которую даёт статус.
func credit(s probe.Status) float64 {
	switch s {
	case probe.OK:
		return 1
	case probe.Slow:
		return 0.5
	default:
		return 0
	}
}

// NewRun считает оценки групп в порядке их первого появления.
func NewRun(strategy string, results []probe.Result) Run {
	run := Run{Strategy: strategy, Results: results}
	index := map[string]int{}
	for _, r := range results {
		i, ok := index[r.Target.Group]
		if !ok {
			i = len(run.Groups)
			index[r.Target.Group] = i
			run.Groups = append(run.Groups, GroupScore{Group: r.Target.Group})
		}
		g := &run.Groups[i]
		w := float64(r.Target.Weight)
		g.Max += w
		g.Total++
		if c := credit(r.Status); c > 0 {
			g.Score += c * w
			g.Time += r.Duration
		}
		if r.Status == probe.OK {
			g.Passed++
		}
	}
	return run
}

// Group возвращает оценку группы в прогоне.
func (r Run) Group(name string) (GroupScore, bool) {
	for _, g := range r.Groups {
		if g.Group == name {
			return g, true
		}
	}
	return GroupScore{}, false
}

// Total — суммарная оценка прогона и её максимум.
func (r Run) Total() (score, max float64, elapsed time.Duration) {
	for _, g := range r.Groups {
		score += g.Score
		max += g.Max
		elapsed += g.Time
	}
	return score, max, elapsed
}

// DropUnreachable убирает из оценки цели, которые не прошли ни в одном успешном прогоне:
// они не различают стратегии (сайт лежит, закрыт по IP или его ничто не чинит)
// и только занижают итог. Возвращает пересчитанные прогоны и убранные цели.
func DropUnreachable(runs []Run) ([]Run, []probe.Target) {
	var (
		failed map[string]bool
		order  []probe.Target
	)
	for _, run := range runs {
		if run.Error != "" {
			continue
		}
		current := make(map[string]bool)
		for _, r := range run.Results {
			if credit(r.Status) == 0 {
				current[targetKey(r.Target)] = true
			}
		}
		if failed == nil {
			failed = current
			for _, r := range run.Results {
				if current[targetKey(r.Target)] {
					order = append(order, r.Target)
				}
			}
			continue
		}
		for k := range failed {
			if !current[k] {
				delete(failed, k)
			}
		}
	}
	if len(failed) == 0 {
		return runs, nil
	}

	var dropped []probe.Target
	for _, t := range order {
		if failed[targetKey(t)] {
			dropped = append(dropped, t)
		}
	}
	out := make([]Run, len(runs))
	for i, run := range runs {
		if run.Error != "" {
			out[i] = run
			continue
		}
		kept := make([]probe.Result, 0, len(run.Results))
		for _, r := range run.Results {
			if !failed[targetKey(r.Target)] {
				kept = append(kept, r)
			}
		}
		out[i] = NewRun(run.Strategy, kept)
	}
	return out, dropped
}

func targetKey(t probe.Target) string {
	return fmt.Sprintf("%s|%s|%s|%d", t.Kind, t.Host, t.Path, t.Port)
}

// BestForGroup выбирает прогон с лучшей оценкой группы; при равенстве — быстрее.
// Прогон без обхода побеждает при равенстве: зачем обход, если и так работает.
func BestForGroup(runs []Run, group string) (Run, GroupScore, bool) {
	var (
		best      Run
		bestScore GroupScore
		found     bool
	)
	for _, run := range runs {
		g, ok := run.Group(group)
		if !ok {
			continue
		}
		if !found || better(g.Score, g.Time, run.Strategy, bestScore.Score, bestScore.Time, best.Strategy) {
			best, bestScore, found = run, g, true
		}
	}
	return best, bestScore, found
}

// BestOverall выбирает прогон с лучшей суммарной оценкой.
func BestOverall(runs []Run) (Run, bool) {
	var (
		best      Run
		bestScore float64
		bestTime  time.Duration
		found     bool
	)
	for _, run := range runs {
		if run.Error != "" {
			continue
		}
		s, _, t := run.Total()
		if !found || better(s, t, run.Strategy, bestScore, bestTime, best.Strategy) {
			best, bestScore, bestTime, found = run, s, t, true
		}
	}
	return best, found
}

// Rank — стратегии от лучшей к худшей по суммарной оценке, не больше n.
// Прогон без обхода и незапустившиеся стратегии не входят.
func Rank(runs []Run, n int) []string {
	var ok []Run
	for _, r := range runs {
		if r.Error == "" && r.Strategy != Baseline {
			ok = append(ok, r)
		}
	}
	sort.SliceStable(ok, func(i, j int) bool {
		si, _, ti := ok[i].Total()
		sj, _, tj := ok[j].Total()
		return better(si, ti, ok[i].Strategy, sj, tj, ok[j].Strategy)
	})
	names := make([]string, 0, min(n, len(ok)))
	for _, r := range ok[:min(n, len(ok))] {
		names = append(names, r.Strategy)
	}
	return names
}

func better(score float64, t time.Duration, strategy string, bestScore float64, bestTime time.Duration, bestStrategy string) bool {
	if score != bestScore {
		return score > bestScore
	}
	if strategy == Baseline || bestStrategy == Baseline {
		return strategy == Baseline
	}
	return t < bestTime
}
