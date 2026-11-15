package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fi/internal/cfdeploy"
	"fi/internal/probe"
	"fi/internal/tgproxy"
)

// Свой Cloudflare Worker без возни. Раньше воркер ставили руками: создать в панели Cloudflare,
// вставить код, скопировать адрес — или создать токен и вписать его в окно; после каждого
// обновления кода всё повторялось. Знакомым автора это оказалось «геморно и неудобно».
//
// Теперь человек проходит три шага мастера (аккаунт, токен, «Создать воркер»), а дальше служба
// сама: проверяет токен, загружает воркер, ждёт, пока адрес оживёт, вписывает его в настройки.
// Токен с согласия человека остаётся на компьютере — в папке, которую читают только система и
// администраторы, — и FI сам обновляет код воркера, когда в новой версии FI он меняется.
//
// Здесь же расход бесплатного тарифа (см. tgproxy/usage.go): служба хранит счётчик между
// перезапусками и отдаёт окну, чтобы лимит не кончался незаметно.

// CloudflareStatus — воркер, токен и расход бесплатного тарифа: для окна.
type CloudflareStatus struct {
	// HasToken — токен сохранён: FI обновляет воркер сам. TokenError — Cloudflare его больше
	// не принимает (отозван или истёк): окно просит новый.
	HasToken   bool   `json:"has_token"`
	TokenError string `json:"token_error,omitempty"`
	Region     string `json:"region,omitempty"`  // где закреплён воркер: de, nl, us
	Version    int    `json:"version,omitempty"` // версия кода в воркере; 0 — не знаем
	Latest     int    `json:"latest"`            // версия кода в этой сборке FI
	// Setup — идёт или только что закончилась настройка воркера по токену.
	Setup *WorkerSetup `json:"setup,omitempty"`
	// Расход: запросы за сутки по счёту FI, исчерпан ли лимит, обрывы загрузок.
	tgproxy.WorkerUsage
	Limit  int  `json:"limit"`
	Saving bool `json:"saving,omitempty"` // запросов осталось мало: Telegram идёт другим путём
	// Ссылки для мастера настройки.
	SignUpURL string `json:"sign_up_url"`
	TokenURL  string `json:"token_url"`
}

// WorkerSetup — ход настройки воркера.
type WorkerSetup struct {
	Busy   bool   `json:"busy"`
	Step   string `json:"step,omitempty"`   // token, upload, wait, check
	Update bool   `json:"update,omitempty"` // это обновление кода, а не первая настройка
	Error  string `json:"error,omitempty"`
	Done   bool   `json:"done,omitempty"`   // закончилась удачно
	Domain string `json:"domain,omitempty"` // адрес созданного воркера
}

const (
	usageFile = "worker-usage.json"
	// workerUpdateEvery — как часто пробовать обновить воркер сохранённым токеном, если прошлая
	// попытка не удалась (нет сети, Cloudflare не ответил).
	workerUpdateEvery = 6 * time.Hour
	workerWaitFor     = 60 * time.Second // сколько ждать, пока новый код воркера начнёт отвечать
)

// workerPoll — как часто спрашивать воркер, ожил ли он; в тестах меньше.
var workerPoll = 2 * time.Second

// pumpProbeHost — сайт, рукопожатием TLS с которым проверяется пересылка через воркер: воркер
// пускает к нему и без ключа, а из Cloudflare он доступен всегда.
const pumpProbeHost = "telegram.org"

// verifyPump проверяет пересылку данных через воркер настоящим соединением; в тестах подменяется.
var verifyPump = func(ctx context.Context, srv *tgproxy.Server) error {
	return srv.VerifyPump(ctx, pumpProbeHost)
}

func (d *Daemon) tokenPath() string {
	return filepath.Join(d.dataDir, "secrets", "cloudflare-token")
}

