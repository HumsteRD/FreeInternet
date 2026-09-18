// Package daemon — ядро FI: держит движок и списки, проверяет сервисы,
// подбирает стратегию и отвечает окну в трее.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"fi/internal/appupdate"
	"fi/internal/autoselect"
	"fi/internal/config"
	"fi/internal/engine"
	"fi/internal/flowseal"
	"fi/internal/lists"
	"fi/internal/probe"
	"fi/internal/selector"
	"fi/internal/strategy"
	"fi/internal/tgproxy"
)

var (
	ErrBusy       = errors.New("уже идёт другая операция")
	errNoStrategy = errors.New("стратегия ещё не подобрана")
)

const (
	maxRestarts     = 3
	restartWindow   = 10 * time.Minute
	restartDelay    = 5 * time.Second
	autoSelectPause = time.Hour // автоподбор не чаще: он на несколько минут прерывает интернет
)

// Task — долгая операция, которую показывает окно.
type Task struct {
	Kind    string `json:"kind"`            // check, select, base, app
	Quick   bool   `json:"quick,omitempty"` // быстрый подбор среди лидеров прошлого
	Title   string `json:"title"`
	Done    int    `json:"done"`
	Total   int    `json:"total"`
	Current string `json:"current,omitempty"`
}

// Status — всё, что нужно главному окну.
type Status struct {
	Enabled      bool            `json:"enabled"`
	Running      bool            `json:"running"`
	Strategy     string          `json:"strategy,omitempty"`
	Strategies   int             `json:"strategies"`
	StrategyList []string        `json:"strategy_list"` // что можно выбрать вручную
	GameMode     string          `json:"game_mode"`
	Games        []string        `json:"games"`
	GameList     []strategy.Game `json:"game_profiles"`
	AutoFix      bool            `json:"auto_fix"`
	Error        string          `json:"error,omitempty"`
	Conflict     []string        `json:"conflict,omitempty"` // другие обходы, из-за которых движок не запущен
	Task         *Task           `json:"task,omitempty"`
	Services     []Service       `json:"services"`
	CheckedAt    time.Time       `json:"checked_at,omitzero"`
	Sites        []config.Site   `json:"sites"`
	Telegram     TelegramStatus  `json:"telegram"`
	Base         BaseStatus      `json:"base"`
	App          AppStatus       `json:"app"`
}

type Daemon struct {
	dataDir string
	log     *slog.Logger
	prober  *probe.Prober

	engineMu sync.Mutex // запуск и остановка движка — строго по одному
	tasks    sync.WaitGroup

	mu         sync.Mutex
	runCtx     context.Context
	cfg        config.Config
	strategies []strategy.Strategy
	baseErr    error
	proc       *engine.Process
	engineErr  string
	conflict   []string // другие обходы, найденные при последнем запуске движка
	restarts   []time.Time
	task       *Task
	cancelTask context.CancelFunc
	services   []Service
	checkedAt  time.Time
	lastSelect time.Time

	tg     *tgproxy.Server
	tgStop context.CancelFunc
	tgDone chan struct{}
	tgErr  string
	tgCF   []string  // общие домены Cloudflare (tg-ws-proxy)
	tgCFAt time.Time // когда их обновляли

	http          *http.Client
	base          manifest // установленный набор
	latest        flowseal.Release
	latestChecked time.Time
	updateErr     string

	ytNode   string    // видеосервер YouTube этой сети
	ytNodeAt time.Time // когда его узнавали

	appLatest  appupdate.Manifest
	appChecked time.Time
	appErr     string
}

// New загружает настройки и набор стратегий из dataDir.
func New(dataDir string, log *slog.Logger) (*Daemon, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	if err := secureDir(dataDir); err != nil {
		log.Warn("папка данных не защищена от записи пользователями", "dir", dataDir, "err", err)
	}
	removeOldBase(dataDir) // остаток прошлого обновления набора: при старте службы движок ещё не работает
	cfg, err := config.Load(configPath(dataDir))
	if err != nil {
		log.Warn("настройки повреждены, взяты по умолчанию", "err", err)
	}
	d := &Daemon{
		dataDir: dataDir,
		log:     log,
		prober:  probe.New(),
		cfg:     cfg,
		runCtx:  context.Background(),
		http:    &http.Client{Timeout: 5 * time.Minute},
	}
	d.strategies, d.base, d.baseErr = loadBase(cfg.BaseDir)
	d.ensureTelegramSecret()
	d.loadCFCache()
	return d, nil
}

