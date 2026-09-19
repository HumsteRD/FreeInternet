// fi — значок в трее и окно FI. Обходом управляет служба fi-service:
// окно показывает её состояние и передаёт команды, поэтому права администратора ему не нужны.
//
// Флаг --hidden запускает без окна — так окно стартует вместе с Windows.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"

	"fi/internal/diag"
	"fi/internal/ipc"
	"fi/internal/trayicon"
)

//go:embed all:ui
var uiFiles embed.FS

func main() {
	openTrayLog()
	// После обновления новое окно ждёт, пока закроется прежнее, — иначе приняло бы себя за второй экземпляр.
	for _, a := range os.Args[1:] {
		if v, ok := strings.CutPrefix(a, "--wait-pid="); ok {
			if pid, err := strconv.Atoi(v); err == nil {
				waitForProcess(uint32(pid), 15*time.Second)
			}
		}
	}

	ui, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		log.Fatal(err)
	}
	t := &tray{poke: make(chan struct{}, 1), notified: map[string]time.Time{}}
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old") // прежняя версия, которую установщик отодвинул при обновлении
		if info, err := os.Stat(exe); err == nil {
			t.exe, t.exeTime = exe, info.ModTime()
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/{method}", t.handleAPI)
	mux.HandleFunc("POST /app/hide", t.handleHide)
	mux.HandleFunc("POST /app/minimize", t.handleMinimize)
	mux.HandleFunc("POST /app/clipboard", t.handleClipboard)
	mux.HandleFunc("POST /app/autostart", t.handleAutostart)
	mux.HandleFunc("POST /app/telegram/open", t.handleTelegramOpen)
	mux.HandleFunc("POST /app/telegram/copy", t.handleTelegramCopy)
	mux.HandleFunc("POST /app/telegram/clients", t.handleTelegramClients)
	mux.HandleFunc("POST /app/telegram/choose", t.handleTelegramChoose)
	mux.HandleFunc("POST /app/diagnose", t.handleDiagnose)
	mux.HandleFunc("POST /app/report", t.handleReport)
	mux.HandleFunc("POST /app/logs", t.handleLogs)
	mux.HandleFunc("POST /app/sites/export", t.handleSitesExport)
	mux.HandleFunc("POST /app/sites/import", t.handleSitesImport)
	mux.Handle("/", http.FileServerFS(ui))

	t.notifier = notifications.New()
	t.app = application.New(application.Options{
		Name:        "FI",
		Description: "Обход блокировок и замедлений",
		Services:    []application.Service{application.NewService(t.notifier)},
		Assets: application.AssetOptions{
			Handler: mux,
			Middleware: func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasPrefix(r.URL.Path, "/wails") { // рантайм Wails: перетаскивание окна
						next.ServeHTTP(w, r)
						return
					}
					mux.ServeHTTP(w, r)
				})
			},
		},
		Windows: application.WindowsOptions{DisableQuitOnLastWindowClosed: true},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID:               "fi.tray",
			OnSecondInstanceLaunch: func(application.SecondInstanceData) { t.showWindow() },
		},
	})
	t.notifier.OnNotificationResponse(func(notifications.NotificationResult) { t.showWindow() })

	// Обычное окно: остаётся открытым, пока его не свернут или не уберут в трей,
	// и видно на панели задач, чтобы его не приходилось искать.
	t.window = t.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Name:             "main",
		Title:            "FI",
		Width:            400,
		Height:           640,
		MinWidth:         340,
		MinHeight:        480,
		MaxWidth:         900,
		MaxHeight:        1400,
		Frameless:        true,
		Hidden:           true,
		HideOnEscape:     true,
		BackgroundColour: application.NewRGB(0x0B, 0x0A, 0x0D),
		URL:              "/",
	})
	t.window.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		t.window.Hide()
		e.Cancel()
	})

	t.menu = t.app.NewMenu()
	t.menu.Add("Открыть FI").OnClick(func(*application.Context) { t.showWindow() })
	t.menu.Add("Проверить сейчас").OnClick(func(*application.Context) { go t.command("check", nil) })
	t.menu.Add("Добавить сайт из буфера").OnClick(func(*application.Context) { t.addFromClipboard() })
	t.menu.Add("Подключить Telegram").OnClick(func(*application.Context) {
		go func() {
			if err := t.openTelegram(""); err != nil {
				t.toast("Telegram", err.Error())
			}
		}()
	})
	t.pauseItem = t.menu.Add("Приостановить обход").OnClick(func(*application.Context) { go t.toggleEnabled() })
	t.menu.AddSeparator()
	t.menu.Add("Выход").OnClick(func(*application.Context) { t.app.Quit() })

	t.systray = t.app.SystemTray.New()
	t.systray.SetTooltip("FI")
	t.systray.SetIcon(trayicon.PNG(16, trayicon.Off, false))
	t.systray.SetDarkModeIcon(trayicon.PNG(16, trayicon.Off, true))
	t.systray.SetMenu(t.menu)
	t.systray.AttachWindow(t.window).WindowOffset(6)
	t.systray.OnClick(t.showFromTray) // клик по значку всегда выводит окно, а не прячет его

	// Обращаться к окну и трею можно только после старта Wails: до него InvokeSync падает,
	// а слежение начинается сразу и при неотвечающей службе доходит до значка за миллисекунды.
	started := make(chan struct{})
	ready := sync.OnceFunc(func() { close(started) })
	t.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) { ready() })
	go func() {
		<-started
		t.watch(!slices.Contains(os.Args[1:], "--hidden"))
	}()

	if err := t.app.Run(); err != nil {
		log.Fatal(err)
	}
}

