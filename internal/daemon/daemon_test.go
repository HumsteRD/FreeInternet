package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fi/internal/config"
	"fi/internal/engine"
	"fi/internal/probe"
)

func TestNormalizeHost(t *testing.T) {
	valid := map[string]string{
		"x.com":                                "x.com",
		"  https://www.X.com/home?a=1 ":        "x.com",
		"http://user@rutracker.org:8080/forum": "rutracker.org",
		"пример.рф":                            "xn--e1afmkfd.xn--p1ai",
		"sub.example.co.uk.":                   "sub.example.co.uk",
	}
	for in, want := range valid {
		if got, err := NormalizeHost(in); err != nil || got != want {
			t.Errorf("NormalizeHost(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "localhost", "1.2.3.4", "exa mple.com", "http://", "-bad.com"} {
		if got, err := NormalizeHost(in); err == nil {
			t.Errorf("NormalizeHost(%q) = %q, want error", in, got)
		}
	}
}

func TestSiteAction(t *testing.T) {
	cases := map[probe.Status]bool{
		probe.OK:          false,
		probe.TLSReset:    true,
		probe.Freeze16K:   true,
		probe.DNSSpoof:    false,
		probe.TCPFail:     false,
		probe.HTTPBlocked: false,
		probe.TLSCert:     false,
	}
	for status, add := range cases {
		if got := siteActionFor(status); got.add != add {
			t.Errorf("%s: add = %v, want %v", status, got.add, add)
		}
	}
}

func TestSummarize(t *testing.T) {
	r := func(group string, st probe.Status) probe.Result {
		return probe.Result{Target: probe.Target{Name: group, Group: group}, Status: st}
	}
	services := summarize([]probe.Result{
		r("youtube", probe.OK), r("youtube", probe.OK),
		r("discord", probe.OK), r("discord", probe.TLSReset),
		r("telegram", probe.Slow),
		r("calls", probe.UDPFail),
		r("reference", probe.OK),
	})
	want := map[string]string{"youtube": StateOK, "discord": StatePartial, "telegram": StateSlow, "calls": StateFail, "sites": StateEmpty}
	for _, s := range services {
		if s.State != want[s.ID] {
			t.Errorf("%s = %s, want %s", s.ID, s.State, want[s.ID])
		}
	}

	detailed := summarize([]probe.Result{{Target: probe.Target{Name: "Discord: gateway", Group: "discord"}, Status: probe.TLSReset}})
	if got := detailed[1].Detail; got != "Gateway: соединение обрывается" {
		t.Errorf("detail = %q", got)
	}

	offline := summarize([]probe.Result{r("youtube", probe.TCPFail), r("reference", probe.TCPFail)})
	if offline[0].State != StateUnknown {
		t.Errorf("без связи youtube = %s, want %s", offline[0].State, StateUnknown)
	}

	if !regressed(services, summarize([]probe.Result{r("youtube", probe.TLSReset), r("reference", probe.OK)})) {
		t.Error("поломка YouTube не замечена")
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestImportBaseDetectsTampering(t *testing.T) {
	src := t.TempDir()
	mustWrite(t, filepath.Join(src, "general.bat"), "start \"x\" /min \"%BIN%winws.exe\" --wf-tcp=443 --hostlist=\"%LISTS%list-general.txt\"\r\n")
	mustWrite(t, filepath.Join(src, "bin", "winws.exe"), "binary")
	mustWrite(t, filepath.Join(src, "lists", "list-general.txt"), "discord.com\n")
	mustWrite(t, filepath.Join(src, "lists", "list-general-user.txt"), "own.example\n")
	mustWrite(t, filepath.Join(src, "service.bat"), "@echo off\r\nset \"LOCAL_VERSION=1.10.2\"\r\n")

	data := t.TempDir()
	n, err := ImportBase(data, src)
	if err != nil || n != 1 {
		t.Fatalf("ImportBase = %d, %v", n, err)
	}
	base := filepath.Join(data, "base")
	if _, err := os.Stat(filepath.Join(base, "lists", "list-general-user.txt")); !os.IsNotExist(err) {
		t.Error("пользовательский список набора скопирован")
	}
	_, m, err := loadBase(base)
	if err != nil || m.Version != "1.10.2" {
		t.Fatalf("свежий набор: версия %q, %v", m.Version, err)
	}
	if cfg, _ := config.Load(configPath(data)); cfg.BaseDir != base {
		t.Errorf("BaseDir = %q, want %q", cfg.BaseDir, base)
	}

	// Повторная установка поверх прежней подменяет набор целиком.
	mustWrite(t, filepath.Join(src, "service.bat"), "set \"LOCAL_VERSION=1.10.3\"\r\n")
	if _, err := ImportBase(data, src); err != nil {
		t.Fatal(err)
	}
	if _, m, err := loadBase(base); err != nil || m.Version != "1.10.3" {
		t.Fatalf("после обновления: версия %q, %v", m.Version, err)
	}
	if _, err := os.Stat(base + ".old"); !os.IsNotExist(err) {
		t.Error("прежний набор не удалён")
	}

	mustWrite(t, filepath.Join(base, "bin", "winws.exe"), "подменён")
	if _, _, err := loadBase(base); err == nil {
		t.Fatal("подмена бинарника не обнаружена")
	}
}

func TestTelegramSecretStable(t *testing.T) {
	a, b := telegramSecret("guid-1"), telegramSecret("guid-1")
	if len(a) != 16 || string(a) != string(b) {
		t.Fatalf("секрет для одного компьютера должен совпадать: %x и %x", a, b)
	}
	if string(a) == string(telegramSecret("guid-2")) {
		t.Fatal("у разных компьютеров секреты совпали")
	}
	if r1, r2 := telegramSecret(""), telegramSecret(""); len(r1) != 16 || string(r1) == string(r2) {
		t.Fatalf("без идентификатора секрет должен быть случайным: %x и %x", r1, r2)
	}
}

func TestMergeRetryKeepsSecondOpinion(t *testing.T) {
	site := probe.Target{Name: "Discord: сайт", Group: "discord", Host: "discord.com"}
	cdn := probe.Target{Name: "Discord: CDN", Group: "discord", Host: "cdn.discordapp.com"}
	first := []probe.Result{{Target: site, Status: probe.TCPFail}, {Target: cdn, Status: probe.OK}}
	again := []probe.Result{{Target: site, Status: probe.OK}}
	if failed := failedTargets(first); len(failed) != 1 || failed[0] != site {
		t.Fatalf("перепроверять нужно только сайт: %v", failed)
	}
	merged := mergeRetry(first, again)
	if merged[0].Status != probe.OK || merged[1].Status != probe.OK || first[0].Status != probe.TCPFail {
		t.Fatalf("повторная проверка не подставлена или испорчен исходный срез: %v / %v", merged, first)
	}
}

func TestCheckTargetsWithTelegramProxy(t *testing.T) {
	for _, tg := range checkTargets(nil, true) {
		if tg.Group == "telegram" {
			t.Fatalf("при работающем прокси Telegram проверяется через него, а не %s", tg.Name)
		}
	}
	found := false
	for _, tg := range checkTargets(nil, false) {
		found = found || tg.Group == "telegram"
	}
	if !found {
		t.Fatal("без прокси Telegram проверяется по сайту")
	}
}

func TestChangesForLog(t *testing.T) {
	prev := []Service{{ID: "youtube", Title: "YouTube", State: StateOK}, {ID: "discord", Title: "Discord", State: StateOK}}
	cur := []Service{{ID: "youtube", Title: "YouTube", State: StateOK}, {ID: "discord", Title: "Discord", State: StatePartial, Detail: "Сайт: сервер недоступен"}}
	got := changes(prev, cur)
	if len(got) != 1 || got[0] != "Discord: работает → частично (Сайт: сервер недоступен)" {
		t.Fatalf("изменения: %q", got)
	}
}

func TestSiteFamilies(t *testing.T) {
	got := familyOf("m.soundcloud.com")
	if !slices.Contains(got, "sndcdn.com") || !slices.Contains(got, "soundcloud.cloud") || slices.Contains(got, "soundcloud.com") {
		t.Fatalf("семейство SoundCloud: %v", got)
	}
	if familyOf("example.org") != nil {
		t.Fatal("у неизвестного сайта семейства нет")
	}
	for _, c := range []struct {
		host, related string
		want          bool
	}{
		{"soundcloud.com", "soundcloud.cloud", true},
		{"discord.com", "discordapp.net", true},
		{"x.com", "xvideos.com", false}, // короткое имя ни с чем не сравниваем
		{"soundcloud.com", "google.com", false},
	} {
		if got := sameBrand(c.host, c.related); got != c.want {
			t.Errorf("sameBrand(%s, %s) = %v", c.host, c.related, got)
		}
	}
}

func TestEnsureFamiliesMigrates(t *testing.T) {
	d := &Daemon{dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	now := time.Now()
	d.cfg.AddSite(config.Site{Host: "soundcloud.com", Added: now})
	d.cfg.AddSite(config.Site{Host: "cdn.example.net", Added: now, Note: "нужен для soundcloud.com"})
	d.ensureFamilies()
	if main := d.cfg.MainSites(); len(main) != 1 || main[0] != "soundcloud.com" {
		t.Fatalf("старый связанный домен не получил родителя: %v", main)
	}
	if hosts := d.cfg.SiteHosts(); !slices.Contains(hosts, "soundcloud.cloud") || !slices.Contains(hosts, "sndcdn.com") {
		t.Fatalf("семейство не добавлено к уже сохранённому сайту: %v", hosts)
	}
}

func TestOfflineAndStrategyLabel(t *testing.T) {
	if !offline([]Service{{ID: "youtube", State: StateOK}, {ID: "discord", State: StateUnknown}}) {
		t.Error("контрольный сайт не открылся — это «нет связи»")
	}
	if offline([]Service{{ID: "youtube", State: StateFail}}) {
		t.Error("сломанный сервис — ещё не «нет связи»")
	}
	if got := strategyLabel("general (ALT5)"); got != "ALT5" {
		t.Errorf("strategyLabel = %q", got)
	}
}

func TestRegressedOnlyWhenStrategyCanHelp(t *testing.T) {
	prev := []Service{{ID: "discord", State: StateOK}}
	ipBlock := summarize([]probe.Result{
		{Target: probe.Target{Name: "Discord: сайт", Group: "discord"}, Status: probe.TCPFail},
		{Target: probe.Target{Name: "Discord: CDN", Group: "discord"}, Status: probe.OK},
	})
	if regressed(prev, ipBlock) {
		t.Error("сервер недоступен по адресу — подбор тут не поможет, а интернет прервёт")
	}
	dpi := summarize([]probe.Result{
		{Target: probe.Target{Name: "Discord: сайт", Group: "discord"}, Status: probe.TLSReset},
		{Target: probe.Target{Name: "Discord: CDN", Group: "discord"}, Status: probe.OK},
	})
	if !regressed(prev, dpi) {
		t.Error("сброс соединения — помеха DPI, другая стратегия может помочь")
	}
}

func TestEngineErrorText(t *testing.T) {
	stuck := &engine.RunError{AtStart: true, Output: "Loading...\nwindivert: error opening filter: The object is referenced by other objects so cannot be deleted."}
	if got := engineErrorText(stuck); !strings.HasPrefix(got, "Драйвер WinDivert не запускается") || strings.Contains(got, "Loading") {
		t.Errorf("engineErrorText = %q", got)
	}
	plain := &engine.RunError{AtStart: true, Output: "bad option --foo"}
	if got := engineErrorText(plain); got != "winws не запустился: bad option --foo" {
		t.Errorf("engineErrorText = %q", got)
	}
}
