package daemon

import (
	"fmt"
	"slices"

	"fi/internal/strategy"
)

// SetGames выбирает игры для игрового режима и перезапускает движок, если режим «игры» включён.
func (d *Daemon) SetGames(ids []string) error {
	var games []string
	for _, id := range ids {
		if !slices.ContainsFunc(strategy.Games, func(g strategy.Game) bool { return g.ID == id }) {
			return fmt.Errorf("неизвестная игра %q", id)
		}
		if !slices.Contains(games, id) {
			games = append(games, id)
		}
	}
	d.mu.Lock()
	if d.task != nil && d.task.Kind == "select" {
		d.mu.Unlock()
		return ErrBusy
	}
	d.cfg.Games = games
	err := d.saveLocked()
	restart := d.proc != nil && d.cfg.GameMode == "games"
	d.mu.Unlock()
	if err != nil || !restart {
		return err
	}
	d.stopEngine()
	return d.startEngine()
}

// quickCandidates — сколько лидеров прошлого подбора пробовать при быстром переподборе.
const quickCandidates = 3

// quickSet — текущая стратегия и лидеры прошлого полного подбора, которые есть в наборе.
func quickSet(all []strategy.Strategy, ranking []string, current string) []strategy.Strategy {
	byName := make(map[string]strategy.Strategy, len(all))
	for _, s := range all {
		byName[s.Name] = s
	}
	var set []strategy.Strategy
	if s, ok := byName[current]; ok {
		set = append(set, s)
	}
	added := 0
	for _, name := range ranking {
		if added == quickCandidates {
			break
		}
		if s, ok := byName[name]; ok && name != current {
			set = append(set, s)
			added++
		}
	}
	return set
}