type tray struct {
	app       *application.App
	window    *application.WebviewWindow
	systray   *application.SystemTray
	menu      *application.Menu
	pauseItem *application.MenuItem
	notifier  *notifications.NotificationService
	poke      chan struct{}
	notified  map[string]time.Time // только из watch
	exe       string               // свой файл: если установщик его заменил, окно перезапускается
	exeTime   time.Time

	mu           sync.Mutex
	enabled      bool
	enabledKnown bool
}

// status — поля статуса службы, которые нужны значку, меню и уведомлениям.
type status struct {
	Enabled   bool   `json:"enabled"`
	AutoFix   bool   `json:"auto_fix"`
	Strategy  string `json:"strategy"`
	Error     string `json:"error"`
	CheckedAt string `json:"checked_at"`
	Base      struct {
		Version string `json:"version"`
	} `json:"base"`
	Task *struct {
		Kind string `json:"kind"`
	} `json:"task"`
	Services []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
		State string `json:"state"`
	} `json:"services"`
}

func (st *status) selecting() bool { return st.Task != nil && st.Task.Kind == "select" }

func (t *tray) call(ctx context.Context, method string, params, result any) error {
	conn, err := ipc.Dial(ctx)
	if err != nil {
		return err
	}
	return ipc.Call(ctx, conn, method, params, result)
}

// command выполняет команду из меню и сразу обновляет значок.
func (t *tray) command(method string, params any) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := t.call(ctx, method, params, nil); err != nil {
		log.Printf("%s: %v", method, err)
	}
	t.refreshSoon()
}

func (t *tray) refreshSoon() {
	select {
	case t.poke <- struct{}{}:
	default:
	}
}

func (t *tray) toggleEnabled() {
	t.mu.Lock()
	on := !t.enabled
	t.mu.Unlock()
	t.command("set_enabled", map[string]bool{"on": on})
}

// showWindow выводит окно на передний план по центру экрана — так его открывают из «Пуска»,
// с рабочего стола, из меню и по уведомлению.
func (t *tray) showWindow() { t.show(false) }

// showFromTray выводит окно у значка в трее: когда его зовут оттуда, там его и ждут.
func (t *tray) showFromTray() { t.show(true) }

func (t *tray) show(atTray bool) {
	application.InvokeAsync(func() {
		switch {
		case t.window.IsMinimised():
			t.window.Restore()
		case !t.window.IsVisible():
			if atTray {
				t.systray.ShowWindow()
			} else {
				t.window.Center()
			}
		}
		roundCorners(t.window.NativeWindow())
		t.window.Show()
		t.window.Focus()
	})
}

