package daemon

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"fi/internal/probe"
)

// Service — состояние сервиса для главного окна.
type Service struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	State  string `json:"state"`
	Passed int    `json:"passed"`
	Slow   int    `json:"slow"`
	Total  int    `json:"total"`
	Detail string `json:"detail,omitempty"`
}

const (
	StateOK      = "ok"
	StateSlow    = "slow"
	StatePartial = "partial"
	StateFail    = "fail"
	StateUnknown = "unknown" // нет интернета — о сервисах судить нельзя
	StateEmpty   = "empty"   // проверять нечего: например, своих сайтов пока нет
)

const userGroup = "user"

// serviceGroups — из каких групп целей probe складывается сервис.
var serviceGroups = []struct {
	id, title string
	groups    []string
}{
	{"youtube", "YouTube", []string{"youtube"}},
	{"discord", "Discord", []string{"discord"}},
	{"telegram", "Telegram", []string{"telegram"}},
	{"calls", "Звонки и голос", []string{"calls"}},
	{"sites", "Сайты", []string{userGroup}},
}

// checkTargets — цели периодической проверки: сервисы, сайты пользователя и контроль связи.
// Чужие заблокированные сайты и Cloudflare сюда не входят: это цели подбора,
// а в окне человек видит то, чем пользуется сам. Когда работает прокси для Telegram,
// Telegram проверяется через него (telegramResult), а не по сайту: у многих провайдеров
// адреса Telegram закрыты, и сайт не откроется, хотя приложение работает.
func checkTargets(sites []string, tgProxy bool) []probe.Target {
	var targets []probe.Target
	for _, t := range probe.DefaultTargets() {
		if t.Group != "blocked" && t.Group != "cloudflare" && !(tgProxy && t.Group == "telegram") {
			targets = append(targets, t)
		}
	}
	return append(targets, siteTargets(sites)...)
}

// selectTargets — цели подбора: все базовые и сайты пользователя.
func selectTargets(sites []string) []probe.Target {
	return append(probe.DefaultTargets(), siteTargets(sites)...)
}

func siteTargets(sites []string) []probe.Target {
	targets := make([]probe.Target, 0, len(sites))
	for _, host := range sites {
		targets = append(targets, probe.Target{Name: host, Group: userGroup, Kind: probe.KindPage, Host: host, Weight: 1})
	}
	return targets
}

func summarize(results []probe.Result) []Service {
	offline := false
	for _, r := range results {
		if r.Target.Group == "reference" && r.Status != probe.OK && r.Status != probe.Slow {
			offline = true
		}
	}

	services := make([]Service, 0, len(serviceGroups))
	for _, def := range serviceGroups {
		s := Service{ID: def.id, Title: def.title}
		for _, r := range results {
			if !slices.Contains(def.groups, r.Target.Group) {
				continue
			}
			s.Total++
			switch r.Status {
			case probe.OK:
				s.Passed++
			case probe.Slow:
				s.Slow++
			default:
				if s.Detail == "" {
					// «Discord: gateway» под строкой Discord читается как «Gateway».
					// Адреса своих сайтов оставляем как есть.
					name := strings.TrimPrefix(r.Target.Name, def.title+": ")
					if name != r.Target.Name {
						name = capitalize(name)
					}
					s.Detail = name + ": " + describe(r.Status)
				}
			}
		}
		s.State = serviceState(s, offline)
		services = append(services, s)
	}
	return services
}

func serviceState(s Service, offline bool) string {
	switch {
	case s.Total == 0:
		return StateEmpty
	case offline:
		return StateUnknown
	case s.Passed == s.Total:
		return StateOK
	case s.Passed+s.Slow == s.Total:
		return StateSlow
	case s.Passed+s.Slow == 0:
		return StateFail
	default:
		return StatePartial
	}
}

// failedTargets — цели, проверка которых не прошла: их стоит перепроверить.
func failedTargets(results []probe.Result) []probe.Target {
	var failed []probe.Target
	for _, r := range results {
		if r.Status != probe.OK && r.Status != probe.Slow {
			failed = append(failed, r.Target)
		}
	}
	return failed
}

// mergeRetry подставляет результаты повторной проверки вместо первых: сбой, который
// не повторился, был случайным.
func mergeRetry(first, again []probe.Result) []probe.Result {
	merged := slices.Clone(first)
	for _, r := range again {
		for i := range merged {
			if merged[i].Target == r.Target {
				merged[i] = r
			}
		}
	}
	return merged
}

// changes — что изменилось в сервисах с прошлой проверки, для журнала.
func changes(prev, cur []Service) []string {
	was := make(map[string]Service, len(prev))
	for _, s := range prev {
		was[s.ID] = s
	}
	var out []string
	for _, s := range cur {
		p, ok := was[s.ID]
		if ok && p.State == s.State && p.Detail == s.Detail {
			continue
		}
		line := s.Title + ": " + stateText[s.State]
		if ok {
			line = s.Title + ": " + stateText[p.State] + " → " + stateText[s.State]
		}
		if s.Detail != "" {
			line += " (" + s.Detail + ")"
		}
		out = append(out, line)
	}
	return out
}

var stateText = map[string]string{
	StateOK:      "работает",
	StateSlow:    "замедлено",
	StatePartial: "частично",
	StateFail:    "не работает",
	StateUnknown: "нет связи",
	StateEmpty:   "проверять нечего",
}

// regressed — какой-то сервис работал при прошлой проверке и сломался сейчас.
func regressed(prev, cur []Service) bool {
	was := make(map[string]string, len(prev))
	for _, s := range prev {
		was[s.ID] = s.State
	}
	for _, s := range cur {
		if was[s.ID] == StateOK && (s.State == StateFail || s.State == StatePartial) {
			return true
		}
	}
	return false
}

var statusText = map[probe.Status]string{
	probe.OK:          "работает",
	probe.DNSFail:     "адрес не найден",
	probe.DNSSpoof:    "провайдер подменяет адрес",
	probe.TCPFail:     "сервер недоступен",
	probe.TLSReset:    "соединение обрывается",
	probe.TLSTimeout:  "соединение зависает",
	probe.TLSCert:     "чужой сертификат",
	probe.Freeze16K:   "загрузка останавливается",
	probe.ConnReset:   "соединение сбрасывается",
	probe.Slow:        "медленно",
	probe.QUICFail:    "QUIC не проходит",
	probe.UDPFail:     "звонки не проходят",
	probe.HTTPBlocked: "сайт ограничивает доступ",
	probe.Failed:      "ошибка соединения",
}

func capitalize(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[size:]
}

// describe — статус проверки человеческими словами.
func describe(s probe.Status) string {
	if text, ok := statusText[s]; ok {
		return text
	}
	return string(s)
}

// offline — не открылся даже контрольный сайт: о сервисах судить нельзя.
func offline(services []Service) bool {
	return slices.ContainsFunc(services, func(s Service) bool { return s.State == StateUnknown })
}

// strategyLabel — «general (ALT5)» → «ALT5»: в наборе Flowseal все стратегии называются general (…).
func strategyLabel(name string) string {
	if inner, ok := strings.CutPrefix(name, "general ("); ok {
		return strings.TrimSuffix(inner, ")")
	}
	return name
}
