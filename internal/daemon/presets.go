package daemon

import (
	"fmt"
	"slices"
	"time"

	"fi/internal/config"
)

// Готовые наборы сайтов: чтобы не вписывать домены по одному. Первый домен набора — главный:
// его проверяет FI, он виден в списке, остальные лежат под ним. Домены набора помечены его
// именем, и выключение убирает только их: сайт, который человек добавил сам, остаётся, даже
// если его домены совпали с набором.

// Preset — набор сайтов.
type Preset struct {
	ID    string   `json:"id"`
	Title string   `json:"title"`
	Sub   string   `json:"sub"`
	Hosts []string `json:"-"`
}

// PresetStatus — набор для окна.
type PresetStatus struct {
	Preset
	On    bool `json:"on"` // все домены набора в списке обхода
	Count int  `json:"count"`
}

var presets = []Preset{
	{ID: "social", Title: "Соцсети", Sub: "X (Twitter), Instagram, Facebook, Threads, LinkedIn", Hosts: []string{
		"x.com", "twitter.com", "twimg.com", "t.co",
		"instagram.com", "cdninstagram.com",
		"facebook.com", "fbcdn.net", "threads.net",
		"linkedin.com", "licdn.com",
	}},
	{ID: "torrents", Title: "Торренты", Sub: "RuTracker, NNM-Club", Hosts: []string{
		"rutracker.org", "rutracker.net", "rutracker.cc", "nnmclub.to",
	}},
	{ID: "video", Title: "Видео и стримы", Sub: "Twitch, Kick, Vimeo, Dailymotion", Hosts: []string{
		"twitch.tv", "ttvnw.net", "jtvnw.net", "kick.com", "vimeo.com", "vimeocdn.com", "dailymotion.com",
	}},
	{ID: "games", Title: "Игровые сервисы", Sub: "Epic Games, Riot, Battle.net, Ubisoft, FACEIT, Roblox", Hosts: []string{
		"epicgames.com", "riotgames.com", "battle.net", "blizzard.com", "ubisoft.com", "faceit.com", "roblox.com",
	}},
	{ID: "dev", Title: "Разработка", Sub: "GitHub, GitLab, Docker, npm, PyPI, Stack Overflow", Hosts: []string{
		"github.com", "githubusercontent.com", "githubassets.com", "gitlab.com",
		"docker.com", "docker.io", "npmjs.com", "pypi.org", "stackoverflow.com",
	}},
}

const presetNote = "набор: "

// presetStatusLocked — наборы и включены ли они.
func (d *Daemon) presetStatusLocked() []PresetStatus {
	out := make([]PresetStatus, len(presets))
	for i, p := range presets {
		on := slices.IndexFunc(p.Hosts, func(h string) bool { return !d.hasSiteLocked(h) }) < 0
		out[i] = PresetStatus{Preset: p, On: on, Count: len(p.Hosts)}
	}
	return out
}

func (d *Daemon) hasSiteLocked(host string) bool {
	return slices.ContainsFunc(d.cfg.Sites, func(s config.Site) bool { return s.Host == host })
}

// SetPreset добавляет набор сайтов в список обхода или убирает его оттуда. winws перечитывает
// список сам, поэтому обход не перезапускается.
func (d *Daemon) SetPreset(id string, on bool) error {
	i := slices.IndexFunc(presets, func(p Preset) bool { return p.ID == id })
	if i < 0 {
		return fmt.Errorf("неизвестный набор сайтов %q", id)
	}
	p := presets[i]
	main, note := p.Hosts[0], presetNote+p.Title
	d.mu.Lock()
	now := time.Now()
	if on {
		d.cfg.AddSite(config.Site{Host: main, Added: now, Note: note})
		for _, h := range p.Hosts[1:] {
			d.cfg.AddSite(config.Site{Host: h, Added: now, Parent: main, Note: note})
		}
	} else {
		// Главный домен уходит вместе с набором — то, что FI нашёл ради него, тоже.
		ours := slices.ContainsFunc(d.cfg.Sites, func(s config.Site) bool { return s.Host == main && s.Note == note })
		d.cfg.Sites = slices.DeleteFunc(d.cfg.Sites, func(s config.Site) bool {
			return s.Note == note || (ours && s.Parent == main)
		})
	}
	err := d.saveSitesLocked()
	d.mu.Unlock()
	if err != nil {
		return err
	}
	if on {
		d.log.Info("добавлен набор сайтов", "набор", p.Title, "доменов", len(p.Hosts))
	} else {
		d.log.Info("убран набор сайтов", "набор", p.Title)
	}
	d.recheckSoon()
	return nil
}
