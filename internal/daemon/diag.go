package daemon

import (
	"context"
	"fmt"
	"strings"

	"fi/internal/diag"
)

// Diagnose ищет на компьютере то, что мешает обходу. Настройки пользователя (прокси)
// служба не видит — их добавляет окно.
func (d *Daemon) Diagnose(context.Context) (diag.Report, error) {
	snap, err := diag.Collect()
	if err != nil {
		d.log.Warn("диагностика собрала не всё", "err", err)
	}
	d.mu.Lock()
	snap.OwnEngine = d.proc != nil
	d.mu.Unlock()
	return diag.Report{Findings: diag.Evaluate(snap)}, nil
}

// Fix выполняет исправление из диагностики и возвращает её заново.
func (d *Daemon) Fix(ctx context.Context, id string) (diag.Report, error) {
	var err error
	switch id {
	case diag.FixTimestamps:
		err = diag.EnableTimestamps()
	case diag.FixBFE:
		err = diag.StartBFE()
	case diag.FixWinDivert:
		d.mu.Lock()
		own := d.proc != nil
		d.mu.Unlock()
		if own {
			return diag.Report{}, fmt.Errorf("драйвер занят обходом FI — выключите обход, если хотите его выгрузить")
		}
		err = diag.UnloadWinDivert()
	case diag.FixBypass:
		d.mu.Lock()
		var own uint32
		if d.proc != nil {
			own = d.proc.PID()
		}
		d.mu.Unlock()
		var stopped []string
		stopped, err = diag.StopBypass(own)
		if len(stopped) > 0 {
			d.log.Info("другой обход остановлен", "what", strings.Join(stopped, ", "))
		}
		if err == nil {
			d.mu.Lock()
			start := d.cfg.Enabled && d.proc == nil && d.task == nil
			d.mu.Unlock()
			if start {
				if serr := d.startEngine(); serr != nil {
					d.log.Warn("обход не запущен после остановки другого", "err", serr)
				}
			}
		}
	default:
		return diag.Report{}, fmt.Errorf("неизвестное исправление %q", id)
	}
	if err != nil {
		return diag.Report{}, err
	}
	d.log.Info("исправление из диагностики", "fix", id)
	return d.Diagnose(ctx)
}