// loadToken читает сохранённый токен Cloudflare; пусто — не сохранён.
func (d *Daemon) loadToken() string {
	data, err := os.ReadFile(d.tokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func (d *Daemon) saveToken(token string) error {
	dir := filepath.Dir(d.tokenPath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := secureSecrets(dir); err != nil {
		return fmt.Errorf("папка для токена не защищена: %w", err)
	}
	if err := os.WriteFile(d.tokenPath(), []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	d.mu.Lock()
	d.cfHasToken = true
	d.mu.Unlock()
	return nil
}

// ForgetCloudflareToken стирает сохранённый токен: воркер продолжает работать, но обновлять его
// FI больше не сможет.
func (d *Daemon) ForgetCloudflareToken() error {
	err := os.Remove(d.tokenPath())
	if errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	has := d.loadToken() != ""
	d.mu.Lock()
	d.cfTokenErr = ""
	d.cfHasToken = has
	d.mu.Unlock()
	if err == nil {
		d.log.Info("токен Cloudflare стёрт")
	}
	return err
}

// StartWorkerSetup разворачивает воркер по токену: проверяет токен, загружает код, ждёт, пока
// адрес оживёт, и вписывает его в настройки. Идёт в фоне; ход — в Status().Cloudflare.Setup.
func (d *Daemon) StartWorkerSetup(token, region string, remember bool) error {
	token = strings.TrimSpace(token)
	if token == "" {
		return errors.New("вставьте токен Cloudflare")
	}
	if !cfdeploy.LooksLikeToken(token) {
		return errors.New("это не похоже на токен Cloudflare: он состоит из 40 латинских букв и цифр. Скопируйте его кнопкой Copy сразу после создания")
	}
	if _, ok := cfdeploy.Regions[region]; !ok {
		region = "de"
	}
	return d.startWorkerSetup(token, region, remember, false)
}

// UpdateWorker загружает в воркер код этой версии FI сохранённым токеном.
func (d *Daemon) UpdateWorker() error {
	token := d.loadToken()
	if token == "" {
		return errors.New("токен Cloudflare не сохранён: пройдите настройку воркера заново — FI запомнит токен и дальше будет обновлять воркер сам")
	}
	d.mu.Lock()
	region := d.cfg.Telegram.WorkerRegion
	d.mu.Unlock()
	if _, ok := cfdeploy.Regions[region]; !ok {
		region = "de"
	}
	return d.startWorkerSetup(token, region, true, true)
}

func (d *Daemon) startWorkerSetup(token, region string, remember, update bool) error {
	d.mu.Lock()
	if d.cfSetup != nil && d.cfSetup.Busy {
		d.mu.Unlock()
		return errors.New("воркер уже настраивается — подождите")
	}
	d.cfSetup = &WorkerSetup{Busy: true, Step: "token", Update: update}
	d.cfUpdateTried = time.Now()
	ctx := d.runCtx
	d.tasks.Add(1)
	d.mu.Unlock()
	go func() {
		defer d.tasks.Done()
		ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
		defer cancel()
		domain, err := d.setupWorker(ctx, token, region, remember, update)
		d.mu.Lock()
		setup := &WorkerSetup{Update: update, Domain: domain, Done: err == nil}
		if err != nil {
			setup.Error = err.Error()
			var api *cfdeploy.APIError
			if update && errors.As(err, &api) && api.BadToken() {
				// Сохранённый токен больше не годится: сам FI воркер уже не обновит.
				d.cfTokenErr = "Cloudflare больше не принимает сохранённый токен — создайте новый"
			}
		} else {
			d.cfTokenErr = ""
		}
		d.cfSetup = setup
		d.mu.Unlock()
		switch {
		case err != nil && ctx.Err() == nil:
			d.log.Warn("воркер не настроен", "update", update, "err", err)
		case err == nil && update:
			d.log.Info("код воркера обновлён", "worker", domain, "version", tgproxy.WorkerVersionCurrent)
		case err == nil:
			d.log.Info("воркер создан", "worker", domain, "region", region)
		}
	}()
	return nil
}

func (d *Daemon) setupStep(step string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfSetup != nil && d.cfSetup.Busy {
		d.cfSetup.Step = step
	}
}

// setupWorker — сами шаги настройки. Возвращает адрес воркера. update — код обновляется
// в уже настроенном воркере: остальные настройки FI при этом не трогаются.
func (d *Daemon) setupWorker(ctx context.Context, token, region string, remember, update bool) (string, error) {
	client := &cfdeploy.Client{Token: token, HTTP: d.http}
	account, err := client.Account(ctx)
	if err != nil {
		return "", err
	}
	d.setupStep("upload")
	domain, err := client.DeployTo(ctx, account, d.WorkerCode(), cfdeploy.Regions[region])
	if err != nil {
		return "", err
	}
	// Токен сохраняется сразу после удачной загрузки: если дальше что-то не сойдётся (адрес
	// оживает не сразу), повторять настройку с новым токеном не придётся.
	if remember {
		if err := d.saveToken(token); err != nil {
			d.log.Warn("токен Cloudflare не сохранён — воркер придётся обновлять вручную", "err", err)
		}
	}
	d.setupStep("wait")
	if err := d.waitWorker(ctx, domain); err != nil {
		return domain, err
	}
	d.setupStep("check")
	// Адрес отвечает — но работает ли сама пересылка? Проверяем настоящим соединением, пока
	// человек ещё в мастере: узнать о сбое потом, по неоткрывшемуся сайту, было бы хуже.
	d.mu.Lock()
	key := d.cfg.Telegram.WorkerKey
	d.mu.Unlock()
	tester := &tgproxy.Server{Worker: domain, WorkerKey: key, Log: d.log, LookupFallback: probe.LookupDoH}
	if err := verifyPump(ctx, tester); err != nil {
		return domain, fmt.Errorf("воркер загружен и отвечает, но соединение через него не прошло: %w. Подождите минуту и нажмите «Проверить» в разделе Cloudflare", err)
	}
	d.mu.Lock()
	d.cfg.Telegram.WorkerRegion = region
	saveErr := d.saveLocked()
	enabled := d.cfg.Telegram.Enabled
	d.mu.Unlock()
	if saveErr != nil {
		return domain, saveErr
	}
	if !enabled && !update { // свой воркер без прокси Telegram не работает: включаем вместе с ним
		if err := d.SetTelegram(true); err != nil {
			return domain, err
		}
	}
	if err := d.SetTelegramWorker(domain); err != nil {
		return domain, err
	}
	d.mu.Lock()
	d.workerVer, d.workerRetries = tgproxy.WorkerVersionCurrent, 0
	d.mu.Unlock()
	d.recheckWorker()
	return domain, nil
}

// waitWorker ждёт, пока воркер начнёт отвечать кодом этой версии: новый адрес на workers.dev
// оживает через несколько секунд, а обновлённый код расходится по Cloudflare не мгновенно.
func (d *Daemon) waitWorker(ctx context.Context, domain string) error {
	deadline := time.Now().Add(workerWaitFor)
	var last error
	for {
		ver, err := tgproxy.WorkerVersion(ctx, d.http, domain)
		if err == nil && ver >= tgproxy.WorkerVersionCurrent {
			return nil
		}
		if err == nil {
			err = fmt.Errorf("воркер пока отвечает старым кодом (версия %d)", ver)
		}
		last = err
		if time.Now().After(deadline) {
			return fmt.Errorf("воркер загружен, но ещё не отвечает по адресу %s: %w. Подождите минуту и нажмите «Проверить» в разделе Cloudflare", domain, last)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(workerPoll):
		}
	}
}

// checkWorkerPump проверяет пересылку через воркер у работающего прокси — раз на каждый запуск
// прокси и на каждую версию кода воркера. Вызывается, когда служба узнала версию воркера.
func (d *Daemon) checkWorkerPump(ctx context.Context, worker string, ver int) {
	if ver < tgproxy.WorkerVersionCurrent {
		return // прежний код быстрой пересылки не умеет: проверять нечего
	}
	d.mu.Lock()
	srv := d.tg
	mark := fmt.Sprintf("%p %s %d", srv, worker, ver)
	done := srv == nil || d.pumpChecked == mark
	d.mu.Unlock()
	if done {
		return
	}
	if err := verifyPump(ctx, srv); err != nil {
		if ctx.Err() == nil {
			d.log.Warn("пересылку через воркер проверить не удалось", "worker", worker, "err", err)
		}
		return // сеть или лимит: проверим при следующем опросе воркера
	}
	d.mu.Lock()
	d.pumpChecked = mark
	d.mu.Unlock()
}

// maybeUpdateWorker обновляет код воркера сохранённым токеном, когда в воркере версия старее,
// чем вшита в эту сборку FI. Вызывается после того, как служба узнала версию воркера.
func (d *Daemon) maybeUpdateWorker(ver int) {
	if ver <= 0 || ver >= tgproxy.WorkerVersionCurrent {
		return
	}
	d.mu.Lock()
	busy := d.cfSetup != nil && d.cfSetup.Busy
	recently := time.Since(d.cfUpdateTried) < workerUpdateEvery
	bad := d.cfTokenErr != ""
	d.mu.Unlock()
	if busy || recently || bad || d.loadToken() == "" {
		return
	}
	d.log.Info("в воркере старый код — обновляю сохранённым токеном", "version", ver, "latest", tgproxy.WorkerVersionCurrent)
	if err := d.UpdateWorker(); err != nil {
		d.log.Warn("обновление воркера не запущено", "err", err)
	}
}

// cloudflareStatusLocked — воркер, токен и расход для окна.
func (d *Daemon) cloudflareStatusLocked() CloudflareStatus {
	st := CloudflareStatus{
		HasToken:    d.cfHasToken,
		TokenError:  d.cfTokenErr,
		Region:      d.cfg.Telegram.WorkerRegion,
		Version:     d.workerVer,
		Latest:      tgproxy.WorkerVersionCurrent,
		WorkerUsage: d.usage,
		Limit:       tgproxy.WorkerDailyLimit,
		SignUpURL:   cfdeploy.SignUpURL,
		TokenURL:    cfdeploy.TokenURL,
	}
	if d.cfSetup != nil {
		setup := *d.cfSetup
		st.Setup = &setup
	}
	if d.tg != nil {
		st.WorkerUsage = d.tg.Usage()
	}
	st.Saving = st.LimitedUntil.IsZero() && float64(st.Requests) >= 0.8*float64(st.Limit)
	return st
}

// ClearWorkerSetup убирает итог настройки, когда окно его показало.
func (d *Daemon) ClearWorkerSetup() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfSetup != nil && !d.cfSetup.Busy {
		d.cfSetup = nil
	}
}

// loadUsage читает расход воркера, сохранённый прошлым запуском службы.
func (d *Daemon) loadUsage() {
	data, err := os.ReadFile(filepath.Join(d.dataDir, usageFile))
	if err != nil {
		return
	}
	var u tgproxy.WorkerUsage
	if json.Unmarshal(data, &u) == nil {
		d.usage, d.usageSaved = u, string(data)
	}
}

// saveUsage запоминает расход воркера: счёт идёт по суткам Cloudflare, а не от запуска службы.
func (d *Daemon) saveUsage() {
	d.mu.Lock()
	if d.tg != nil {
		d.usage = d.tg.Usage()
	}
	u, saved := d.usage, d.usageSaved
	d.mu.Unlock()
	data, err := json.Marshal(u)
	if err != nil || string(data) == saved {
		return
	}
	if err := os.WriteFile(filepath.Join(d.dataDir, usageFile), data, 0o644); err != nil {
		d.log.Warn("расход воркера не сохранён", "err", err)
		return
	}
	d.mu.Lock()
	d.usageSaved = string(data)
	d.mu.Unlock()
}