func configPath(dataDir string) string { return filepath.Join(dataDir, "config.json") }

func (d *Daemon) listsDir() string { return filepath.Join(d.dataDir, "lists") }

func (d *Daemon) saveLocked() error { return config.Save(configPath(d.dataDir), d.cfg) }

// Run держит обход включённым по настройкам и периодически проверяет сервисы — до отмены ctx.
func (d *Daemon) Run(ctx context.Context) error {
	d.mu.Lock()
	d.runCtx = ctx
	enabled := d.cfg.Enabled
	d.mu.Unlock()
	if enabled {
		if err := d.startEngine(); err != nil {
			d.log.Warn("обход не запущен", "err", err)
		}
	}
	d.startTelegram()
	d.tasks.Add(1)
	go func() {
		defer d.tasks.Done()
		d.refreshCF(ctx, false)
	}()

	// Первая проверка вскоре после старта, чтобы окно сразу показало состояние.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			d.CancelTask()
			d.tasks.Wait()
			d.stopTelegram()
			d.stopEngine()
			return nil
		case <-timer.C:
			d.periodicCheck(ctx)
			d.maybeAutoUpdate(ctx)
			d.maybeAppUpdate(ctx)
			d.refreshCF(ctx, false)
			d.mu.Lock()
			every := max(time.Duration(d.cfg.CheckEvery), time.Minute)
			d.mu.Unlock()
			timer.Reset(every)
		}
	}
}

// Status возвращает снимок состояния.
func (d *Daemon) Status() Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := Status{
		Enabled:      d.cfg.Enabled,
		Running:      d.proc != nil,
		Strategy:     d.cfg.Strategy,
		Strategies:   len(d.strategies),
		StrategyList: strategyNames(d.strategies),
		GameMode:     d.cfg.GameMode,
		Games:        slices.Clone(d.cfg.Games),
		GameList:     strategy.Games,
		AutoFix:      d.cfg.AutoFix,
		Services:     slices.Clone(d.services),
		Conflict:     slices.Clone(d.conflict),
		CheckedAt:    d.checkedAt,
		Sites:        slices.Clone(d.cfg.Sites),
	}
	if d.task != nil {
		task := *d.task
		st.Task = &task
	}
	st.Telegram = d.telegramStatusLocked()
	st.Base = d.baseStatusLocked()
	st.App = d.appStatusLocked()
	switch {
	case d.baseErr != nil:
		st.Error = d.baseErr.Error()
	case d.engineErr != "":
		st.Error = d.engineErr
	case d.cfg.Enabled && d.cfg.Strategy == "":
		st.Error = errNoStrategy.Error()
	}
	return st
}

// SetEnabled включает или выключает обход.
func (d *Daemon) SetEnabled(on bool) error {
	d.mu.Lock()
	if d.task != nil && d.task.Kind == "select" {
		d.mu.Unlock()
		return ErrBusy
	}
	d.cfg.Enabled = on
	err := d.saveLocked()
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if !on {
		d.stopEngine()
		return nil
	}
	return d.startEngine()
}

// SetGameMode меняет игровой режим и перезапускает движок с новыми портами.
func (d *Daemon) SetGameMode(mode string) error {
	if !slices.Contains([]string{"off", "games", "tcp", "udp", "all"}, mode) {
		return fmt.Errorf("неизвестный игровой режим %q", mode)
	}
	d.mu.Lock()
	if d.task != nil && d.task.Kind == "select" {
		d.mu.Unlock()
		return ErrBusy
	}
	d.cfg.GameMode = mode
	err := d.saveLocked()
	restart := d.proc != nil
	d.mu.Unlock()
	if err != nil || !restart {
		return err
	}
	d.stopEngine()
	return d.startEngine()
}

func strategyNames(list []strategy.Strategy) []string {
	names := make([]string, len(list))
	for i, s := range list {
		names[i] = s.Name
	}
	return names
}

