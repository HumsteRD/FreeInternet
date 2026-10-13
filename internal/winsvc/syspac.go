package winsvc

import (
	"net/url"
	"regexp"
	"strings"
)

// Системный PAC — для сайтов через воркер в браузерах. FI ставит AutoConfigURL пользователя
// на свой файл PAC; прокси, заданный до этого вручную (например VPN-клиентом в режиме системного
// прокси), передаётся в PAC как запасной для остальных сайтов, чтобы VPN продолжал работать.

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

// nextPAC решает, каким должен быть AutoConfigURL. base — PAC службы (пусто — сайтов через
// воркер нет), manual — прокси, заданный вручную.
func nextPAC(cur, base, manual string) (string, pacAction) {
	if base == "" {
		if IsOurSitesPAC(cur) {
			return "", pacRemove
		}
		return cur, pacKeep
	}
	if cur != "" && !IsOurSitesPAC(cur) {
		return cur, pacForeign
	}
	want := base
	if manual != "" && !strings.HasPrefix(base, "http://"+manual+"/") { // свой же прокси запасным не годится
		want += "?fallback=" + url.QueryEscape(manual)
	}
	if want == cur {
		return cur, pacKeep
	}
	return want, pacSet
}
