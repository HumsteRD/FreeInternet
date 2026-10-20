package daemon

import (
	"slices"

	"fi/internal/probe"
)

// QUIC у YouTube. Браузеры грузят YouTube по QUIC (UDP), и когда провайдер его режет или
// душит, видео встаёт, хотя TCP обход держит. Стратегии QUIC лечат не всегда, а проверка QUIC
// мигает — раньше каждое мигание запускало подбор, и тот рвал связь (а во время игры
// с античитом — до перезагрузки). Теперь QUIC подбор не запускает: если он стабильно не
// проходит, FI запрещает браузерам QUIC, и YouTube идёт по TCP.

// quicFailsToDisable — после скольких проверок подряд, где не прошёл только QUIC, FI его выключает.
const quicFailsToDisable = 2

// quicOnly — из всех целей не прошёл только QUIC.
func quicOnly(results []probe.Result) bool {
	quic := false
	for _, r := range results {
		if r.Status == probe.OK || r.Status == probe.Slow {
			continue
		}
		if r.Target.Kind != probe.KindQUIC {
			return false
		}
		quic = true
	}
	return quic
}

// dropQUIC убирает проверки QUIC: браузерам он запрещён, и его судьба ни на что не влияет.
func dropQUIC(targets []probe.Target) []probe.Target {
	return slices.DeleteFunc(targets, func(t probe.Target) bool { return t.Kind == probe.KindQUIC })
}

// noteQUIC считает проверки подряд, где не прошёл только QUIC, и выключает QUIC в браузерах,
// когда это стало стабильным, а FI это разрешено. true — сбой только в QUIC: подбор не нужен.
func (d *Daemon) noteQUIC(results []probe.Result) bool {
	only := quicOnly(results)
	d.mu.Lock()
	if only {
		d.quicFails++
	} else {
		d.quicFails = 0
	}
	disable := only && d.quicFails >= quicFailsToDisable && d.cfg.QUICAuto && !d.cfg.BrowserQUICOff
	d.mu.Unlock()
	if disable {
		d.log.Info("QUIC у YouTube стабильно не проходит — запрещаю его браузерам, YouTube пойдёт по TCP")
		if err := d.SetBrowserQUICOff(true); err != nil {
			d.log.Warn("QUIC в браузерах не выключен", "err", err)
		}
	}
	return only
}

// results — результаты последней проверки сервисов.
func (d *Daemon) results() []probe.Result {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.lastResults
}

// selectTargetsLocked — цели подбора; без QUIC, если браузерам он запрещён.
func (d *Daemon) selectTargetsLocked() []probe.Target {
	targets := selectTargets(d.cfg.MainSites())
	if d.cfg.BrowserQUICOff {
		targets = dropQUIC(targets)
	}
	return targets
}

// SetBrowserQUICOff запрещает или снова разрешает браузерам QUIC.
func (d *Daemon) SetBrowserQUICOff(off bool) error {
	if err := setBrowserQUIC(!off); err != nil {
		return err
	}
	d.mu.Lock()
	d.cfg.BrowserQUICOff = off
	d.quicFails = 0
	err := d.saveLocked()
	d.mu.Unlock()
	if err == nil {
		d.recheckSoon() // плитка YouTube без проверки QUIC
	}
	return err
}

// SetQUICAuto разрешает или запрещает FI выключать QUIC в браузерах самому.
func (d *Daemon) SetQUICAuto(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg.QUICAuto = on
	return d.saveLocked()
}
