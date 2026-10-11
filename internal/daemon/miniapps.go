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

// startMiniAppsLocked запускает прокси для мини-приложений рядом с прокси Telegram srv.
// Возвращает канал, который закроется, когда прокси остановится; nil — не запускался.
func (d *Daemon) startMiniAppsLocked(ctx context.Context, srv *tgproxy.Server) chan struct{} {
	if !d.cfg.Telegram.MiniApps {
		return nil
	}
	web := &tgproxy.WebProxy{Addr: miniAppsAddr(d.cfg.Telegram.Port), Server: srv}
	d.web = web
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
