package winsvc

import (
	"slices"
	"testing"
	"time"
)

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
}

// TestPACWatch: VPN-клиент стёр сценарий — функция не выключается, сценарий возвращается сам.
func TestPACWatch(t *testing.T) {
	const base = "http://127.0.0.1:1454/sites.pac"
	now := time.Date(2026, 11, 12, 22, 1, 20, 0, time.UTC)
	tick := func() time.Time { now = now.Add(3 * time.Second); return now }

	// Happ включили: он стёр сценарий, через пару опросов служба увидела VPN и сценарий больше не просит.
	var w PACWatch
	for i := 1; i < PACGoneChecks; i++ {
		if d := w.Decide(tick(), base, "", "", true); d != PACWait {
			t.Fatalf("опрос %d после пропажи: %v, ждали ожидание VPN", i, d)
		}
	}
	if d := w.Decide(tick(), "", "", "", true); d != PACSync {
		t.Fatalf("служба увидела VPN: %v", d)
	}
	// VPN выключили: сценарий просто ставится снова — прошлая пропажа забыта.
	if d := w.Decide(tick(), base, "", "", false); d != PACSync {
		t.Fatalf("после VPN: %v", d)
	}

	// VPN не объявился — сценарий возвращается, а не выключается функция.
	w = PACWatch{}
	var d PACDecision
	for range PACGoneChecks {
		d = w.Decide(tick(), base, "", "", true)
	}
	if d != PACRestore {
		t.Fatalf("сценарий пропал без VPN: %v, ждали возврат", d)
	}

	// Прокси VPN-клиента или чужой сценарий на месте нашего — не пропажа.
	if d := w.Decide(tick(), base, "", "127.0.0.1:10809", true); d != PACSync {
		t.Fatalf("прокси VPN: %v", d)
	}
	if d := w.Decide(tick(), base, "http://corp/proxy.pac", "", true); d != PACSync {
		t.Fatalf("чужой сценарий: %v", d)
	}

	// Сценарий убирают раз за разом — FI перестаёт спорить.
	w = PACWatch{}
	var got []PACDecision
	for range (pacFightLimit + 1) * PACGoneChecks {
		if d := w.Decide(tick(), base, "", "", true); d != PACWait {
			got = append(got, d)
		}
	}
	want := []PACDecision{PACRestore, PACRestore, PACRestore, PACGiveUp}
	if !slices.Equal(got, want) {
		t.Fatalf("решения %v, ждали %v", got, want)
	}
}
