package daemon

import (
	"context"
	"time"

	"fi/internal/probe"
)

// Игра с античитом и интернет. Перезапускать обход во время игры нельзя: драйвер WinDivert
// застревает до выхода из игры. Но и уже работающий обход античит может заклинить: пакеты уходят
// в winws и не возвращаются, и встаёт весь интернет — вместе с туннелем VPN, который тоже идёт
// через драйвер. Пока идёт игра, FI проверяет контрольные сайты, и если они раз за разом молчат,
// выключает обход до выхода из игры: лучше интернет без обхода, чем никакого. После игры обход
// поднимается сам (afterGame), со всем лечением драйвера.

var (
	// gameCheckEvery — как часто во время игры проверять, идёт ли интернет через драйвер обхода.
	gameCheckEvery = 20 * time.Second
	// gameFailsToStop — после скольких проверок подряд без интернета обход выключается.
	gameFailsToStop = 2
	// internetUp — открывается ли хоть один контрольный сайт; подменяется в тестах.
	internetUp = (*Daemon).internetOK
)

// gameTargets — контрольные сайты: им обход не нужен, но их пакеты идут через его драйвер.
func gameTargets() []probe.Target {
	return []probe.Target{
		{Name: "Google (контроль)", Group: "reference", Kind: probe.KindTLS, Host: "www.google.com", Weight: 1},
		{Name: "Яндекс (контроль)", Group: "reference", Kind: probe.KindTLS, Host: "ya.ru", Weight: 1},
	}
}

func (d *Daemon) internetOK(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	for _, r := range d.prober.Run(ctx, gameTargets()) {
		if r.Status == probe.OK || r.Status == probe.Slow {
			return true
		}
	}
	return false
}

// gameWatch — проверки интернета, пока идёт игра с античитом.
type gameWatch struct {
	at    time.Time // когда проверяли в последний раз
	fails int       // проверок подряд без интернета
}

// due — пора ли проверять; первая проверка — через gameCheckEvery после начала игры.
func (g *gameWatch) due(now time.Time) bool {
	if now.Sub(g.at) < gameCheckEvery {
		return false
	}
	g.at = now
	return true
}

// note учитывает проверку; true — интернет молчит уже gameFailsToStop раз подряд.
func (g *gameWatch) note(ok bool) bool {
	if ok {
		g.fails = 0
		return false
	}
	g.fails++
	return g.fails >= gameFailsToStop
}

// guardGame вызывается, пока загружен античит: если интернет через драйвер обхода встал,
// обход выключается до выхода из игры.
func (d *Daemon) guardGame(ctx context.Context, ac string) {
	d.mu.Lock()
	running := d.proc != nil
	due := running && d.game.due(time.Now())
	d.mu.Unlock()
	if !due {
		return
	}
	ok := internetUp(d, ctx)
	d.mu.Lock()
	stop := d.game.note(ok)
	d.mu.Unlock()
	if !stop || ctx.Err() != nil {
		return
	}
	d.log.Warn("пока идёт игра, интернет через драйвер обхода встал — выключаю обход до выхода из игры", "anticheat", ac)
	d.stopEngine()
	if internetUp(d, ctx) {
		d.log.Info("без обхода интернет снова идёт: драйвер заклинил античит; после игры обход включится сам", "anticheat", ac)
	} else {
		d.log.Info("интернета нет и без обхода — дело не в FI; после игры обход включится сам")
	}
	d.mu.Lock()
	d.engineErr = "Античит (" + ac + ") заклинил драйвер обхода, и интернет вставал — обход выключен до выхода из игры. После игры FI включит его сам."
	d.mu.Unlock()
}
