package daemon

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"time"

	"fi/internal/probe"
	"fi/internal/tgproxy"
)

// Сайты через воркер — для сайтов, которые закрыли для России сами сервисы (SoundCloud:
// «Not available in Russian Federation»). Обход DPI тут бесполезен: сервис смотрит на IP.
// Через Cloudflare Worker такой сайт видит IP Cloudflare за пределами России. Браузеры
// получают системный PAC от окна FI, Telegram — через PAC мини-приложений.

// ensureWorkerKey создаёт ключ FI для кода воркера при первом запуске.
func (d *Daemon) ensureWorkerKey() {
	if d.cfg.Telegram.WorkerKey != "" {
		return
	}
	b := make([]byte, 16)
	rand.Read(b)
	d.cfg.Telegram.WorkerKey = hex.EncodeToString(b)
	if err := d.saveLocked(); err != nil {
		d.log.Warn("ключ воркера не сохранён", "err", err)
	}
}

// viaWorkerList — сайты через воркер; вызывается локальным прокси на каждый запрос.
func (d *Daemon) viaWorkerList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.cfg.ViaWorker)
}

// AddViaWorker добавляет сайт (вместе с его доменами, как SoundCloud и sndcdn.com) в список через воркер.
func (d *Daemon) AddViaWorker(input string) error {
	host, err := NormalizeHost(input)
	if err != nil {
		return err
	}
	if tgproxy.IsTelegramWeb(host) {
		return fmt.Errorf("%s — сайт Telegram, он и так идёт через FI (мини-приложения)", host)
	}
	d.mu.Lock()
	restart := !d.webProxyNeededLocked() && d.cfg.Telegram.Enabled
	for _, h := range append([]string{host}, familyOf(host)...) {
		if !slices.Contains(d.cfg.ViaWorker, h) {
			d.cfg.ViaWorker = append(d.cfg.ViaWorker, h)
		}
	}
	err = d.saveLocked()
	d.mu.Unlock()
	if err != nil {
		return err
	}
	d.log.Info("сайт пойдёт через воркер", "site", host)
	if restart {
		d.stopTelegram() // локальный прокси запускается вместе с прокси Telegram
		d.startTelegram()
	} else {
		d.recheckWorker()
	}
	return nil
}

// RemoveViaWorker убирает сайт и его домены из списка через воркер.
func (d *Daemon) RemoveViaWorker(host string) error {
	d.mu.Lock()
	n := len(d.cfg.ViaWorker)
	family := append([]string{host}, familyOf(host)...)
	d.cfg.ViaWorker = slices.DeleteFunc(d.cfg.ViaWorker, func(h string) bool { return slices.Contains(family, h) })
	if len(d.cfg.ViaWorker) == n {
		d.mu.Unlock()
		return fmt.Errorf("сайта %s нет в списке", host)
	}
	err := d.saveLocked()
	d.mu.Unlock()
	return err
}

// SetViaWorkerBrowsers включает или выключает сайты через воркер в браузерах (системный
// сценарий настройки прокси Windows, его ставит окно FI). Без сценария не работают и свежие
// ролики YouTube через воркер — они выключаются вместе с ним.
func (d *Daemon) SetViaWorkerBrowsers(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg.ViaWorkerBrowsers = on
	if !on {
		d.cfg.YouTubeViaWorker = false
	}
	return d.saveLocked()
}

// Свежие ролики YouTube. Популярное YouTube отдаёт с кэша в сети провайдера, а свежее и редкое —
// с серверов самого Google, и провайдер может закрыть их адреса: TCP есть, а приветствие TLS
// пропадает, на любое имя сайта и с любой стратегией (2026-10-21: 173.194.176.0/23, 173.194.181.0/24).
// Тогда видеосерверы googlevideo.com идут через локальный прокси FI: напрямую, если сервер
// отвечает, и через воркер, если молчит. Привязки ролика к IP у YouTube нет — проверено.

// youtubeFallbackSites — что идёт через FI, пока включены свежие ролики через воркер.
var youtubeFallbackSites = []string{"googlevideo.com"}

// fallbackList — сайты «напрямую, а если сервер молчит — через воркер»; вызывается прокси на каждый запрос.
func (d *Daemon) fallbackList() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfg.YouTubeViaWorker {
		return youtubeFallbackSites
	}
	return nil
}

// SetYouTubeViaWorker включает или выключает свежие ролики YouTube через воркер. Включает и
// сценарий прокси в браузерах: человек соглашается на него этим же переключателем.
func (d *Daemon) SetYouTubeViaWorker(on bool) error {
	d.mu.Lock()
	if on && (!d.cfg.Telegram.Enabled || d.cfg.Telegram.Worker == "") {
		d.mu.Unlock()
		return errors.New("нужен свой Cloudflare Worker и включённый прокси для Telegram — задайте воркер в разделе Telegram")
	}
	restart := on && !d.webProxyNeededLocked()
	d.cfg.YouTubeViaWorker = on
	if on {
		d.cfg.ViaWorkerBrowsers = true
	}
	err := d.saveLocked()
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if on {
		d.log.Info("видеосерверы YouTube пойдут через FI: напрямую, а закрытые провайдером — через воркер")
	}
	if restart {
		d.stopTelegram() // локальный прокси запускается вместе с прокси Telegram
		d.startTelegram()
	} else if on {
		d.recheckWorker()
	}
	d.recheckSoon()
	return nil
}

// throughWorker — свежие ролики идут через воркер: сервер, закрытый провайдером, проверяется
// через воркер, и если он открывается, YouTube работает.
func throughWorker(ctx context.Context, srv *tgproxy.Server, results []probe.Result) {
	for i, r := range results {
		if r.Status != probe.IPBlocked || r.Target.Group != "youtube" {
			continue
		}
		wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		err := srv.WorkerTLS(wctx, r.Target.Host)
		cancel()
		if err == nil {
			results[i].Status, results[i].Detail = probe.OK, "через воркер: провайдер закрыл адрес сервера"
		} else {
			results[i].Detail += "; через воркер: " + err.Error()
		}
	}
}

// WorkerCode — код воркера с ключом этого FI.
func (d *Daemon) WorkerCode() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return tgproxy.WorkerCode(d.cfg.Telegram.WorkerKey)
}

// recheckWorker заново узнаёт версию и ключ воркера — например, после того как человек вставил новый код.
func (d *Daemon) recheckWorker() {
	d.mu.Lock()
	worker, ctx := d.cfg.Telegram.Worker, d.runCtx
	d.workerKeyOK = nil
	d.mu.Unlock()
	if worker != "" {
		go d.checkWorkerVersion(ctx, worker)
	}
}
