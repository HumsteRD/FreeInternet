package daemon

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"fi/internal/config"
	"fi/internal/tgproxy"
)

// Программы через Cloudflare. Человек добавляет программу (файл .exe), и окно FI запускает её
// с указанием прокси службы: всё, что программа через него открывает, идёт через воркер — с не
// российского IP. Сама служба программ не запускает: она работает от имени системы, а программа
// должна открыться у человека на рабочем столе. Служба держит список и прокси (tgproxy.AppProxy).

// ProgramsStatus — программы для запуска через Cloudflare и состояние их прокси.
type ProgramsStatus struct {
	Apps []config.App `json:"apps"`
	// Proxy — адрес прокси для программ (127.0.0.1:1455), пока он принимает соединения.
	Proxy string `json:"proxy,omitempty"`
	// Error — почему программы сейчас через Cloudflare не пойдут.
	Error string `json:"error,omitempty"`
	// OldWorker — код воркера старее восьмой версии: он открывает только порт 443, и программа,
	// которой нужны другие порты, часть соединений не откроет.
	OldWorker bool `json:"old_worker,omitempty"`
	tgproxy.AppProxyStats
}

const maxApps = 30

// programsAddr — адрес прокси программ: через один порт от прокси Telegram.
func programsAddr(port int) string { return telegramAddr(port + 2) }

// appName — «C:\Games\Discord\Discord.exe» → «Discord».
func appName(path string) string {
	name := path
	if i := strings.LastIndexAny(name, `\/`); i >= 0 {
		name = name[i+1:]
	}
	if len(name) > 4 && strings.EqualFold(name[len(name)-4:], ".exe") {
		name = name[:len(name)-4]
	}
	return name
}

// AddApp добавляет программу в список запускаемых через Cloudflare.
func (d *Daemon) AddApp(path string) error {
	path = strings.TrimSpace(path)
	switch {
	case path == "":
		return errors.New("выберите программу")
	case len(path) > 500 || strings.ContainsAny(path, "\"\r\n\x00"):
		return errors.New("такой путь к программе не подходит")
	case len(path) < 5 || !strings.EqualFold(path[len(path)-4:], ".exe"):
		return errors.New("нужен файл программы .exe")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if slices.ContainsFunc(d.cfg.Apps, func(a config.App) bool { return strings.EqualFold(a.Path, path) }) {
		return nil // уже в списке
	}
	if len(d.cfg.Apps) >= maxApps {
		return fmt.Errorf("в списке уже %d программ — уберите лишние", maxApps)
	}
	d.cfg.Apps = append(d.cfg.Apps, config.App{Path: path, Name: appName(path)})
	if err := d.saveLocked(); err != nil {
		return err
	}
	d.log.Info("программа пойдёт через Cloudflare", "app", appName(path))
	if d.tg != nil && d.apps == nil {
		d.appsDone = d.startProgramsLocked(d.tgCtx, d.tg)
	}
	return nil
}

// RemoveApp убирает программу из списка.
func (d *Daemon) RemoveApp(path string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.cfg.Apps)
	d.cfg.Apps = slices.DeleteFunc(d.cfg.Apps, func(a config.App) bool { return strings.EqualFold(a.Path, path) })
	if len(d.cfg.Apps) == n {
		return errors.New("такой программы нет в списке")
	}
	if len(d.cfg.Apps) == 0 && d.appsStop != nil {
		// Программ не осталось — прокси для них незачем держать открытым.
		d.appsStop()
		d.apps, d.appsStop, d.appsDone = nil, nil, nil
	}
	return d.saveLocked()
}

// startProgramsLocked запускает прокси программ рядом с прокси Telegram srv, если программы
// есть и задан воркер. Возвращает канал, который закроется, когда прокси остановится.
func (d *Daemon) startProgramsLocked(ctx context.Context, srv *tgproxy.Server) chan struct{} {
	if len(d.cfg.Apps) == 0 || srv.Worker == "" || ctx == nil {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	apps := &tgproxy.AppProxy{Addr: programsAddr(d.cfg.Telegram.Port), Server: srv}
	d.apps, d.appsStop, d.appsErr = apps, cancel, ""
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := apps.ListenAndServe(ctx); err != nil {
			d.log.Error("прокси программ не запущен", "err", err)
			d.mu.Lock()
			if d.apps == apps {
				d.appsErr = fmt.Sprintf("прокси программ не запущен: %v", err)
			}
			d.mu.Unlock()
		}
	}()
	return done
}

func (d *Daemon) programsStatusLocked() ProgramsStatus {
	st := ProgramsStatus{Apps: slices.Clone(d.cfg.Apps), Error: d.appsErr}
	if st.Apps == nil {
		st.Apps = []config.App{}
	}
	switch {
	case len(st.Apps) == 0:
	case !d.cfg.Telegram.Enabled:
		st.Error = "включите прокси для Telegram: программы через Cloudflare работают вместе с ним"
	case d.cfg.Telegram.Worker == "":
		st.Error = "нужен свой Cloudflare Worker — настройте его в разделе Cloudflare"
	}
	st.OldWorker = d.workerVer > 0 && d.workerVer < tgproxy.WorkerVersionCurrent
	if d.apps != nil {
		st.AppProxyStats = d.apps.Stats()
		if st.Listening {
			st.Proxy = programsAddr(d.cfg.Telegram.Port)
		}
	}
	return st
}
