package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestProxyEnv(t *testing.T) {
	env := proxyEnv([]string{`PATH=C:\Windows`, "https_proxy=http://corp:3128", "NO_PROXY=*", `TEMP=C:\Temp`}, "127.0.0.1:1455")
	for _, want := range []string{`PATH=C:\Windows`, `TEMP=C:\Temp`, "HTTPS_PROXY=http://127.0.0.1:1455", "HTTP_PROXY=http://127.0.0.1:1455", "NO_PROXY=localhost,127.0.0.1,::1"} {
		if !slices.Contains(env, want) {
			t.Errorf("в окружении нет %q: %v", want, env)
		}
	}
	// Прежние настройки прокси убраны: программа должна пойти через FI, а не через них.
	for _, kv := range env {
		if strings.Contains(kv, "corp:3128") || kv == "NO_PROXY=*" {
			t.Errorf("прежняя настройка прокси осталась: %q", kv)
		}
	}
}

func TestIsChromium(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain", "tool.exe")
	electron := filepath.Join(dir, "app", "Discord.exe")
	for _, p := range []string{plain, electron, filepath.Join(dir, "app", "resources", "app.asar")} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
	}
	if isChromium(plain) {
		t.Error("обычная программа принята за Chromium")
	}
	if !isChromium(electron) {
		t.Error("программа на Electron не узнана")
	}
	if got := proxyArgs("127.0.0.1:1455"); got[0] != "--proxy-server=http://127.0.0.1:1455" {
		t.Errorf("ключи Chromium: %v", got)
	}
	if _, err := runViaProxy(filepath.Join(dir, "нет.exe"), "127.0.0.1:1455"); err == nil {
		t.Error("несуществующая программа должна дать понятную ошибку")
	}
	if _, err := runViaProxy(filepath.Join(dir, "script.bat"), "127.0.0.1:1455"); err == nil {
		t.Error("запускать можно только .exe")
	}
}

// TestCurrentExe: программа с установщиком Squirrel после обновления лежит в новой папке
// app-<версия> — запуск находит её сам, а не требует добавить заново.
func TestCurrentExe(t *testing.T) {
	dir := t.TempDir()
	old := filepath.Join(dir, "Discord", "app-1.0.9164", "Discord.exe")
	fresh := filepath.Join(dir, "Discord", "app-1.0.9165", "Discord.exe")
	other := filepath.Join(dir, "Discord", "app-1.0.9163", "Discord.exe")
	for i, p := range []string{other, fresh} {
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, nil, 0o644)
		os.Chtimes(p, time.Time{}, time.Date(2026, 11, 1+i, 0, 0, 0, 0, time.UTC))
	}
	os.MkdirAll(filepath.Join(dir, "Discord", "packages"), 0o755)
	if got := currentExe(old); got != fresh {
		t.Errorf("после обновления: %q, нужна самая свежая %q", got, fresh)
	}
	if got := currentExe(fresh); got != fresh {
		t.Errorf("файл на месте: %q", got)
	}
	// Обычная программа: пропала — значит пропала, соседние папки не трогаем.
	gone := filepath.Join(dir, "tools", "tool.exe")
	if got := currentExe(gone); got != gone {
		t.Errorf("обычная программа: %q", got)
	}
}

// TestCloudflareNotices: о лимите и обрывах воркера человек узнаёт из уведомления, один раз.
func TestCloudflareNotices(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 16, h, 0, 0, 0, time.UTC) }
	st := func(requests, cuts int, limited time.Time) *status {
		s := &status{}
		s.Cloudflare.Requests, s.Cloudflare.Limit, s.Cloudflare.Cuts = requests, 100000, cuts
		s.Cloudflare.LimitedUntil, s.Cloudflare.ResetAt = limited, at(0)
		return s
	}
	titles := func(prev, cur *status) []string {
		var out []string
		for _, n := range cloudflareNotices(prev, cur) {
			out = append(out, n[0])
		}
		return out
	}
	if got := titles(st(100, 0, time.Time{}), st(200, 1, time.Time{})); len(got) != 0 {
		t.Fatalf("обычная работа: %v", got)
	}
	if got := titles(st(79000, 0, time.Time{}), st(80100, 0, time.Time{})); !slices.Equal(got, []string{"Израсходовано 80% лимита Cloudflare"}) {
		t.Fatalf("80%% лимита: %v", got)
	}
	if got := titles(st(80100, 0, time.Time{}), st(81000, 0, time.Time{})); len(got) != 0 {
		t.Fatalf("о 80%% сказано один раз: %v", got)
	}
	if got := titles(st(99990, 0, time.Time{}), st(100000, 0, at(0))); !slices.Equal(got, []string{"Суточный лимит Cloudflare исчерпан"}) {
		t.Fatalf("лимит исчерпан: %v", got)
	}
	if got := titles(st(100000, 0, at(0)), st(100000, 0, at(0))); len(got) != 0 {
		t.Fatalf("о лимите сказано один раз: %v", got)
	}
	if got := titles(st(500, 4, time.Time{}), st(600, 5, time.Time{})); !slices.Equal(got, []string{"Cloudflare обрывает загрузки"}) {
		t.Fatalf("обрывы: %v", got)
	}
}
