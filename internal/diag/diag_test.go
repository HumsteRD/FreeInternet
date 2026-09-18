package diag

import (
	"strings"
	"testing"
)

func byID(findings []Finding) map[string]Finding {
	m := make(map[string]Finding, len(findings))
	for _, f := range findings {
		m[f.ID] = f
	}
	return m
}

// clean — компьютер, на котором ничего не мешает: работает только движок FI.
func clean() Snapshot {
	return Snapshot{
		Services:   []Service{{"BFE", "Base Filtering Engine"}, {"WinDivert", "WinDivert"}, {"Dnscache", "DNS Client"}},
		Processes:  []string{"explorer.exe", "winws.exe"},
		OwnEngine:  true,
		Hosts:      "# localhost\n127.0.0.1 localhost\n149.154.167.99 web.telegram.org\n",
		DoH:        true,
		Timestamps: true,
		Adapters:   []Adapter{{"Ethernet", "Realtek PCIe GbE Family Controller", 5}},
		InternetIf: 5,
	}
}

func TestEvaluateClean(t *testing.T) {
	for _, f := range Evaluate(clean()) {
		if f.Level != LevelOK {
			t.Errorf("%s: %s %q (%s)", f.ID, f.Level, f.Title, f.Detail)
		}
	}
}

func TestEvaluateProblems(t *testing.T) {
	s := clean()
	s.OwnEngine = false
	s.Services = []Service{
		{"zapret", "zapret"}, {"WinDivert", "WinDivert"},
		{"KillerNetworkService", "Killer Network Service"},
		{"RvControlSvc", "Radmin VPN Control Service"},
		{"AmneziaVPN-service", "AmneziaVPN-service"},
	}
	s.Processes = []string{"winws.exe", "adguardsvc.exe"}
	s.Timestamps = false
	s.DoH = false
	s.Hosts = "0.0.0.0 youtube.com www.youtube.com # старое\n# 1.2.3.4 i.ytimg.com\n"
	s.Adapters = append(s.Adapters, Adapter{"AmneziaVPN", "WireGuard Tunnel", 9})
	s.InternetIf = 9

	got := byID(Evaluate(s))
	want := map[string]Level{
		"bypass": LevelFail, "bfe": LevelFail, "windivert": LevelOK, "timestamps": LevelWarn,
		"vpn": LevelWarn, "killer": LevelFail, "adguard": LevelWarn, "doh": LevelInfo, "hosts": LevelWarn,
	}
	for id, level := range want {
		if got[id].Level != level {
			t.Errorf("%s: %s, ждали %s (%+v)", id, got[id].Level, level, got[id])
		}
	}
	if got["bypass"].Detail != "zapret" {
		t.Errorf("другой обход: %q", got["bypass"].Detail)
	}
	if got["timestamps"].Fix != FixTimestamps || got["bfe"].Fix != FixBFE || got["bypass"].Fix != FixBypass {
		t.Error("нет исправлений в один клик")
	}
	if got["vpn"].Detail != "AmneziaVPN (WireGuard Tunnel)" {
		t.Errorf("VPN: %q", got["vpn"].Detail)
	}
	if got["hosts"].Detail != "youtube.com, www.youtube.com" {
		t.Errorf("hosts: %q", got["hosts"].Detail)
	}
	if _, ok := got["programs"]; ok {
		t.Error("при найденных программах не должно быть общей строки «не мешают»")
	}

	// VPN установлен, но интернет идёт мимо него.
	s.InternetIf = 5
	if f := byID(Evaluate(s))["vpn"]; f.Level != LevelInfo || !strings.Contains(f.Detail, "Radmin VPN") {
		t.Errorf("VPN мимо: %+v", f)
	}
}

func TestEvaluateForeignEngine(t *testing.T) {
	s := clean()
	s.Processes = []string{"winws.exe", "winws.exe"} // второй winws — не наш
	if f := byID(Evaluate(s))["bypass"]; f.Level != LevelFail || f.Detail != "winws.exe" {
		t.Errorf("чужой winws: %+v", f)
	}

	s = clean()
	s.OwnEngine = false
	s.Processes = nil // обход не запущен, а драйвер загружен
	if f := byID(Evaluate(s))["windivert"]; f.Level != LevelWarn || f.Fix != FixWinDivert {
		t.Errorf("брошенный WinDivert: %+v", f)
	}

	s = clean()
	s.Services = nil // службы узнать не удалось — о них не судим
	for _, f := range Evaluate(s) {
		if f.ID == "bfe" || f.ID == "windivert" {
			t.Errorf("проверка %s без списка служб", f.ID)
		}
	}
}

func TestEvaluateUser(t *testing.T) {
	if f := EvaluateUser(UserSnapshot{})[0]; f.Level != LevelOK {
		t.Errorf("без прокси: %+v", f)
	}
	if f := EvaluateUser(UserSnapshot{ProxyEnabled: true, ProxyServer: "127.0.0.1:8080"})[0]; f.Level != LevelWarn || f.Detail != "127.0.0.1:8080" {
		t.Errorf("прокси: %+v", f)
	}
	if f := EvaluateUser(UserSnapshot{AutoConfig: "http://wpad/wpad.dat"})[0]; f.Level != LevelWarn {
		t.Errorf("PAC: %+v", f)
	}
}

func TestParseTimestamps(t *testing.T) {
	cases := map[string]bool{
		"RFC 1323 Timestamps                 : enabled \n":  true,
		"RFC 1323 Timestamps                 : disabled \n": false,
		"Отметки времени RFC 1323            : disabled\n":  false,
		"Что-то другое : disabled\n":                        true,
	}
	for out, want := range cases {
		if got := parseTimestamps(out); got != want {
			t.Errorf("parseTimestamps(%q) = %v", out, got)
		}
	}
}
