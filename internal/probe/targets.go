package probe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// DefaultTargets — базовый набор проверок по сервисам.
// Страницы выбраны так, чтобы ответ был больше 64 КБ: иначе обрыв на 16–20 КБ не увидеть.
func DefaultTargets() []Target {
	return []Target{
		{Name: "YouTube: сайт", Group: "youtube", Kind: KindPage, Host: "www.youtube.com", Weight: 2},
		{Name: "YouTube: превью", Group: "youtube", Kind: KindPage, Host: "i.ytimg.com", Path: "/vi/dQw4w9WgXcQ/maxresdefault.jpg", Weight: 2},
		// Видеосервер принимает 64 КБ и отвечает 404 — этого хватает, чтобы поймать обрыв на 16–20 КБ.
		// Когда узел сети известен, проверка идёт на нём (WithYouTubeNode).
		{Name: "YouTube: видеосервер", Group: "youtube", Kind: KindUpload, Host: videoRedirector, Weight: 3},
		{Name: "YouTube: QUIC", Group: "youtube", Kind: KindQUIC, Host: "www.youtube.com", Weight: 2},

		{Name: "Discord: сайт", Group: "discord", Kind: KindPage, Host: "discord.com", Weight: 2},
		{Name: "Discord: gateway", Group: "discord", Kind: KindTLS, Host: "gateway.discord.gg", Weight: 3},
		{Name: "Discord: CDN", Group: "discord", Kind: KindTLS, Host: "cdn.discordapp.com", Weight: 2},
		{Name: "Discord: обновления", Group: "discord", Kind: KindTLS, Host: "updates.discord.com", Weight: 1},
		// Отсюда приложение Discord скачивает обновления; если сервер недоступен,
		// оно висит на «Checking for updates…».
		{Name: "Discord: загрузка обновлений", Group: "discord", Kind: KindTLS, Host: "stable.dl2.discordapp.net", Weight: 2},

		{Name: "Cloudflare: сайт", Group: "cloudflare", Kind: KindPage, Host: "www.cloudflare.com", Weight: 2},
		{Name: "Cloudflare: cdnjs", Group: "cloudflare", Kind: KindPage, Host: "cdnjs.cloudflare.com", Path: "/ajax/libs/jquery/3.7.1/jquery.min.js", Weight: 2},

		// Google STUN попадает в UDP-фильтр стратегий Flowseal (19294–19344), Cloudflare — нет:
		// вторая цель показывает, проходит ли STUN вообще, без участия обхода.
		{Name: "Звонки: STUN Google", Group: "calls", Kind: KindSTUN, Host: "stun.l.google.com", Port: 19302, Weight: 2},
		{Name: "Звонки: STUN Cloudflare", Group: "calls", Kind: KindSTUN, Host: "stun.cloudflare.com", Port: 3478, Weight: 1},

		{Name: "Telegram: сайт", Group: "telegram", Kind: KindPage, Host: "telegram.org", Weight: 1},
		// Через этот WebSocket работает прокси FI для Telegram Desktop.
		{Name: "Telegram: канал прокси", Group: "telegram", Kind: KindWS, Host: "kws2.web.telegram.org", Path: "/apiws", Weight: 2},

		{Name: "Instagram", Group: "blocked", Kind: KindPage, Host: "www.instagram.com", Weight: 1},
		{Name: "X (Twitter)", Group: "blocked", Kind: KindPage, Host: "x.com", Weight: 1},
		{Name: "RuTracker", Group: "blocked", Kind: KindPage, Host: "rutracker.org", Path: "/forum/index.php", Weight: 1},

		{Name: "Google (контроль)", Group: "reference", Kind: KindPage, Host: "www.google.com", Weight: 1},
	}
}

// hostingSuiteURL — набор сайтов на зарубежных хостингах от hyperion-cs/dpi-checkers (Apache-2.0).
const hostingSuiteURL = "https://hyperion-cs.github.io/dpi-checkers/ru/tcp-16-20/suite.v2.json"

// LoadHostingSuite загружает цели для проверки обрыва «16–20 КБ» по хостингам.
func LoadHostingSuite(ctx context.Context) ([]Target, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, hostingSuiteURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hosting suite: HTTP %d", resp.StatusCode)
	}

	var suite []struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
		Host     string `json:"host"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&suite); err != nil {
		return nil, fmt.Errorf("hosting suite: %w", err)
	}
	targets := make([]Target, 0, len(suite))
	for _, s := range suite {
		targets = append(targets, Target{
			Name:   s.Provider + " " + s.ID,
			Group:  "hosting",
			Kind:   KindUpload,
			Host:   s.Host,
			Weight: 1,
		})
	}
	return targets, nil
}

// Hosts возвращает уникальные хосты целей — для временного hostlist стенда.
func Hosts(targets []Target) []string {
	seen := make(map[string]bool, len(targets))
	var hosts []string
	for _, t := range targets {
		if !seen[t.Host] {
			seen[t.Host] = true
			hosts = append(hosts, t.Host)
		}
	}
	return hosts
}
