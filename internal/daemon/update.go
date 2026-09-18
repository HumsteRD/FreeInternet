package daemon

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"fi/internal/flowseal"
)

// updateCheckEvery — как часто служба сама проверяет, не вышел ли новый набор.
const updateCheckEvery = 24 * time.Hour

// BaseStatus — установленный набор стратегий и доступное обновление.
type BaseStatus struct {
	Version         string    `json:"version,omitempty"`
	Installed       time.Time `json:"installed,omitzero"`
	Latest          string    `json:"latest,omitempty"`
	UpdateAvailable bool      `json:"update_available"`
	CheckedAt       time.Time `json:"checked_at,omitzero"`
	AutoUpdate      bool      `json:"auto_update"`
	Error           string    `json:"error,omitempty"`
}

func (d *Daemon) baseStatusLocked() BaseStatus {
	return BaseStatus{
		Version:         d.base.Version,
		Installed:       d.base.Imported,
		Latest:          d.latest.Version,
		UpdateAvailable: d.latest.Version != "" && (d.baseErr != nil || flowseal.Newer(d.latest.Version, d.base.Version)),
		CheckedAt:       d.latestChecked,
		AutoUpdate:      d.cfg.AutoUpdate,
		Error:           d.updateErr,
	}
}

// CheckBaseUpdate узнаёт последнюю версию набора на GitHub.
func (d *Daemon) CheckBaseUpdate(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	rel, err := flowseal.Latest(ctx, d.http)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.latestChecked = time.Now()
	if err != nil {
		d.updateErr = "не удалось узнать последнюю версию набора: " + err.Error()
		return errors.New(d.updateErr)
	}
	d.latest, d.updateErr = rel, ""
	return nil
}

// StartBaseUpdate скачивает и устанавливает последний набор стратегий в фоне; ход виден в Status().Task.
func (d *Daemon) StartBaseUpdate() error {
	if !d.beginTask(&Task{Kind: "base", Title: "Обновление набора стратегий", Total: 3}) {
		return ErrBusy
	}
	d.mu.Lock()
	ctx := d.runCtx
	d.tasks.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.tasks.Done()
		defer d.endTask()
		err := d.updateBase(ctx)
		d.mu.Lock()
		d.updateErr = ""
		if err != nil {
			d.updateErr = err.Error()
		}
		d.mu.Unlock()
		if err != nil {
			d.log.Error("набор стратегий не обновлён", "err", err)
		}
	}()
	return nil
}

func (d *Daemon) updateBase(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	d.progress(0, 3, "Узнаём последнюю версию")
	rel, err := flowseal.Latest(ctx, d.http)
	if err != nil {
		return fmt.Errorf("не удалось узнать последнюю версию набора: %w", err)
	}
	d.mu.Lock()
	d.latest, d.latestChecked = rel, time.Now()
	upToDate := d.baseErr == nil && !flowseal.Newer(rel.Version, d.base.Version)
	d.mu.Unlock()
	if upToDate {
		return nil
	}

	d.progress(1, 3, "Скачиваем набор "+rel.Version)
	// Скачиваем внутрь папки данных: её не изменить обычному пользователю, значит,
	// файлы не подменят между проверкой хеша и установкой.
	tmp, err := os.MkdirTemp(d.dataDir, "download-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	src, err := flowseal.Download(ctx, d.http, rel, tmp)
	if err != nil {
		return err
	}

	d.progress(2, 3, "Устанавливаем набор "+rel.Version)
	d.stopEngine() // winws держит файлы набора
	removeOldBase(d.dataDir)
	dst, strategies, m, err := installBase(d.dataDir, src)

	d.mu.Lock()
	if err == nil {
		d.cfg.BaseDir, d.strategies, d.base, d.baseErr = dst, strategies, m, nil
		if !hasStrategy(strategies, d.cfg.Strategy) {
			d.cfg.Strategy = "" // стратегию убрали из набора — нужен подбор
		}
		err = d.saveLocked()
		d.log.Info("набор стратегий установлен", "version", m.Version, "strategies", len(strategies))
	}
	restart := d.cfg.Enabled && d.cfg.Strategy != ""
	d.mu.Unlock()

	if restart {
		if serr := d.startEngine(); serr != nil {
			d.log.Warn("обход не запущен", "err", serr)
		}
	}
	return err
}

// maybeAutoUpdate раз в сутки проверяет, не вышел ли новый набор, и устанавливает его.
// Первую установку набора человек запускает сам — из окна.
func (d *Daemon) maybeAutoUpdate(ctx context.Context) {
	d.mu.Lock()
	due := d.cfg.AutoUpdate && d.baseErr == nil && d.task == nil && time.Since(d.latestChecked) > updateCheckEvery
	d.mu.Unlock()
	if !due {
		return
	}
	if err := d.CheckBaseUpdate(ctx); err != nil {
		d.log.Warn("проверка обновлений набора", "err", err)
		return
	}
	d.mu.Lock()
	latest, newer := d.latest.Version, flowseal.Newer(d.latest.Version, d.base.Version)
	d.mu.Unlock()
	if !newer {
		return
	}
	d.log.Info("вышел новый набор стратегий", "version", latest)
	if err := d.StartBaseUpdate(); err != nil {
		d.log.Warn("обновление набора не запущено", "err", err)
	}
}

// SetAutoUpdate включает или выключает автообновление набора.
func (d *Daemon) SetAutoUpdate(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg.AutoUpdate = on
	return d.saveLocked()
}
