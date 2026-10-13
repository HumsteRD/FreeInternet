package daemon

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"

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
