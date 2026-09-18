package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"fi/internal/appupdate"
)

// Version — версия FI; fi-service задаёт её при старте из версии сборки.
var Version = "dev"

const (
	appCheckEvery = 24 * time.Hour
	// appInstallWait — сколько задача «обновление» ждёт, пока установщик остановит службу.
	// Если служба всё ещё работает, установщик не справился: подробности в update.log.
	appInstallWait = 3 * time.Minute
)

// AppStatus — версия FI и доступное обновление.
type AppStatus struct {
	Version         string    `json:"version"`
	Configured      bool      `json:"configured"` // сборка знает, где брать обновления
	Latest          string    `json:"latest,omitempty"`
	UpdateAvailable bool      `json:"update_available"`
	CheckedAt       time.Time `json:"checked_at,omitzero"`
	AutoUpdate      bool      `json:"auto_update"`
	Error           string    `json:"error,omitempty"`
}

func (d *Daemon) appStatusLocked() AppStatus {
	return AppStatus{
		Version:         Version,
		Configured:      appupdate.Configured(),
		Latest:          d.appLatest.Version,
		UpdateAvailable: d.appLatest.Version != "" && appupdate.Newer(d.appLatest.Version, Version),
		CheckedAt:       d.appChecked,
		AutoUpdate:      d.cfg.AppUpdate,
		Error:           d.appErr,
	}
}

// CheckAppUpdate узнаёт, не вышла ли новая версия FI.
func (d *Daemon) CheckAppUpdate(ctx context.Context) error {
	if !appupdate.Configured() {
		return appupdate.ErrNotConfigured
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	m, err := appupdate.Check(ctx, d.http)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.appChecked = time.Now()
	if err != nil {
		d.appErr = "не удалось проверить обновление FI: " + err.Error()
		return errors.New(d.appErr)
	}
	d.appLatest, d.appErr = m, ""
	return nil
}

// StartAppUpdate скачивает новую версию и запускает её установщик; ход виден в Status().Task.
func (d *Daemon) StartAppUpdate() error {
	if !appupdate.Configured() {
		return appupdate.ErrNotConfigured
	}
	if !d.beginTask(&Task{Kind: "app", Title: "Обновление FI", Total: 3}) {
		return ErrBusy
	}
	d.mu.Lock()
	ctx := d.runCtx
	d.tasks.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.tasks.Done()
		defer d.endTask()
		launched, err := d.updateApp(ctx)
		if err != nil {
			d.mu.Lock()
			d.appErr = err.Error()
			d.mu.Unlock()
			d.log.Error("FI не обновлён", "err", err)
			return
		}
		if launched {
			// Установщик остановит службу; до тех пор окно показывает, что идёт обновление.
			select {
			case <-ctx.Done():
			case <-time.After(appInstallWait):
				d.mu.Lock()
				d.appErr = "установщик обновления не справился — подробности в " + filepath.Join(d.dataDir, "update.log")
				d.mu.Unlock()
			}
		}
	}()
	return nil
}

func (d *Daemon) updateApp(ctx context.Context) (launched bool, err error) {
	d.progress(0, 3, "Узнаём последнюю версию")
	if err := d.CheckAppUpdate(ctx); err != nil {
		return false, err
	}
	d.mu.Lock()
	m := d.appLatest
	d.mu.Unlock()
	if !appupdate.Newer(m.Version, Version) {
		return false, nil
	}

	d.progress(1, 3, "Скачиваем FI "+m.Version)
	// Папка данных закрыта от записи пользователями: установщик не подменить после проверки хеша.
	dir := filepath.Join(d.dataDir, "updates")
	os.RemoveAll(dir) // установщики прошлых обновлений
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false, err
	}
	dlCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	path, err := appupdate.Download(dlCtx, d.http, m, dir)
	if err != nil {
		return false, err
	}

	d.progress(2, 3, "Устанавливаем FI "+m.Version)
	d.log.Info("запускаю установщик обновления", "version", m.Version)
	if err := launchInstaller(path); err != nil {
		return false, err
	}
	return true, nil
}

// maybeAppUpdate раз в сутки проверяет новую версию FI и ставит её, если включено.
func (d *Daemon) maybeAppUpdate(ctx context.Context) {
	d.mu.Lock()
	due := appupdate.Configured() && Version != "dev" && d.cfg.AppUpdate && d.task == nil &&
		time.Since(d.appChecked) > appCheckEvery
	d.mu.Unlock()
	if !due {
		return
	}
	if err := d.CheckAppUpdate(ctx); err != nil {
		d.log.Warn("проверка обновлений FI", "err", err)
		return
	}
	d.mu.Lock()
	latest, newer := d.appLatest.Version, appupdate.Newer(d.appLatest.Version, Version)
	d.mu.Unlock()
	if !newer {
		return
	}
	d.log.Info("вышла новая версия FI", "version", latest)
	if err := d.StartAppUpdate(); err != nil {
		d.log.Warn("обновление FI не запущено", "err", err)
	}
}

// SetAppAutoUpdate включает или выключает автообновление приложения.
func (d *Daemon) SetAppAutoUpdate(on bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg.AppUpdate = on
	return d.saveLocked()
}
