// Package autoselect перебирает стратегии на сети пользователя и выбирает лучшую.
package autoselect

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"fi/internal/engine"
	"fi/internal/lists"
	"fi/internal/probe"
	"fi/internal/selector"
	"fi/internal/strategy"
)

type Options struct {
	BaseDir  string // набор: bin/winws.exe, lists/
	GameMode string
	Games    []string // игры для режима games
	Ipset    lists.IpsetMode
	Targets  []probe.Target
	Prober   *probe.Prober

	// BaselineName — имя первого прогона, без обхода.
	BaselineName string
	// OnStart вызывается перед прогоном i из total (0 — прогон без обхода).
	OnStart func(i, total int, name string)
	// OnRun вызывается после прогона.
	OnRun func(i, total int, run selector.Run)
	// Heal выгружает зависший драйвер WinDivert, чтобы winws можно было запустить снова.
	Heal func() error
}

// Run делает прогон без обхода и прогоны стратегий; при отмене ctx возвращает то, что успел.
// Хосты целей, которых нет в списках набора, на время перебора попадают в пользовательский
// список: иначе стратегии их бы не трогали и сравнивать было бы нечего.
func Run(ctx context.Context, o Options, strategies []strategy.Strategy) ([]selector.Run, error) {
	listsDir, err := os.MkdirTemp("", "fi-lists-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(listsDir)

	baseLists := filepath.Join(o.BaseDir, "lists")
	known, err := lists.LoadDomains(filepath.Join(baseLists, "list-general.txt"), filepath.Join(baseLists, "list-google.txt"))
	if err != nil {
		return nil, err
	}
	var extra []string
	for _, h := range probe.Hosts(o.Targets) {
		if !known.Matches(h) {
			extra = append(extra, h)
		}
	}
	if err := lists.Prepare(listsDir, baseLists, extra, o.Ipset); err != nil {
		return nil, err
	}

	total := len(strategies) + 1
	name := o.BaselineName
	if name == "" {
		name = selector.Baseline
	}
	o.start(0, total, name)
	runs := []selector.Run{selector.NewRun(name, o.Prober.Run(ctx, o.Targets))}
	o.done(0, total, runs[0])

	gameTCP, gameUDP := strategy.GamePortsFor(o.GameMode, o.Games)
	vars := strategy.Vars{Bin: filepath.Join(o.BaseDir, "bin"), Lists: listsDir, GameTCP: gameTCP, GameUDP: gameUDP}
	exe := filepath.Join(o.BaseDir, "bin", "winws.exe")
	for i, s := range strategies {
		if ctx.Err() != nil {
			break
		}
		o.start(i+1, total, s.Name)
		run := measure(ctx, o, exe, s.Name, s.Expand(vars))
		runs = append(runs, run)
		o.done(i+1, total, run)
	}
	return runs, nil
}

func measure(ctx context.Context, o Options, exe, name string, args []string) selector.Run {
	proc, err := engine.Start(ctx, exe, args)
	var re *engine.RunError
	if err != nil && o.Heal != nil && errors.As(err, &re) && re.WinDivert() && o.Heal() == nil {
		proc, err = engine.Start(ctx, exe, args)
	}
	if err != nil {
		return selector.Run{Strategy: name, Error: err.Error()}
	}
	defer proc.Stop()
	return selector.NewRun(name, o.Prober.Run(ctx, o.Targets))
}

func (o Options) start(i, total int, name string) {
	if o.OnStart != nil {
		o.OnStart(i, total, name)
	}
}

func (o Options) done(i, total int, run selector.Run) {
	if o.OnRun != nil {
		o.OnRun(i, total, run)
	}
}

// RankingSize — сколько лидеров подбора запоминать для быстрого переподбора.
const RankingSize = 5

// Choice — итог подбора.
type Choice struct {
	Best    selector.Run
	Needed  bool           // обход даёт больше, чем сеть без него
	Dropped []probe.Target // цели, не открывшиеся ни в одном прогоне
	Ranking []string       // лучшие стратегии, лучшая первая
	Broken  []string       // стратегии, при которых перестаёт открываться то, что работало без обхода
}

// Pick выбирает стратегию для постоянной работы — лучшую по сумме оценок.
// Первый прогон считается прогоном без обхода. false — ни одна стратегия не запустилась.
func Pick(runs []selector.Run) (Choice, bool) {
	scored, dropped := selector.DropUnreachable(runs)
	var candidates []selector.Run
	var broken []string
	// Контрольный сайт, который открывался без обхода, при стратегии открываться обязан: иначе
	// она ломает весь интернет (так бывает с syndata на всех соединениях), сколько бы очков ни набрала.
	baseRef := len(scored) > 0 && referenceOK(scored[0])
	for _, r := range scored[min(1, len(scored)):] {
		switch {
		case r.Error != "":
		case baseRef && !referenceOK(r):
			broken = append(broken, r.Strategy)
		default:
			candidates = append(candidates, r)
		}
	}
	best, ok := selector.BestOverall(candidates)
	if !ok {
		return Choice{Dropped: dropped, Broken: broken}, false
	}
	bestScore, _, _ := best.Total()
	baseScore, _, _ := scored[0].Total()
	return Choice{Best: best, Needed: bestScore > baseScore, Dropped: dropped, Ranking: selector.Rank(candidates, RankingSize), Broken: broken}, true
}

// ReferenceGroup — группа контрольных целей: они открываются почти в любой сети.
const ReferenceGroup = "reference"

// referenceOK — в прогоне открылись все контрольные цели (или их не было).
func referenceOK(r selector.Run) bool {
	for _, res := range r.Results {
		if res.Target.Group == ReferenceGroup && res.Status != probe.OK && res.Status != probe.Slow {
			return false
		}
	}
	return true
}
