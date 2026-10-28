package winsvc

import "testing"

func TestParseProxyServer(t *testing.T) {
	for in, want := range map[string]string{
		"127.0.0.1:10809":                          "127.0.0.1:10809",
		"http=127.0.0.1:8080;https=127.0.0.1:8443": "127.0.0.1:8443",
		"http=127.0.0.1:8080":                      "127.0.0.1:8080",
		"socks=127.0.0.1:1080":                     "",
		`x"; alert(1)`:                             "",
		"":                                         "",
	} {
		if got := parseProxyServer(in); got != want {
			t.Errorf("parseProxyServer(%q) = %q, ждали %q", in, got, want)
		}
	}
}

func TestNextPAC(t *testing.T) {
	const base = "http://127.0.0.1:1454/sites.pac"
	cases := []struct {
		name, cur, base, manual, want string
		action                        pacAction
	}{
		{"включили", "", base, "", base, pacSet},
		{"VPN включил свой прокси — браузеры ведёт он", "", base, "127.0.0.1:10809", "", pacKeep},
		{"VPN включил прокси поверх PAC FI — PAC убираем", base, base, "127.0.0.1:10809", "", pacRemove},
		{"VPN выключили — PAC возвращается", "", base, "", base, pacSet},
		{"старый PAC с запасным прокси", base + "?fallback=127.0.0.1%3A10809", base, "", base, pacSet},
		{"без изменений", base, base, "", base, pacKeep},
		{"чужой PAC", "http://corp/proxy.pac", base, "", "http://corp/proxy.pac", pacForeign},
		{"сайтов не стало", base, "", "", "", pacRemove},
		{"чужой PAC без сайтов", "http://corp/proxy.pac", "", "", "http://corp/proxy.pac", pacKeep},
		{"свой прокси за VPN не считаем", "", base, "127.0.0.1:1454", base, pacSet},
	}
	for _, c := range cases {
		want, action := nextPAC(c.cur, c.base, c.manual)
		if want != c.want || action != c.action {
			t.Errorf("%s: %q %v, ждали %q %v", c.name, want, action, c.want, c.action)
		}
	}
	if UserRemovedPAC("", "127.0.0.1:10809") {
		t.Error("PAC сменил прокси VPN-клиента — человек ничего не убирал, функцию выключать нельзя")
	}
	if !UserRemovedPAC("", "") {
		t.Error("PAC пропал, и прокси нет — его убрал человек")
	}
}