func (t *tray) addFromClipboard() {
	text, _ := application.InvokeSyncWithResultAndOther(t.app.Clipboard.Text)
	t.showWindow()
	if text = strings.TrimSpace(text); text == "" {
		return
	}
	arg, _ := json.Marshal(text) // строка JSON — безопасный литерал для JS
	t.window.ExecJS("window.fi && window.fi.addSite(" + string(arg) + ")")
}

// watch держит значок, подсказку и меню в соответствии со статусом службы
// и сообщает уведомлениями о поломках и починке.
func (t *tray) watch(showOnStart bool) {
	// Сбой слежения не должен уносить с собой значок в трее и окно.
	defer func() {
		if r := recover(); r != nil {
			log.Printf("слежение за состоянием остановлено: %v", r)
		}
	}()
	if showOnStart {
		time.AfterFunc(700*time.Millisecond, t.showWindow)
	}
	var prev *status
	last, lastTip := trayicon.State(-1), ""
	for {
		if t.restartIfUpdated() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		st := &status{}
		err := t.call(ctx, "status", nil, st)
		cancel()
		if err != nil {
			st = nil
		}

		state, tip := trayState(st, err)
		if state != last {
			t.setIcon(state)
			last = state
		}
		if tip != lastTip {
			application.InvokeSync(func() { t.systray.SetTooltip(tip) })
			lastTip = tip
		}
		t.setPauseLabel(st != nil && st.Enabled)
		t.notifyChanges(prev, st)
		prev = st

		select {
		case <-t.poke:
		case <-time.After(3 * time.Second):
		}
	}
}

// notifyChanges сравнивает два статуса подряд: закончился подбор, сервис сломался или ожил.
func (t *tray) notifyChanges(prev, cur *status) {
	if prev == nil || cur == nil {
		return
	}
	if prev.Base.Version != "" && cur.Base.Version != "" && prev.Base.Version != cur.Base.Version {
		t.notify("Набор стратегий обновлён", "Версия "+cur.Base.Version+". Выбранная стратегия сохранена, если она осталась в наборе.")
	}
	if prev.selecting() && !cur.selecting() {
		if cur.Error == "" && cur.Strategy != "" {
			t.notify("Стратегия подобрана", "Обход работает на "+strategyLabel(cur.Strategy)+".")
		}
		return
	}
	// Сравнивать сервисы имеет смысл только после новой проверки при включённом обходе.
	if cur.CheckedAt == prev.CheckedAt || !cur.Enabled || cur.selecting() {
		return
	}
	was := make(map[string]string, len(prev.Services))
	for _, s := range prev.Services {
		was[s.ID] = s.State
	}
	for _, s := range cur.Services {
		broken := s.State == "fail" || s.State == "partial"
		switch before := was[s.ID]; {
		case before == "ok" && broken:
			body := "Откройте FI, чтобы подобрать стратегию заново."
			if cur.AutoFix {
				body = "FI подбирает стратегию заново."
			}
			t.notify("Не работает: "+s.Title, body)
		case (before == "fail" || before == "partial") && s.State == "ok":
			t.notify("Снова работает: "+s.Title, "")
		}
	}
}

func (t *tray) notify(title, body string) {
	if last, ok := t.notified[title]; ok && time.Since(last) < 10*time.Minute {
		return // одно и то же не чаще раза в 10 минут
	}
	t.notified[title] = time.Now()
	t.toast(title, body)
}

// toast показывает уведомление Windows сразу, без проверки на повторы.
func (t *tray) toast(title, body string) {
	err := t.notifier.SendNotification(notifications.NotificationOptions{
		ID:    fmt.Sprintf("fi-%d", time.Now().UnixNano()),
		Title: title,
		Body:  body,
	})
	if err != nil {
		log.Printf("уведомление: %v", err)
	}
}

// restartIfUpdated перезапускает окно на новую версию, если установщик обновления заменил fi.exe.
func (t *tray) restartIfUpdated() bool {
	if t.exe == "" {
		return false
	}
	info, err := os.Stat(t.exe)
	if err != nil || info.ModTime().Equal(t.exeTime) {
		return false // файла нет — установщик как раз подменяет его; проверим в следующий раз
	}
	t.exeTime = info.ModTime()
	args := []string{"--wait-pid=" + strconv.Itoa(os.Getpid())}
	if !application.InvokeSyncWithResult(t.window.IsVisible) {
		args = append(args, "--hidden")
	}
	if err := exec.Command(t.exe, args...).Start(); err != nil {
		log.Printf("перезапуск после обновления: %v", err)
		return false
	}
	t.app.Quit()
	return true
}

