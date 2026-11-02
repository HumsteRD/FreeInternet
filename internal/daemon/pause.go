package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"fi/internal/lists"
)

// Пауза: обход выключается на заданное время и включается сам — чтобы зайти в банк или
// поиграть с придирчивым античитом и не забыть вернуть обход. Срок хранится в настройках и
// переживает перезапуск службы. Если к концу паузы идёт игра с античитом, обход ждёт её конца.

// maxPause — дольше сутки уже не пауза: для этого есть обычное выключение.
const maxPause = 24 * time.Hour

// Pause выключает обход на minutes минут.
func (d *Daemon) Pause(minutes int) error {
	wait := time.Duration(minutes) * time.Minute
	if wait <= 0 || wait > maxPause {
		return fmt.Errorf("пауза — от минуты до суток, а не %d мин", minutes)
	}
	until := time.Now().Add(wait)
	d.mu.Lock()
	if d.task != nil && d.task.Kind == "select" {
		d.mu.Unlock()
		return ErrBusy
	}
	d.cfg.Enabled, d.cfg.PausedUntil = false, until
	err := d.saveLocked()
	d.mu.Unlock()
	if err != nil {
		return err
	}
	d.stopEngine()
	d.log.Info("обход на паузе", "до", until.Format("15:04"))
	return nil
}

// resumeIfDue включает обход, когда пауза закончилась. Вызывается из watchVPN.
func (d *Daemon) resumeIfDue(inGame bool) {
	d.mu.Lock()
	due := !d.cfg.PausedUntil.IsZero() && time.Now().After(d.cfg.PausedUntil)
	d.mu.Unlock()
	if !due || inGame {
		return
	}
	d.log.Info("пауза закончилась — включаю обход")
	if err := d.SetEnabled(true); err != nil {
		d.log.Warn("обход после паузы не запущен", "err", err)
	}
}

// ipsetModes — какие адреса обходить вместе со списками сайтов.
var ipsetModes = []lists.IpsetMode{lists.IpsetNone, lists.IpsetLoaded, lists.IpsetAny}

// SetIpset меняет режим обхода по адресам и перезапускает движок с новым списком.
func (d *Daemon) SetIpset(mode string) error {
	m := lists.IpsetMode(mode)
	if !slices.Contains(ipsetModes, m) {
		return fmt.Errorf("неизвестный режим ipset %q", mode)
	}
	if err := restartAllowed(); err != nil {
		return err
	}
	d.mu.Lock()
	if d.task != nil && d.task.Kind == "select" {
		d.mu.Unlock()
		return ErrBusy
	}
	if m == lists.IpsetLoaded {
		// Без полного списка режим молча ничего бы не менял.
		if _, err := os.Stat(filepath.Join(d.cfg.BaseDir, "lists", "ipset-all.txt.backup")); err != nil {
			d.mu.Unlock()
			return fmt.Errorf("в наборе стратегий нет списка адресов: обновите набор")
		}
	}
	d.cfg.Ipset = m
	err := d.saveLocked()
	restart := d.proc != nil
	d.mu.Unlock()
	if err != nil || !restart {
		return err
	}
	d.stopEngine()
	return d.startEngine()
}
