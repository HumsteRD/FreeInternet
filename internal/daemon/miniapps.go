package daemon

import (
	"context"
	"fmt"

	"fi/internal/tgproxy"
)

// miniAppsAddr — адрес локального прокси для мини-приложений: следующий порт за прокси Telegram.
func miniAppsAddr(port int) string { return telegramAddr(port + 1) }

func miniAppsPAC(port int) string { return "http://" + miniAppsAddr(port) + tgproxy.PACPath }

// SetTelegramMiniApps включает или выключает прокси для мини-приложений Telegram.
func (d *Daemon) SetTelegramMiniApps(on bool) error {
	d.mu.Lock()
	d.cfg.Telegram.MiniApps = on
	err := d.saveLocked()
	enabled := d.cfg.Telegram.Enabled
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if enabled {
		d.stopTelegram()
		d.startTelegram()
	}
	return d.applyMiniAppsPolicy()
}

// applyMiniAppsPolicy приводит политику WebView2 клиентов Telegram в соответствие настройкам:
// файл PAC от FI нужен, только пока включены и прокси, и мини-приложения.
func (d *Daemon) applyMiniAppsPolicy() error {
	d.mu.Lock()
	pac := ""
	if d.cfg.Telegram.Enabled && d.cfg.Telegram.MiniApps {
		pac = miniAppsPAC(d.cfg.Telegram.Port)
	}
	d.mu.Unlock()
	if err := setTelegramPAC(pac); err != nil {
		d.log.Warn("настройка окон мини-приложений Telegram не применена", "err", err)
		return fmt.Errorf("окна мини-приложений Telegram не настроены: %w", err)
	}
	return nil
}

// checkWorkerVersion узнаёт версию кода воркера: прежний код сайты Telegram не открывает,
// и окно должно попросить его обновить.
func (d *Daemon) checkWorkerVersion(ctx context.Context, worker string) {
	ver, err := tgproxy.WorkerVersion(ctx, d.http, worker)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfg.Telegram.Worker != worker {
		return // пока проверяли, воркер сменили
	}
	if err != nil {
		d.workerVer = 0
		if ctx.Err() == nil {
			d.log.Warn("воркер не ответил", "worker", worker, "err", err)
		}
		return
	}
	d.workerVer = ver
	if ver < tgproxy.WorkerVersionWeb {
		d.log.Warn("код воркера устарел — мини-приложения через него не откроются", "worker", worker, "version", ver)
	}
	if len(d.cfg.ViaWorker) > 0 {
		key := d.cfg.Telegram.WorkerKey
		go d.checkWorkerKey(ctx, worker, key)
	}
}

// checkWorkerKey узнаёт, вставлен ли в воркер код с ключом FI: без него сайты через воркер не откроются.
func (d *Daemon) checkWorkerKey(ctx context.Context, worker, key string) {
	ok, err := tgproxy.WorkerKeyOK(ctx, d.http, worker, key)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil || d.cfg.Telegram.Worker != worker || d.cfg.Telegram.WorkerKey != key {
		return
	}
	d.workerKeyOK = &ok
	if !ok {
		d.log.Warn("в воркере нет ключа FI — сайты через воркер не откроются; скопируйте код воркера из FI", "worker", worker)
	}
}

// webProxyNeededLocked — нужен ли локальный прокси: для мини-приложений или сайтов через воркер.
func (d *Daemon) webProxyNeededLocked() bool {
	return d.cfg.Telegram.MiniApps || len(d.cfg.ViaWorker) > 0
}

// startMiniAppsLocked запускает локальный прокси (мини-приложения, сайты через воркер) рядом
// с прокси Telegram srv. Возвращает канал, который закроется, когда прокси остановится;
// nil — не запускался.
func (d *Daemon) startMiniAppsLocked(ctx context.Context, srv *tgproxy.Server) chan struct{} {
	if !d.webProxyNeededLocked() {
		return nil
	}
	web := &tgproxy.WebProxy{Addr: miniAppsAddr(d.cfg.Telegram.Port), Server: srv, ViaWorker: d.viaWorkerList}
	d.web = web
	if worker := d.cfg.Telegram.Worker; worker != "" {
		go d.checkWorkerVersion(ctx, worker)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := web.ListenAndServe(ctx); err != nil {
			d.log.Error("прокси мини-приложений не запущен", "err", err)
			d.mu.Lock()
			if d.web == web {
				d.webErr = fmt.Sprintf("прокси мини-приложений не запущен: %v", err)
			}
			d.mu.Unlock()
		}
	}()
	return done
}