var generalName = regexp.MustCompile(`^general\s*\((.+)\)$`)

// strategyLabel: «general (ALT4)» → «ALT4».
func strategyLabel(name string) string {
	if m := generalName.FindStringSubmatch(name); m != nil {
		return m[1]
	}
	return name
}

func (t *tray) setIcon(state trayicon.State) {
	size := trayicon.FrameSize(trayIconSize())
	light, dark := trayicon.PNG(size, state, false), trayicon.PNG(size, state, true)
	application.InvokeSync(func() {
		t.systray.SetIcon(light)
		t.systray.SetDarkModeIcon(dark)
	})
}

func (t *tray) setPauseLabel(enabled bool) {
	t.mu.Lock()
	changed := !t.enabledKnown || t.enabled != enabled
	t.enabled, t.enabledKnown = enabled, true
	t.mu.Unlock()
	if !changed {
		return
	}
	label := "Включить обход"
	if enabled {
		label = "Приостановить обход"
	}
	application.InvokeSync(func() {
		t.pauseItem.SetLabel(label)
		t.menu.Update()
	})
}

// trayState выбирает состояние значка и подсказку по статусу службы.
func trayState(st *status, err error) (trayicon.State, string) {
	switch {
	case err != nil || st == nil:
		return trayicon.Off, "FI — служба не запущена"
	case st.selecting():
		return trayicon.Selecting, "FI — подбор стратегии"
	case st.Task != nil && st.Task.Kind == "app":
		return trayicon.Selecting, "FI — обновление"
	case st.Task != nil && st.Task.Kind == "base":
		return trayicon.Selecting, "FI — обновление набора стратегий"
	case !st.Enabled:
		return trayicon.Paused, "FI — обход выключен"
	case st.Error != "":
		return trayicon.Failure, tooltip("FI — " + st.Error)
	}
	state := trayicon.Active
	var broken []string
	for _, s := range st.Services {
		switch s.State {
		case "fail":
			state = trayicon.Failure
			broken = append(broken, s.Title)
		case "partial", "slow", "unknown":
			if state != trayicon.Failure {
				state = trayicon.Attention
			}
			broken = append(broken, s.Title)
		}
	}
	if len(broken) > 0 {
		return state, tooltip("FI — с перебоями: " + strings.Join(broken, ", "))
	}
	return trayicon.Active, "FI — обход работает"
}

// tooltip укладывает подсказку в предел Windows (127 символов).
func tooltip(s string) string {
	if r := []rune(s); len(r) > 120 {
		return string(r[:119]) + "…"
	}
	return s
}

// ─── HTTP для окна ────────────────────────────────────────────────────────

func (t *tray) handleAPI(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var params json.RawMessage
	if len(body) > 0 {
		params = body
	}

	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	var result json.RawMessage
	err = t.call(ctx, r.PathValue("method"), params, &result)

	var remote *ipc.RemoteError
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]json.RawMessage{"result": result})
	case errors.As(err, &remote):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": remote.Message})
	default:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Служба FI не отвечает", "code": "service_unavailable"})
	}
	if r.PathValue("method") != "status" {
		t.refreshSoon()
	}
}

func (t *tray) handleHide(w http.ResponseWriter, _ *http.Request) {
	application.InvokeAsync(func() { t.window.Hide() })
	w.WriteHeader(http.StatusNoContent)
}

func (t *tray) handleMinimize(w http.ResponseWriter, _ *http.Request) {
	application.InvokeAsync(func() { t.window.Minimise() })
	w.WriteHeader(http.StatusNoContent)
}

func (t *tray) handleClipboard(w http.ResponseWriter, _ *http.Request) {
	text, _ := application.InvokeSyncWithResultAndOther(t.app.Clipboard.Text)
	writeJSON(w, http.StatusOK, map[string]string{"text": text})
}

