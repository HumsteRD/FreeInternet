// Package strategy описывает стратегии обхода DPI и импортирует их из готовых наборов.
package strategy

import (
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

// Expand подставляет значения в аргументы.
func (s Strategy) Expand(v Vars) []string {
	r := strings.NewReplacer(
		"{bin}", withSep(v.Bin),
		"{lists}", withSep(v.Lists),
		"{game_tcp}", orDefault(v.GameTCP, GameOff),
		"{game_udp}", orDefault(v.GameUDP, GameOff),
	)
	args := make([]string, len(s.Args))
	for i, a := range s.Args {
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
