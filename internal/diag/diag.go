// Package diag ищет на компьютере то, что мешает обходу: другие обходы, VPN, программы,
// которые перехватывают трафик, выключенные настройки Windows, записи в hosts.
// Набор проверок повторяет диагностику service.bat из zapret-discord-youtube (Flowseal)
// и дополнен тем, что видно FI: через какой адаптер на самом деле идёт интернет.
package diag

import (
	"slices"
	"strconv"
	"strings"
)

// Level — насколько найденное мешает обходу.
type Level string

const (
	LevelOK   Level = "ok"
	LevelInfo Level = "info" // стоит знать, но обходу не мешает
	LevelWarn Level = "warn" // может мешать
	LevelFail Level = "fail" // мешает наверняка
)

// Finding — итог одной проверки.
type Finding struct {
	ID       string `json:"id"`
	Level    Level  `json:"level"`
	Title    string `json:"title"`
	Detail   string `json:"detail,omitempty"`
	Advice   string `json:"advice,omitempty"`
	Fix      string `json:"fix,omitempty"` // исправление, которое умеет служба
	FixLabel string `json:"fix_label,omitempty"`
}

// Report — итог диагностики.
type Report struct {
	Findings []Finding `json:"findings"`
}

// Исправления, которые выполняет служба с правами администратора.
const (
	FixTimestamps = "enable_timestamps"
	FixBFE        = "start_bfe"
	FixWinDivert  = "unload_windivert"
	FixBypass     = "stop_bypass"
)

type Service struct {
	Name    string
	Display string
}

type Adapter struct {
	Name        string
	Description string
	Index       uint32
}

// Snapshot — что диагностика узнала о системе. Сбор отделён от оценки, чтобы оценку проверять тестами.
type Snapshot struct {
	Services   []Service // запущенные службы и драйверы; nil — узнать не удалось
	Processes  []string  // имена исполняемых файлов в нижнем регистре, с повторами
	OwnEngine  bool      // один из winws.exe запустил сам FI
	Hosts      string    // содержимое файла hosts
	DoH        bool      // в Windows настроен DNS поверх HTTPS
	Timestamps bool      // метки времени TCP (RFC 1323) включены
	Adapters   []Adapter // подключённые сетевые адаптеры
	InternetIf uint32    // адаптер, через который идёт интернет; 0 — не известно
}

// UserSnapshot — настройки пользователя: служба работает от имени системы и их не видит.
type UserSnapshot struct {
	ProxyEnabled bool
	ProxyServer  string
	AutoConfig   string
}

// Evaluate превращает сведения о системе в список проверок.
func Evaluate(s Snapshot) []Finding {
	findings := []Finding{checkBypass(s)}
	if s.Services != nil {
		findings = append(findings, checkBFE(s), checkWinDivert(s))
	}
	findings = append(findings, checkTimestamps(s), checkVPN(s))
	findings = append(findings, checkPrograms(s)...)
	return append(findings, checkDoH(s), checkHosts(s))
}

// EvaluateUser проверяет настройки пользователя.
func EvaluateUser(u UserSnapshot) []Finding {
	f := Finding{ID: "proxy", Level: LevelOK, Title: "Системный прокси выключен"}
	advice := "Браузер и Discord ходят через этот прокси, а не напрямую, и обход до них не доходит. Если вы им не пользуетесь, выключите: Параметры → Сеть и Интернет → Прокси."
	switch {
	case u.ProxyEnabled:
		f.Level, f.Title, f.Detail, f.Advice = LevelWarn, "Включён системный прокси", u.ProxyServer, advice
	case u.AutoConfig != "":
		f.Level, f.Title, f.Detail, f.Advice = LevelWarn, "Включена автонастройка прокси", u.AutoConfig, advice
	}
	return []Finding{f}
}

var (
	bypassServices  = []string{"zapret", "winws1", "winws2", "goodbyedpi", "discordfix_zapret"}
	bypassProcesses = []string{"goodbyedpi.exe", "winws2.exe"}
)

func checkBypass(s Snapshot) Finding {
	f := Finding{ID: "bypass", Level: LevelOK, Title: "Другие обходы не запущены"}
	found := s.running(func(name, _ string) bool { return slices.Contains(bypassServices, name) })
	for _, p := range bypassProcesses {
		if s.processCount(p) > 0 {
			found = append(found, p)
		}
	}
	own := 0
	if s.OwnEngine {
		own = 1
	}
	if len(found) == 0 && s.processCount("winws.exe") > own {
		found = append(found, "winws.exe")
	}
	if len(found) > 0 {
		f.Level, f.Title, f.Detail = LevelFail, "Работает другой обход", strings.Join(found, ", ")
		f.Advice = "Два обхода одновременно мешают друг другу, поэтому FI свой не запускает. FI может остановить другой обход и перевести его службу на ручной запуск — при удалении FI прежний запуск вернётся."
		f.Fix, f.FixLabel = FixBypass, "Остановить другой обход"
	}
	return f
}

