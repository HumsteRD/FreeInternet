// Package strategy описывает стратегии обхода DPI и импортирует их из готовых наборов.
package strategy

import (
	"os"
	"path/filepath"
	"strings"
)

// GameOff — порт-заглушка вместо игрового диапазона, когда игровой фильтр выключен
// (так же делает Flowseal: winws не принимает пустой список портов).
const GameOff = "12"

// Strategy — аргументы winws с плейсхолдерами {bin}, {lists}, {game_tcp}, {game_udp}.
type Strategy struct {
	Name   string   `json:"name"`
	Source string   `json:"source,omitempty"`
	Args   []string `json:"args"`
}

// Vars — значения плейсхолдеров.
type Vars struct {
	Bin     string // каталог с winws.exe и fake-файлами
	Lists   string // каталог со списками
	GameTCP string // диапазон портов игрового фильтра или GameOff
	GameUDP string
}

// GamePorts возвращает порты игрового фильтра для режима off, tcp, udp или all.
func GamePorts(mode string) (tcp, udp string) {
	const all = "1024-65535"
	switch mode {
	case "tcp":
		return all, GameOff
	case "udp":
		return GameOff, all
	case "all":
		return all, all
	default:
		return GameOff, GameOff
	}
}

// Expand подставляет значения в аргументы. Если в папке списков есть list-google,
// YouTube попадает и в правила QUIC (withGoogleQUIC), а если есть ipset-game — игровые
// правила берут адреса из него (withGameIpset).
func (s Strategy) Expand(v Vars) []string {
	src := s.Args
	if _, err := os.Stat(filepath.Join(v.Lists, "list-google.txt")); v.Lists != "" && err == nil {
		src = withGoogleQUIC(src)
	}
	if _, err := os.Stat(filepath.Join(v.Lists, "ipset-game.txt")); v.Lists != "" && err == nil {
		src = withGameIpset(src)
	}
	r := strings.NewReplacer(
		"{bin}", withSep(v.Bin),
		"{lists}", withSep(v.Lists),
		"{game_tcp}", orDefault(v.GameTCP, GameOff),
		"{game_udp}", orDefault(v.GameUDP, GameOff),
	)
	args := make([]string, len(src))
	for i, a := range src {
		args[i] = r.Replace(a)
	}
	return args
}

func withSep(dir string) string {
	return filepath.Clean(dir) + string(filepath.Separator)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
