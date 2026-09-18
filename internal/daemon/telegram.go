package daemon

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fi/internal/lists"
	"fi/internal/tgproxy"
)

// TelegramStatus — состояние прокси для Telegram.
type TelegramStatus struct {
	Enabled bool   `json:"enabled"`
	Address string `json:"address"`
	Link    string `json:"link,omitempty"`   // tg://proxy — открывает настройку в Telegram Desktop
	Worker  string `json:"worker,omitempty"` // домен Cloudflare Worker, если задан
	// SharedCF — включены общие домены Cloudflare (tg-ws-proxy); CFDomains — сколько их известно.
	SharedCF  bool   `json:"shared_cf"`
	CFDomains int    `json:"cf_domains"`
	Error     string `json:"error,omitempty"`
	tgproxy.Stats
}

func telegramAddr(port int) string {
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
}

// ensureTelegramSecret создаёт секрет прокси при первом запуске.
func (d *Daemon) ensureTelegramSecret() {
	if b, err := hex.DecodeString(d.cfg.Telegram.Secret); err == nil && len(b) == 16 {
		return
	}
	d.cfg.Telegram.Secret = hex.EncodeToString(telegramSecret(machineID()))
	if err := d.saveLocked(); err != nil {
		d.log.Warn("секрет прокси Telegram не сохранён", "err", err)
	}
}

// telegramSecret выводит секрет прокси из идентификатора компьютера: после переустановки FI
// секрет прежний, и прокси, уже сохранённый в Telegram, работает без повторного подключения.
// Прокси слушает только 127.0.0.1, так что предсказуемость секрета ничего не открывает.
// Без идентификатора секрет случайный.
func telegramSecret(machine string) []byte {
	if machine == "" {
		secret := make([]byte, 16)
		rand.Read(secret)
		return secret
	}
	sum := sha256.Sum256([]byte("fi-telegram-proxy:" + machine))
	return sum[:16]
}

func (d *Daemon) telegramStatusLocked() TelegramStatus {
	st := TelegramStatus{
		Enabled:   d.cfg.Telegram.Enabled,
		Address:   telegramAddr(d.cfg.Telegram.Port),
		Worker:    d.cfg.Telegram.Worker,
		SharedCF:  d.cfg.Telegram.SharedCF,
		CFDomains: len(d.tgCF),
		Error:     d.tgErr,
	}
	if secret, err := hex.DecodeString(d.cfg.Telegram.Secret); err == nil && len(secret) == 16 {
		st.Link = tgproxy.Link(st.Address, secret)
	}
	if d.tg != nil {
		st.Stats = d.tg.Stats()
	}
	return st
}

// SetTelegram включает или выключает прокси для Telegram.
func (d *Daemon) SetTelegram(on bool) error {
	d.mu.Lock()
	d.cfg.Telegram.Enabled = on
	err := d.saveLocked()
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if on {
		d.startTelegram()
	} else {
		d.stopTelegram()
	}
	return nil
}

const (
	cfRefreshEvery = time.Hour
	cfCacheName    = "telegram-cf.txt" // последний список общих доменов: Telegram работает, не дожидаясь GitHub
)

// loadCFCache читает сохранённый список общих доменов Cloudflare.
func (d *Daemon) loadCFCache() {
	if data, err := os.ReadFile(filepath.Join(d.dataDir, cfCacheName)); err == nil {
		d.tgCF = tgproxy.ParseCFDomains(string(data))
	}
}

// refreshCF раз в час берёт свежий список общих доменов Cloudflare из репозитория tg-ws-proxy.
func (d *Daemon) refreshCF(ctx context.Context, force bool) {
	d.mu.Lock()
	due := d.cfg.Telegram.Enabled && d.cfg.Telegram.SharedCF && (force || time.Since(d.tgCFAt) > cfRefreshEvery)
	d.mu.Unlock()
	if !due {
		return
	}
	list, err := tgproxy.FetchCFDomains(ctx, d.http)

	d.mu.Lock()
	d.tgCFAt = time.Now()
	if err != nil {
		d.mu.Unlock()
		if ctx.Err() == nil {
			d.log.Warn("список общих доменов Cloudflare не обновлён", "err", err)
		}
		return
	}
	d.tgCF = list
	srv := d.tg
	hostsErr := lists.WriteUserHosts(d.listsDir(), d.bypassHostsLocked())
	d.mu.Unlock()

	if srv != nil {
		srv.SetCFDomains(list)
	}
	if err := os.WriteFile(filepath.Join(d.dataDir, cfCacheName), []byte(strings.Join(list, "\n")+"\n"), 0o644); err != nil {
		d.log.Warn("список общих доменов Cloudflare не сохранён", "err", err)
	}
	if hostsErr != nil && !errors.Is(hostsErr, fs.ErrNotExist) {
		d.log.Warn("домены Cloudflare не добавлены в список обхода", "err", hostsErr)
	}
}