// SetStrategy ставит выбранную вручную стратегию и перезапускает движок.
func (d *Daemon) SetStrategy(name string) error {
	d.mu.Lock()
	if d.task != nil && d.task.Kind == "select" {
		d.mu.Unlock()
		return ErrBusy
	}
	if !hasStrategy(d.strategies, name) {
		d.mu.Unlock()
		return fmt.Errorf("стратегии %q нет в наборе", name)
	}
	d.cfg.Strategy = name
	err := d.saveLocked()
	enabled, running := d.cfg.Enabled, d.proc != nil
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if running {
		d.stopEngine()
	}
	if !enabled {
		return nil
	}
	return d.startEngine()
}

// SetAutoFix включает или выключает автоматический переподбор.
func (d *Daemon) SetAutoFix(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg.AutoFix = on
	return d.saveLocked()
}

// CancelTask отменяет подбор стратегии, если он идёт.
func (d *Daemon) CancelTask() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cancelTask != nil {
		d.cancelTask()
	}
}

// ─── Движок ───────────────────────────────────────────────────────────────

func (d *Daemon) startEngine() error {
	d.engineMu.Lock()
	defer d.engineMu.Unlock()

	d.mu.Lock()
	if d.proc != nil {
		d.mu.Unlock()
		return nil
	}
	exe, args, err := d.engineArgsLocked()
	d.mu.Unlock()

	var others []string
	if err == nil {
		if found, cerr := engine.Conflicts(); cerr == nil && len(found) > 0 {
			others = found
			err = fmt.Errorf("работает другой обход (%s) — остановите его", strings.Join(found, ", "))
		}
	}
	var proc *engine.Process
	if err == nil {
		proc, err = engine.Start(context.Background(), exe, args)
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	d.conflict = others
	if err != nil {
		d.engineErr = err.Error()
		return err
	}
	d.proc, d.engineErr = proc, ""
	d.log.Info("обход запущен", "strategy", d.cfg.Strategy)
	go d.watch(proc)
	return nil
}

func (d *Daemon) engineArgsLocked() (string, []string, error) {
	if d.baseErr != nil {
		return "", nil, d.baseErr
	}
	i := slices.IndexFunc(d.strategies, func(s strategy.Strategy) bool { return s.Name == d.cfg.Strategy })
	if i < 0 {
		return "", nil, errNoStrategy
	}
	if err := lists.Prepare(d.listsDir(), filepath.Join(d.cfg.BaseDir, "lists"), d.bypassHostsLocked(), d.cfg.Ipset); err != nil {
		return "", nil, fmt.Errorf("списки: %w", err)
	}
	gameTCP, gameUDP := strategy.GamePortsFor(d.cfg.GameMode, d.cfg.Games)
	args := d.strategies[i].Expand(strategy.Vars{
		Bin:     filepath.Join(d.cfg.BaseDir, "bin"),
		Lists:   d.listsDir(),
		GameTCP: gameTCP,
		GameUDP: gameUDP,
	})
	return filepath.Join(d.cfg.BaseDir, "bin", "winws.exe"), args, nil
}

func (d *Daemon) stopEngine() {
	d.engineMu.Lock()
	defer d.engineMu.Unlock()
	d.mu.Lock()
	proc := d.proc
	d.proc = nil
	d.mu.Unlock()
	if proc != nil {
		proc.Stop()
		d.log.Info("обход остановлен")
	}
}

// watch перезапускает движок, если он завершился сам. Больше maxRestarts падений
// за restartWindow — дело не в случайности: движок остаётся выключенным, причина видна в статусе.
func (d *Daemon) watch(proc *engine.Process) {
	<-proc.Done()
	d.mu.Lock()
	if d.proc != proc { // остановлен намеренно
		d.mu.Unlock()
		return
	}
	d.proc = nil
	d.engineErr = fmt.Sprintf("движок завершился: %v", proc.Err())
	now := time.Now()
	d.restarts = append(slices.DeleteFunc(d.restarts, func(t time.Time) bool { return now.Sub(t) > restartWindow }), now)
	retry := len(d.restarts) <= maxRestarts && d.cfg.Enabled
	d.mu.Unlock()

	d.log.Warn("движок завершился", "err", proc.Err(), "retry", retry)
	if !retry {
		return
	}
	time.Sleep(restartDelay)
	d.mu.Lock()
	retry = d.cfg.Enabled && d.proc == nil && d.task == nil
	d.mu.Unlock()
	if retry {
		d.startEngine()
	}
}

// ─── Проверки и подбор ────────────────────────────────────────────────────

func (d *Daemon) beginTask(t *Task) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.task != nil {
		return false
	}
	d.task = t
	return true
}

