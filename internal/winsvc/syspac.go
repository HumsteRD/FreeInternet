package winsvc

import (
	"regexp"
	"strings"
	"time"
)

// Системный PAC — для сайтов через воркер и видеосерверов YouTube в браузерах. FI ставит
// AutoConfigURL пользователя на свой файл PAC. Когда VPN-клиент включает системный прокси
// (Happ: 127.0.0.1:10809), браузеры ведёт он — FI убирает свой PAC и не спорит с ним: через VPN
// и YouTube, и закрытые для России сайты идут быстрее, чем через воркер. VPN выключили — PAC
// возвращается.

// pacAction — что сделать с AutoConfigURL.
type pacAction int

const (
	pacKeep    pacAction = iota // ничего не менять
	pacSet                      // записать want
	pacRemove                   // удалить: это наш PAC, а сайтов через воркер больше нет
	pacForeign                  // стоит чужой PAC — не трогаем
)

// IsOurSitesPAC — AutoConfigURL поставил FI.
func IsOurSitesPAC(u string) bool {
	return strings.HasPrefix(u, "http://127.0.0.1:") && strings.Contains(u, "/sites.pac")
}

var proxyAddrPattern = regexp.MustCompile(`^[A-Za-z0-9.\-]{1,100}:[0-9]{1,5}$`)

// parseProxyServer достаёт адрес прокси для HTTPS из ProxyServer Windows:
// «127.0.0.1:10809» или «http=127.0.0.1:8080;https=127.0.0.1:8443». Пусто — не разобрать.
func parseProxyServer(s string) string {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "=") {
		if proxyAddrPattern.MatchString(s) {
			return s
		}
		return ""
	}
	var http string
	for _, part := range strings.Split(s, ";") {
		scheme, addr, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || !proxyAddrPattern.MatchString(addr) {
			continue
		}
		switch strings.ToLower(scheme) {
		case "https":
			return addr
		case "http":
			http = addr
		}
	}
	return http
}

// nextPAC решает, каким должен быть AutoConfigURL. base — PAC службы (пусто — через FI в браузерах
// ничего не идёт), manual — прокси, заданный вручную (обычно VPN-клиентом).
func nextPAC(cur, base, manual string) (string, pacAction) {
	vpn := manual != "" && !strings.HasPrefix(base, "http://"+manual+"/") // свой же прокси — не VPN
	if base == "" || vpn {
		if IsOurSitesPAC(cur) {
			return "", pacRemove
		}
		return cur, pacKeep
	}
	if cur != "" && !IsOurSitesPAC(cur) {
		return cur, pacForeign
	}
	if cur == base {
		return cur, pacKeep
	}
	return base, pacSet
}

// Пропавший сценарий. VPN-клиенты при подключении стирают чужие настройки прокси: Happ в режиме
// TUN убирает AutoConfigURL и своего прокси не ставит. Раньше FI принимал это за «сценарий убрал
// человек» и выключал сайты через воркер и свежие ролики YouTube — у одного пользователя 27 раз
// за три недели, каждый раз в секунду включения VPN, и после VPN функции приходилось включать
// руками. Теперь пропавший сценарий возвращается: пока VPN работает, служба сама не просит его
// ставить (base пуст), а без VPN FI ставит его снова. Выключается функция только переключателем
// в FI — или когда сценарий убирают раз за разом: значит, настройками прокси управляет кто-то
// другой, и спорить с ним незачем.
const (
	// PACGoneChecks — сколько опросов подряд ждать, прежде чем возвращать сценарий: служба
	// замечает VPN через несколько секунд после того, как VPN-клиент стёр сценарий.
	PACGoneChecks = 4
	// pacFightLimit возвратов за pacFightWindow — и FI перестаёт возвращать сценарий.
	pacFightLimit  = 3
	pacFightWindow = 10 * time.Minute
)

// PACDecision — что делать со сценарием прокси при очередном опросе.
type PACDecision int

const (
	PACSync    PACDecision = iota // всё на месте: поставить или убрать как обычно
	PACWait                       // сценарий пропал: ждём, не объявится ли VPN
	PACRestore                    // VPN не объявился — возвращаем сценарий
	PACGiveUp                     // его убирают снова и снова — функцию пора выключить
)

// PACWatch помнит, что случалось со сценарием прокси FI.
type PACWatch struct {
	gone     int         // опросов подряд, когда сценарий должен стоять, а его нет
	restores []time.Time // когда FI возвращал пропавший сценарий
}

// Decide решает, что делать: base — сценарий, который просит служба (пусто — не нужен, например
// пока включён VPN), cur и manual — что стоит в Windows сейчас, wasSet — стоял ли сценарий FI
// при прошлом опросе.
func (w *PACWatch) Decide(now time.Time, base, cur, manual string, wasSet bool) PACDecision {
	if base == "" || !wasSet || cur != "" || manual != "" {
		// Сценарий не нужен, ещё не ставился, стоит (наш или чужой) или браузеры ведёт прокси VPN.
		w.gone = 0
		return PACSync
	}
	if w.gone++; w.gone < PACGoneChecks {
		return PACWait
	}
	w.gone = 0
	recent := w.restores[:0]
	for _, t := range w.restores {
		if now.Sub(t) < pacFightWindow {
			recent = append(recent, t)
		}
	}
	w.restores = recent
	if len(w.restores) >= pacFightLimit {
		w.restores = nil
		return PACGiveUp
	}
	w.restores = append(w.restores, now)
	return PACRestore
}
