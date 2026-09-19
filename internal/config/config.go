// Package config хранит настройки FI.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"time"

	"fi/internal/lists"
)

// Duration — time.Duration, в JSON записывается строкой вида "15m".
type Duration time.Duration

func (d Duration) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Duration(d).String())
}

func (d *Duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// Site — сайт, добавленный пользователем в список обхода.
type Site struct {
	Host   string    `json:"host"`
	Added  time.Time `json:"added"`
	Note   string    `json:"note,omitempty"`   // что было не так, когда добавляли
	Parent string    `json:"parent,omitempty"` // домен добавлен вместе с этим сайтом: с него тот грузит музыку, видео, картинки
}

type Config struct {
	Enabled    bool            `json:"enabled"`
	BaseDir    string          `json:"base_dir"`        // установленный набор стратегий
	Strategy   string          `json:"strategy"`        // выбранная стратегия; пусто — ещё не подобрана
	GameMode   string          `json:"game_mode"`       // off, games, tcp, udp, all
	Games      []string        `json:"games,omitempty"` // игры для режима games
	Ipset      lists.IpsetMode `json:"ipset"`
	AutoFix    bool            `json:"auto_fix"`          // подбирать стратегию заново, если сервис перестал работать
	Ranking    []string        `json:"ranking,omitempty"` // лидеры последнего полного подбора, лучший первый
	AutoUpdate bool            `json:"auto_update"`       // раз в сутки ставить новый набор стратегий
	AppUpdate  bool            `json:"app_auto_update"`   // ставить новые версии самого FI
	CheckEvery Duration        `json:"check_every"`
	Sites      []Site          `json:"sites"`
	Telegram   Telegram        `json:"telegram"`
}

// Telegram — встроенный прокси для Telegram Desktop.
type Telegram struct {
	Enabled bool   `json:"enabled"`
	Port    int    `json:"port"`
	Secret  string `json:"secret"`           // 32 шестнадцатеричных символа; создаётся при первом запуске
	Worker  string `json:"worker,omitempty"` // домен Cloudflare Worker: путь в Telegram, если его адреса закрыты по IP
	// SharedCF — ходить в Telegram и через общие домены Cloudflare проекта tg-ws-proxy,
	// если прямой путь закрыт.
	SharedCF bool `json:"shared_cf"`
}

func Default() Config {
	return Config{
		Enabled:    true,
		GameMode:   "off",
		Ipset:      lists.IpsetNone,
		AutoFix:    true,
		AutoUpdate: true,
		AppUpdate:  true,
		CheckEvery: Duration(15 * time.Minute),
		// Порт не 1443, чтобы не спорить с TgWsProxy, если он ещё установлен.
		Telegram: Telegram{Enabled: true, Port: 1453, SharedCF: true},
	}
}

// Load читает настройки; если файла нет — возвращает настройки по умолчанию.
// Отсутствующие в файле поля тоже берутся по умолчанию.
func Load(path string) (Config, error) {
	c := Default()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Default(), fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

// Save записывает настройки через временный файл, чтобы сбой не оставил их обрезанными.
func Save(path string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// SiteHosts возвращает все адреса для списка обхода: сайты пользователя и домены, добавленные вместе с ними.
func (c Config) SiteHosts() []string {
	hosts := make([]string, len(c.Sites))
	for i, s := range c.Sites {
		hosts[i] = s.Host
	}
	return hosts
}

// MainSites — сайты, которые добавил сам пользователь, без связанных доменов: их и проверяем.
func (c Config) MainSites() []string {
	var hosts []string
	for _, s := range c.Sites {
		if s.Parent == "" {
			hosts = append(hosts, s.Host)
		}
	}
	return hosts
}

// AddSite добавляет сайт; false — сайт уже в списке.
func (c *Config) AddSite(site Site) bool {
	if slices.ContainsFunc(c.Sites, func(s Site) bool { return s.Host == site.Host }) {
		return false
	}
	c.Sites = append(c.Sites, site)
	return true
}

// RemoveSite удаляет сайт вместе с доменами, добавленными ради него; false — такого сайта не было.
func (c *Config) RemoveSite(host string) bool {
	n := len(c.Sites)
	c.Sites = slices.DeleteFunc(c.Sites, func(s Site) bool { return s.Host == host || s.Parent == host })
	return len(c.Sites) != n
}
