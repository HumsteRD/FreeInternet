package strategy

import (
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Game — порты игры для игрового фильтра. Фильтр перехватывает только эти порты, а не все
// 1024–65535: меньше нагрузка на процессор и меньше риск задеть другие программы.
type Game struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	TCP   string `json:"tcp,omitempty"`
	UDP   string `json:"udp,omitempty"`
}

// Games — порты из поддержки разработчиков (2026-09): Valve «Required Ports for Steam»,
// Riot «How to Set Up Port Forwarding», Epic «port forwarding for Epic Games servers»,
// Roblox «General Connection Problems»; Minecraft — стандартные порты серверов.
var Games = []Game{
	{ID: "steam", Title: "Steam: CS2, Dota 2 и другие", TCP: "27014-27050", UDP: "3478,4379-4380,27000-27030"},
	{ID: "valorant", Title: "Valorant", TCP: "2099,5222-5223,8088,8393-8400,8446", UDP: "7000-8000,8180-8181"},
	{ID: "lol", Title: "League of Legends", TCP: "2099,5222-5223,8088,8393-8400", UDP: "5000-5500"},
	{ID: "fortnite", Title: "Fortnite", TCP: "3478-3479,5060,5062,5222,6250,12000-65000", UDP: "3478-3479,5060,5062,6250,12000-65000"},
	{ID: "roblox", Title: "Roblox", UDP: "49152-65535"},
	{ID: "minecraft", Title: "Minecraft", TCP: "25565", UDP: "19132-19133"},
}

// GamePortsFor возвращает порты игрового фильтра. Для режима games — порты выбранных игр,
// для остальных режимов — как GamePorts.
func GamePortsFor(mode string, games []string) (tcp, udp string) {
	if mode != "games" {
		return GamePorts(mode)
	}
	var tcpLists, udpLists []string
	for _, g := range Games {
		if slices.Contains(games, g.ID) {
			tcpLists = append(tcpLists, g.TCP)
			udpLists = append(udpLists, g.UDP)
		}
	}
	return orDefault(MergePorts(tcpLists...), GameOff), orDefault(MergePorts(udpLists...), GameOff)
}

// MergePorts объединяет списки портов вида «80,443,1000-2000» без повторов и пересечений.
// Неверные части пропускаются.
func MergePorts(lists ...string) string {
	type span struct{ lo, hi int }
	var spans []span
	for _, list := range lists {
		for part := range strings.SplitSeq(list, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			lo, hi, isRange := strings.Cut(part, "-")
			a, err := strconv.Atoi(lo)
			b := a
			if err == nil && isRange {
				b, err = strconv.Atoi(hi)
			}
			if err != nil || a < 1 || b > 65535 || a > b {
				continue
			}
			spans = append(spans, span{a, b})
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].lo < spans[j].lo })

	var merged []span
	for _, s := range spans {
		if n := len(merged); n > 0 && s.lo <= merged[n-1].hi+1 {
			merged[n-1].hi = max(merged[n-1].hi, s.hi)
			continue
		}
		merged = append(merged, s)
	}
	parts := make([]string, len(merged))
	for i, s := range merged {
		parts[i] = strconv.Itoa(s.lo)
		if s.hi != s.lo {
			parts[i] += "-" + strconv.Itoa(s.hi)
		}
	}
	return strings.Join(parts, ",")
}
