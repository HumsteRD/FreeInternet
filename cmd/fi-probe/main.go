// fi-probe — стенд тестирования стратегий обхода DPI (этап 0).
//
// Сначала проверяет сервисы без обхода, затем по очереди запускает стратегии
// из папки zapret-discord-youtube и выбирает лучшую для каждого сервиса.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"fi/internal/autoselect"
	"fi/internal/engine"
	"fi/internal/lists"
	"fi/internal/probe"
	"fi/internal/selector"
	"fi/internal/strategy"
)

type options struct {
	flowsealDir string
	only        string
	list        bool
	baseline    bool
	hosting     bool
	ipset       string
	game        string
	jsonOut     string
	parallel    int
	retries     int
	timeout     time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.flowsealDir, "flowseal", "", "папка zapret-discord-youtube (bin, lists, general*.bat)")
	flag.StringVar(&o.only, "only", "", "проверить только стратегии, в имени которых есть одна из подстрок (через запятую)")
	flag.BoolVar(&o.list, "list", false, "показать стратегии из папки -flowseal и выйти")
	flag.BoolVar(&o.baseline, "baseline", false, "только проверка без обхода (права администратора не нужны)")
	flag.BoolVar(&o.hosting, "hosting", false, "добавить проверку зарубежных хостингов на обрыв 16–20 КБ (~36 целей, дольше)")
	flag.StringVar(&o.ipset, "ipset", "", "режим ipset: any, loaded, none (по умолчанию any с -hosting, иначе none)")
	flag.StringVar(&o.game, "game", "off", "игровой фильтр: off, tcp, udp, all")
	flag.StringVar(&o.jsonOut, "json", "", "путь для JSON-отчёта (по умолчанию reports/probe-<время>.json)")
	flag.IntVar(&o.parallel, "parallel", 8, "параллельных проверок")
	flag.IntVar(&o.retries, "retries", 1, "повторов после сбоя, похожего на случайный")
	flag.DurationVar(&o.timeout, "timeout", 5*time.Second, "таймаут соединения и замирания передачи")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, o); err != nil {
		fmt.Fprintln(os.Stderr, "Ошибка:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, o options) error {
	if o.list {
		return listStrategies(o.flowsealDir)
	}

	conflicts, err := engine.Conflicts()
	if err != nil {
		fmt.Println("⚠", err)
	}
	baselineName := selector.Baseline
	if len(conflicts) > 0 {
		if !o.baseline {
			return fmt.Errorf("уже работает другой обход (%s) — остановите его, иначе результаты будут неверными",
				strings.Join(conflicts, ", "))
		}
		// Без стратегий стенд ничего не запускает, поэтому просто честно меряем текущее состояние.
		baselineName = "текущий обход"
		fmt.Printf("⚠ Работает другой обход (%s): меряем, как справляется он, а не сеть без обхода.\n",
			strings.Join(conflicts, ", "))
	}

	var strategies []strategy.Strategy
	if !o.baseline {
		if o.flowsealDir == "" {
			return errors.New("укажите -flowseal <папка> или -baseline")
		}
		if !engine.IsElevated() {
			return errors.New("для проверки стратегий запустите от имени администратора (нужен WinDivert)")
		}
		all, err := strategy.ImportFlowsealDir(o.flowsealDir)
		if err != nil {
			return err
		}
		if strategies = filterStrategies(all, o.only); len(strategies) == 0 {
			return fmt.Errorf("под фильтр %q не попала ни одна стратегия", o.only)
		}
	}

	targets := probe.DefaultTargets()
	if host, err := probe.FindYouTubeNode(ctx, http.DefaultClient); err != nil {
		fmt.Println("⚠ видеосервер YouTube вашей сети не определён, проверяю общий:", err)
	} else {
		fmt.Println("Видеосервер YouTube вашей сети:", host)
		targets = probe.WithYouTubeNode(targets, host)
	}
	if o.hosting {
		suite, err := probe.LoadHostingSuite(ctx)
		if err != nil {
			fmt.Println("⚠ список хостингов не загружен:", err)
		}
		targets = append(targets, suite...)
	}

	prober := probe.New()
	prober.Parallel = o.parallel
	prober.Retries = o.retries
	prober.ConnectTimeout = o.timeout
	prober.StallTimeout = o.timeout

	var runs []selector.Run
	if o.baseline {
		fmt.Printf("Целей: %d. Проверка: %s…\n", len(targets), baselineName)
		runs = []selector.Run{selector.NewRun(baselineName, prober.Run(ctx, targets))}
		printRun(runs[0])
	} else {
		mode := lists.IpsetMode(o.ipset)
		if mode == "" {
			mode = lists.IpsetNone
			if o.hosting {
				mode = lists.IpsetAny
			}
		}
		runs, err = autoselect.Run(ctx, autoselect.Options{
			BaseDir:  o.flowsealDir,
			GameMode: o.game,
			Ipset:    mode,
			Targets:  targets,
			Prober:   prober,
			OnStart: func(i, total int, name string) {
				if i == 0 {
					fmt.Printf("Целей: %d, ipset=%s. Проверка: %s…\n", len(targets), mode, name)
					return
				}
				fmt.Printf("\n[%d/%d] %s\n", i, total-1, name)
			},
			OnRun: func(_, _ int, run selector.Run) { printRun(run) },
		}, strategies)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			fmt.Println("Прервано.")
		}
	}

	scored, dropped := runs, []probe.Target(nil)
	if len(runs) > 1 {
		scored, dropped = selector.DropUnreachable(runs)
	}
	printSummary(scored, dropped)
	return saveJSON(o.jsonOut, runs)
}

func listStrategies(dir string) error {
	if dir == "" {
		return errors.New("укажите -flowseal <папка>")
	}
	strategies, err := strategy.ImportFlowsealDir(dir)
	if err != nil {
		return err
	}
	for i, s := range strategies {
		profiles := 1
		for _, a := range s.Args {
			if a == "--new" {
				profiles++
			}
		}
		fmt.Printf("%2d. %-32s профилей: %d, аргументов: %d\n", i+1, s.Name, profiles, len(s.Args))
	}
	return nil
}

func filterStrategies(all []strategy.Strategy, only string) []strategy.Strategy {
	if only == "" {
		return all
	}
	var out []strategy.Strategy
	for _, s := range all {
		for _, sub := range strings.Split(only, ",") {
			if sub = strings.TrimSpace(sub); sub != "" && strings.Contains(strings.ToLower(s.Name), strings.ToLower(sub)) {
				out = append(out, s)
				break
			}
		}
	}
	return out
}

func saveJSON(path string, runs []selector.Run) error {
	if path == "" {
		path = filepath.Join("reports", "probe-"+time.Now().Format("20060102-150405")+".json")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(runs, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return err
	}
	fmt.Println("\nОтчёт:", path)
	return nil
}