// handleAutostart без тела сообщает, запускается ли окно вместе с Windows;
// с телом {"on": true|false} — включает или выключает автозапуск.
func (t *tray) handleAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On *bool `json:"on"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var err error
	switch {
	case req.On == nil:
	case *req.On:
		err = t.app.Autostart.EnableWithOptions(application.AutostartOptions{Arguments: []string{"--hidden"}})
	default:
		err = t.app.Autostart.Disable()
	}
	enabled, statusErr := t.app.Autostart.IsEnabled()
	resp := map[string]any{"enabled": enabled}
	if err = errors.Join(err, statusErr); err != nil {
		resp["error"] = "Не удалось изменить автозапуск: " + err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// telegramLink берёт у службы ссылку tg://proxy.
func (t *tray) telegramLink() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var st struct {
		Telegram struct {
			Enabled bool   `json:"enabled"`
			Link    string `json:"link"`
		} `json:"telegram"`
	}
	if err := t.call(ctx, "status", nil, &st); err != nil {
		return "", errors.New("служба FI не отвечает")
	}
	if !st.Telegram.Enabled || !strings.HasPrefix(st.Telegram.Link, "tg://proxy?") {
		return "", errors.New("прокси для Telegram выключен — включите его в настройках")
	}
	return st.Telegram.Link, nil
}

// openTelegram открывает ссылку прокси в клиенте Telegram, и тот сам предложит включить прокси.
// path — выбранный клиент; пусто — последний выбранный, найденный на компьютере или тот,
// что Windows открывает для ссылок tg://.
func (t *tray) openTelegram(path string) error {
	link, err := t.telegramLink()
	if err != nil {
		return err
	}
	if path == "" {
		path = loadTraySettings().TelegramClient
	}
	if path == "" {
		if clients := findTelegramClients(); len(clients) > 0 {
			path = clients[0].Path // первыми идут запущенные
		}
	}
	if path != "" {
		if err := openInClient(path, link); err == nil {
			saveTraySettings(traySettings{TelegramClient: path})
			return nil
		}
	}
	if err := t.app.Browser.OpenURL(link); err != nil {
		return fmt.Errorf("не удалось открыть Telegram — установлен ли Telegram Desktop? (%w)", err)
	}
	return nil
}

func (t *tray) handleTelegramOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
	resp := map[string]string{}
	if err := t.openTelegram(req.Path); err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleTelegramClients — клиенты Telegram, найденные на компьютере, и последний выбранный.
func (t *tray) handleTelegramClients(w http.ResponseWriter, _ *http.Request) {
	s := loadTraySettings()
	writeJSON(w, http.StatusOK, map[string]any{"clients": findTelegramClients(s.TelegramClient), "preferred": s.TelegramClient})
}

// handleTelegramChoose даёт выбрать клиент Telegram вручную и сразу открывает в нём ссылку прокси.
func (t *tray) handleTelegramChoose(w http.ResponseWriter, _ *http.Request) {
	path, err := t.app.Dialog.OpenFile().
		SetTitle("Выберите программу Telegram (например, AyuGram.exe)").
		AddFilter("Программы", "*.exe").
		CanChooseFiles(true).
		AttachToWindow(t.window).
		PromptForSingleSelection()
	resp := map[string]string{}
	switch {
	case err != nil:
		resp["error"] = err.Error()
	case path == "":
		// окно выбора закрыли
	default:
		if err := t.openTelegram(path); err != nil {
			resp["error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (t *tray) handleTelegramCopy(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]string{}
	link, err := t.telegramLink()
	if err == nil && !application.InvokeSyncWithResult(func() bool { return t.app.Clipboard.SetText(link) }) {
		err = errors.New("не удалось скопировать ссылку")
	}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDiagnose берёт диагностику у службы и дополняет её настройками пользователя (прокси):
// служба работает от имени системы и их не видит.
func (t *tray) handleDiagnose(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var report diag.Report
	err := t.call(ctx, "diagnose", nil, &report)
	var remote *ipc.RemoteError
	switch {
	case errors.As(err, &remote):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": remote.Message})
		return
	case err != nil:
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Служба FI не отвечает", "code": "service_unavailable"})
		return
	}
	report.Findings = append(report.Findings, diag.EvaluateUser(diag.CollectUser())...)
	writeJSON(w, http.StatusOK, map[string]any{"result": report})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}