func checkBFE(s Snapshot) Finding {
	f := Finding{ID: "bfe", Level: LevelOK, Title: "Служба базовой фильтрации работает"}
	if len(s.running(func(name, _ string) bool { return name == "bfe" })) == 0 {
		f.Level, f.Title = LevelFail, "Остановлена служба базовой фильтрации (BFE)"
		f.Advice = "Без неё не работает драйвер WinDivert, на котором держится обход."
		f.Fix, f.FixLabel = FixBFE, "Запустить"
	}
	return f
}

func checkWinDivert(s Snapshot) Finding {
	f := Finding{ID: "windivert", Level: LevelOK, Title: "Драйвер WinDivert в порядке"}
	loaded := s.running(func(name, _ string) bool { return name == "windivert" || name == "windivert14" })
	users := s.processCount("winws.exe") + s.processCount("winws2.exe") + s.processCount("goodbyedpi.exe")
	if len(loaded) > 0 && users == 0 {
		f.Level, f.Title, f.Detail = LevelWarn, "Драйвер WinDivert занят, хотя обход не запущен", strings.Join(loaded, ", ")
		f.Advice = "Обычно его оставляет другой обход после остановки. Пока драйвер занят, стратегии FI могут не запуститься."
		f.Fix, f.FixLabel = FixWinDivert, "Выгрузить драйвер"
	}
	return f
}

func checkTimestamps(s Snapshot) Finding {
	f := Finding{ID: "timestamps", Level: LevelOK, Title: "Метки времени TCP включены"}
	if !s.Timestamps {
		f.Level, f.Title = LevelWarn, "Выключены метки времени TCP"
		f.Advice = "Половина стратегий Flowseal подделывает пакеты с помощью меток времени (fooling=ts) и без них не работает."
		f.Fix, f.FixLabel = FixTimestamps, "Включить"
	}
	return f
}

// vpnWords — по этим словам в имени или описании адаптер похож на VPN или туннель.
var vpnWords = []string{"vpn", "wireguard", "wintun", "tap-windows", "openvpn", "amnezia", "warp", "tunnel", "tun2socks", "sing-tun", "hamachi", "zerotier", "outline"}

func looksLikeVPN(text string) bool {
	text = strings.ToLower(text)
	return slices.ContainsFunc(vpnWords, func(w string) bool { return strings.Contains(text, w) })
}

func checkVPN(s Snapshot) Finding {
	f := Finding{ID: "vpn", Level: LevelOK, Title: "Интернет идёт не через VPN"}
	for _, a := range s.Adapters {
		if s.InternetIf != 0 && a.Index == s.InternetIf && (looksLikeVPN(a.Name) || looksLikeVPN(a.Description)) {
			f.Level, f.Title, f.Detail = LevelWarn, "Интернет идёт через VPN", a.Name
			if a.Description != "" && a.Description != a.Name {
				f.Detail += " (" + a.Description + ")"
			}
			f.Advice = "Пока VPN включён, сайты открываются через него: проверки FI видят сеть VPN, а не провайдера, и подбирать стратегию бессмысленно. Выключите VPN, чтобы проверить обход."
			return f
		}
	}
	vpns := s.running(func(name, display string) bool {
		return strings.Contains(name, "vpn") || strings.Contains(display, "vpn")
	})
	if len(vpns) > 0 {
		f.Level, f.Detail = LevelInfo, strings.Join(vpns[:min(len(vpns), 3)], ", ")
		f.Title = "Установлен VPN, но интернет идёт мимо него"
		f.Advice = "Если сервисы перестают работать, когда VPN включается, дело в VPN, а не в обходе."
	}
	return f
}

type program struct {
	id, title, advice string
	level             Level
	service           func(name, display string) bool
	process           string
}