// SetTelegramSharedCF включает или выключает общие домены Cloudflare для Telegram.
func (d *Daemon) SetTelegramSharedCF(on bool) error {
	d.mu.Lock()
	d.cfg.Telegram.SharedCF = on
	err := d.saveLocked()
	srv, list := d.tg, d.tgCF
	if err == nil {
		lists.WriteUserHosts(d.listsDir(), d.bypassHostsLocked())
	}
	ctx := d.runCtx
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if srv != nil {
		if on {
			srv.SetCFDomains(list)
		} else {
			srv.SetCFDomains(nil)
		}
	}
	if on && len(list) == 0 {
		d.tasks.Add(1)
		go func() {
			defer d.tasks.Done()
			d.refreshCF(ctx, true)
		}()
	}
	return nil
}

// bypassHostsLocked — домены для пользовательского списка обхода: сайты пользователя и домены,
// через которые ходит прокси Telegram. Подсети Cloudflare у провайдеров бывают закрыты, поэтому
// tg-ws-proxy советует пускать эти домены через обход.
func (d *Daemon) bypassHostsLocked() []string {
	hosts := d.cfg.SiteHosts()
	if d.cfg.Telegram.Enabled {
		if d.cfg.Telegram.SharedCF {
			hosts = append(hosts, d.tgCF...)
		}
		if d.cfg.Telegram.Worker != "" {
			hosts = append(hosts, d.cfg.Telegram.Worker)
		}
	}
	return hosts
}

// SetTelegramWorker задаёт домен Cloudflare Worker и перезапускает прокси. Пустая строка —
// ходить в Telegram напрямую.
func (d *Daemon) SetTelegramWorker(value string) error {
	host, err := tgproxy.NormalizeWorker(value)
	if err != nil {
		return err
	}
	d.mu.Lock()
	same := d.cfg.Telegram.Worker == host
	d.cfg.Telegram.Worker = host
	err = d.saveLocked()
	enabled := d.cfg.Telegram.Enabled
	d.mu.Unlock()
	if err != nil || same {
		return err
	}
	d.stopTelegram()
	if enabled {
		d.startTelegram()
	}
	d.log.Info("путь в Telegram изменён", "worker", host)
	return nil
}

// startTelegram запускает прокси, если он включён и ещё не работает.
func (d *Daemon) startTelegram() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.cfg.Telegram.Enabled || d.tg != nil {
		return
	}
	secret, _ := hex.DecodeString(d.cfg.Telegram.Secret)
	srv := &tgproxy.Server{
		Addr:   telegramAddr(d.cfg.Telegram.Port),
		Secret: secret,
		Worker: d.cfg.Telegram.Worker,
		Log:    d.log,
	}
	if d.cfg.Telegram.SharedCF {
		srv.SetCFDomains(d.tgCF)
	}
	ctx, cancel := context.WithCancel(d.runCtx)
	done := make(chan struct{})
	d.tg, d.tgStop, d.tgDone, d.tgErr = srv, cancel, done, ""

	go func() {
		defer close(done)
		if err := srv.ListenAndServe(ctx); err != nil {
			d.log.Error("прокси Telegram не запущен", "err", err)
			d.mu.Lock()
			if d.tg == srv {
				d.tgErr = fmt.Sprintf("прокси не запущен: %v", err)
			}
			d.mu.Unlock()
		}
	}()
	d.log.Info("прокси Telegram запущен", "addr", srv.Addr)
}

// stopTelegram останавливает прокси и дожидается закрытия соединений.
func (d *Daemon) stopTelegram() {
	d.mu.Lock()
	cancel, done := d.tgStop, d.tgDone
	d.tg, d.tgStop, d.tgDone = nil, nil, nil
	d.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}