func (d *Daemon) endTask() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.task, d.cancelTask = nil, nil
}

func (d *Daemon) progress(done, total int, current string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.task != nil {
		d.task.Done, d.task.Total, d.task.Current = done, total, current
	}
}

// Check проверяет сервисы при текущем состоянии обхода.
func (d *Daemon) Check(ctx context.Context) ([]Service, error) {
	if !d.beginTask(&Task{Kind: "check", Title: "Проверка сервисов"}) {
		return nil, ErrBusy
	}
	defer d.endTask()
	// Проверку можно прервать: подбор стратегии, запущенный человеком, важнее.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	d.mu.Lock()
	d.cancelTask = cancel
	d.mu.Unlock()
	return d.check(ctx)
}

// preemptCheck прерывает идущую проверку сервисов и ждёт, пока она закончится. false — служба
// занята чем-то, что прерывать нельзя.
func (d *Daemon) preemptCheck() bool {
	d.mu.Lock()
	if d.task == nil || d.task.Kind != "check" || d.cancelTask == nil {
		free := d.task == nil
		d.mu.Unlock()
		return free
	}
	d.cancelTask()
	d.mu.Unlock()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
		d.mu.Lock()
		free := d.task == nil
		d.mu.Unlock()
		if free {
			return true
		}
	}
	return false
}

func (d *Daemon) check(ctx context.Context) ([]Service, error) {
	node := d.youtubeNode(ctx)
	d.mu.Lock()
	targets := probe.WithYouTubeNode(checkTargets(d.cfg.SiteHosts()), node)
	d.mu.Unlock()

	results := d.prober.Run(ctx, targets)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	services := summarize(results)
	d.mu.Lock()
	d.services, d.checkedAt = services, time.Now()
	d.mu.Unlock()
	return services, nil
}

// youtubeNode — видеосервер YouTube этой сети. Узнаём раз в сутки, после неудачи повторяем
// через час; пока узел не известен, проверяется общий redirector.
func (d *Daemon) youtubeNode(ctx context.Context) string {
	d.mu.Lock()
	host, at := d.ytNode, d.ytNodeAt
	d.mu.Unlock()
	again := 24 * time.Hour
	if host == "" {
		again = time.Hour
	}
	if !at.IsZero() && time.Since(at) < again {
		return host
	}

	found, err := probe.FindYouTubeNode(ctx, d.http)
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ytNodeAt = time.Now()
	if err != nil {
		if ctx.Err() == nil {
			d.log.Warn("видеосервер YouTube не определён", "err", err)
		}
		return d.ytNode
	}
	if found != d.ytNode {
		d.log.Info("видеосервер YouTube", "host", found)
	}
	d.ytNode = found
	return found
}

// recheckSoon пересчитывает состояние сервисов в фоне — например, после изменения списка сайтов.
func (d *Daemon) recheckSoon() {
	d.mu.Lock()
	ctx := d.runCtx
	d.tasks.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.tasks.Done()
		if _, err := d.Check(ctx); err != nil && !errors.Is(err, ErrBusy) && ctx.Err() == nil {
			d.log.Warn("проверка после изменения списка сайтов не удалась", "err", err)
		}
	}()
}

func (d *Daemon) periodicCheck(ctx context.Context) {
	d.mu.Lock()
	if d.cfg.Strategy == "" {
		// Первый запуск: до подбора проверять нечего, а долгая проверка без обхода
		// только держала бы занятой кнопку «Начать подбор».
		d.mu.Unlock()
		return
	}
	prev := d.services
	autoFix := d.cfg.AutoFix && d.proc != nil && time.Since(d.lastSelect) > autoSelectPause
	d.mu.Unlock()

	services, err := d.Check(ctx)
	if err != nil {
		if !errors.Is(err, ErrBusy) && ctx.Err() == nil {
			d.log.Warn("проверка не удалась", "err", err)
		}
		return
	}
	if autoFix && regressed(prev, services) {
		d.log.Info("сервис перестал работать — подбираю стратегию заново")
		if err := d.startSelect(true); err != nil {
			d.log.Warn("подбор не запущен", "err", err)
		}
	}
}