var programs = []program{
	{
		id: "killer", title: "Работает Killer Network Service", level: LevelFail,
		advice:  "Killer перехватывает сетевой трафик и не даёт обходу работать. Отключите службы Killer в «Службах» (services.msc) или удалите Killer Control Center.",
		service: func(n, d string) bool { return strings.Contains(n, "killer") || strings.Contains(d, "killer") },
	},
	{
		id: "intel_cns", title: "Работает Intel Connectivity Network Service", level: LevelFail,
		advice: "Эта служба Intel перехватывает трафик так же, как обход, и они конфликтуют. Отключите её в «Службах» (services.msc).",
		service: func(_, d string) bool {
			return strings.Contains(d, "intel") && strings.Contains(d, "connectivity") && strings.Contains(d, "network")
		},
	},
	{
		id: "checkpoint", title: "Установлен клиент Check Point", level: LevelFail,
		advice:  "Check Point перехватывает трафик и конфликтует с обходом. Помогает только удаление Check Point.",
		service: func(n, _ string) bool { return n == "tracsrvwrapper" || n == "epwd" },
	},
	{
		id: "smartbyte", title: "Работает SmartByte", level: LevelFail,
		advice:  "SmartByte управляет сетевым трафиком и мешает обходу. Отключите службу SmartByte в «Службах» (services.msc) или удалите программу.",
		service: func(n, d string) bool { return strings.Contains(n, "smartbyte") || strings.Contains(d, "smartbyte") },
	},
	{
		id: "adguard", title: "Работает Adguard", level: LevelWarn, process: "adguardsvc.exe",
		advice: "Adguard пропускает трафик через себя и часто ломает Discord. Выключите защиту Adguard или добавьте Discord в исключения.",
	},
}

func checkPrograms(s Snapshot) []Finding {
	var found []Finding
	for _, p := range programs {
		var detail string
		switch {
		case p.process != "" && s.processCount(p.process) > 0:
			detail = p.process
		case p.service != nil:
			detail = strings.Join(s.running(p.service), ", ")
		}
		if detail != "" {
			found = append(found, Finding{ID: p.id, Level: p.level, Title: p.title, Detail: detail, Advice: p.advice})
		}
	}
	if len(found) == 0 {
		found = append(found, Finding{ID: "programs", Level: LevelOK, Title: "Killer, SmartByte, Check Point, Intel CNS и Adguard не мешают"})
	}
	return found
}

func checkDoH(s Snapshot) Finding {
	f := Finding{ID: "doh", Level: LevelOK, Title: "В Windows настроен защищённый DNS"}
	if !s.DoH {
		f.Level, f.Title = LevelInfo, "Защищённый DNS в Windows не настроен"
		f.Advice = "Если провайдер подменяет адреса сайтов, включите DNS поверх HTTPS: в Windows 11 — в свойствах подключения, или в настройках браузера."
	}
	return f
}

// hostsDomains — записи для них ломают YouTube. Записи для Discord и Telegram не трогаем:
// их кладёт в hosts сам Flowseal.
var hostsDomains = []string{"youtube.com", "youtu.be", "googlevideo.com", "ytimg.com"}

func checkHosts(s Snapshot) Finding {
	f := Finding{ID: "hosts", Level: LevelOK, Title: "В hosts нет записей для YouTube"}
	if found := hostsEntries(s.Hosts); len(found) > 0 {
		f.Level, f.Title = LevelWarn, "В hosts есть записи для YouTube"
		f.Detail = strings.Join(found[:min(len(found), 4)], ", ")
		if len(found) > 4 {
			f.Detail += " и ещё " + strconv.Itoa(len(found)-4)
		}
		f.Advice = `Такие записи ведут на заданные вручную адреса, которые быстро устаревают, и YouTube перестаёт открываться. Удалите их из C:\Windows\System32\drivers\etc\hosts (понадобятся права администратора).`
	}
	return f
}

func hostsEntries(content string) []string {
	var found []string
	for line := range strings.Lines(content) {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		for _, name := range fields[1:] {
			name = strings.ToLower(strings.TrimSuffix(name, "."))
			for _, d := range hostsDomains {
				if (name == d || strings.HasSuffix(name, "."+d)) && !slices.Contains(found, name) {
					found = append(found, name)
				}
			}
		}
	}
	return found
}

// running — отображаемые имена запущенных служб, подходящих под условие (имена в нижнем регистре).
func (s Snapshot) running(match func(name, display string) bool) []string {
	var found []string
	for _, svc := range s.Services {
		if !match(strings.ToLower(svc.Name), strings.ToLower(svc.Display)) {
			continue
		}
		label := svc.Display
		if label == "" {
			label = svc.Name
		}
		if !slices.Contains(found, label) {
			found = append(found, label)
		}
	}
	return found
}

func (s Snapshot) processCount(name string) int {
	n := 0
	for _, p := range s.Processes {
		if p == name {
			n++
		}
	}
	return n
}