// StartAutoSelect запускает полный подбор стратегии в фоне; ход виден в Status().Task.
func (d *Daemon) StartAutoSelect() error { return d.startSelect(false) }

// startSelect запускает подбор. Быстрый (quick) проверяет только текущую стратегию и лидеров
// прошлого полного подбора — интернет прерывается на минуту, а не на несколько. Если лидеров
// не запомнено, подбор полный.
func (d *Daemon) startSelect(quick bool) error {
	task := &Task{Kind: "select", Title: "Подбор стратегии"}
	if !d.beginTask(task) && !(d.preemptCheck() && d.beginTask(task)) {
		return ErrBusy
	}
	d.mu.Lock()
	if d.baseErr != nil {
		err := d.baseErr
		d.mu.Unlock()
		d.endTask()
		return err
	}
	ctx, cancel := context.WithCancel(d.runCtx)
	strategies, current := d.strategies, d.cfg.Strategy
	if quick {
		if set := quickSet(d.strategies, d.cfg.Ranking, current); len(set) > 1 {
			strategies = set
			d.task.Title, d.task.Quick = "Быстрый подбор стратегии", true
		} else {
			quick = false
		}
	}
	opts := autoselect.Options{
		BaseDir:  d.cfg.BaseDir,
		GameMode: d.cfg.GameMode,
		Games:    d.cfg.Games,
		Ipset:    d.cfg.Ipset,
		Targets:  selectTargets(d.cfg.SiteHosts()),
		Prober:   d.prober,
		OnStart:  func(i, total int, name string) { d.progress(i, total, name) },
	}
	d.task.Total = len(strategies) + 1
	d.cancelTask = cancel
	d.lastSelect = time.Now()
	d.tasks.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.tasks.Done()
		defer cancel()
		// Узел YouTube узнаём до остановки движка: без обхода redirector может не отвечать.
		opts.Targets = probe.WithYouTubeNode(opts.Targets, d.youtubeNode(ctx))
		// Стратегии проверяются по очереди, каждая со своим движком: текущий мешал бы.
		d.stopEngine()
		if others, err := engine.Conflicts(); err == nil && len(others) > 0 {
			d.finishAutoSelect(ctx, nil, fmt.Errorf("работает другой обход (%s)", strings.Join(others, ", ")), quick, current)
			return
		}
		runs, err := autoselect.Run(ctx, opts, strategies)
		d.finishAutoSelect(ctx, runs, err, quick, current)
	}()
	return nil
}

func (d *Daemon) finishAutoSelect(ctx context.Context, runs []selector.Run, err error, quick bool, current string) {
	d.mu.Lock()
	switch {
	case err != nil:
		d.engineErr = "подбор не удался: " + err.Error()
		d.log.Error("подбор не удался", "err", err)
	case ctx.Err() != nil:
		d.log.Info("подбор отменён")
	default:
		choice, ok := autoselect.Pick(runs)
		switch {
		case !ok:
			d.engineErr = "ни одна стратегия не запустилась"
		case quick && choice.Best.Strategy == current:
			d.log.Info("быстрый подбор: лучше текущей стратегии не нашлось", "strategy", current)
		default:
			d.cfg.Strategy = choice.Best.Strategy
			if !quick { // лидеров запоминает только полный подбор: быстрый видел не все стратегии
				d.cfg.Ranking = choice.Ranking
			}
			score, maxScore, _ := choice.Best.Total()
			d.log.Info("стратегия подобрана", "strategy", d.cfg.Strategy, "quick", quick, "score", score, "max", maxScore, "needed", choice.Needed)
			if err := d.saveLocked(); err != nil {
				d.log.Error("настройки не сохранены", "err", err)
			}
		}
	}
	d.task, d.cancelTask = &Task{Kind: "check", Title: "Проверка сервисов"}, nil
	enabled, runCtx := d.cfg.Enabled, d.runCtx
	d.mu.Unlock()

	if enabled {
		if err := d.startEngine(); err != nil {
			d.log.Warn("обход не запущен", "err", err)
		}
	}
	if _, err := d.check(runCtx); err != nil && runCtx.Err() == nil {
		d.log.Warn("проверка после подбора не удалась", "err", err)
	}
	d.endTask()
}
