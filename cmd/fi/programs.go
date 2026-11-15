package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"fi/internal/cfdeploy"
)

// Программы через Cloudflare: окно запускает программу с указанием прокси службы, и всё, что она
// через него открывает, идёт через воркер. Запускает именно окно, а не служба: служба работает
// от имени системы, а программа должна открыться у человека, с его настройками и в его сеансе.
//
// Как программа узнаёт о прокси:
//   - Chromium и всё, что на нём построено (браузеры, Electron: Discord, лаунчеры), — ключ
//     --proxy-server в командной строке;
//   - остальные — переменные окружения HTTPS_PROXY и HTTP_PROXY: их читают curl, Git, Python,
//     Node.js, программы на Go и Rust и многие лаунчеры.
//
// Программа, которая настроек прокси не читает (большинство игр), пойдёт мимо — принудить её
// нельзя без драйвера, перехватывающего весь её трафик.

// chromiumFiles — файлы, которые лежат рядом с программой на Chromium или Electron.
var chromiumFiles = []string{
	"chrome_100_percent.pak", "chrome_elf.dll", "libcef.dll", "v8_context_snapshot.bin",
	"icudtl.dat", filepath.Join("resources", "app.asar"), filepath.Join("resources", "app"),
}

// isChromium — программа построена на Chromium или Electron и понимает --proxy-server.
func isChromium(exe string) bool {
	dir := filepath.Dir(exe)
	for _, name := range chromiumFiles {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return true
		}
	}
	return false
}

// proxyEnv — окружение программы: прежнее, но с прокси FI вместо любых своих настроек прокси.
func proxyEnv(env []string, proxy string) []string {
	url := "http://" + proxy
	out := make([]string, 0, len(env)+6)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
			continue
		}
		out = append(out, kv)
	}
	return append(out,
		"HTTP_PROXY="+url, "HTTPS_PROXY="+url, "ALL_PROXY="+url,
		"http_proxy="+url, "https_proxy="+url,
		"NO_PROXY=localhost,127.0.0.1,::1",
	)
}

// proxyArgs — ключи командной строки для программы на Chromium.
func proxyArgs(proxy string) []string {
	return []string{"--proxy-server=http://" + proxy, "--proxy-bypass-list=<-loopback>"}
}

// currentExe — где программа лежит сейчас. Discord, Slack и другие программы с установщиком
// Squirrel при каждом обновлении переезжают в новую папку app-<версия>: путь, добавленный в
// список, перестаёт существовать, хотя программа на месте. Тогда берётся самая свежая из
// соседних папок app-*, в которой есть файл с тем же именем.
func currentExe(exe string) string {
	if _, err := os.Stat(exe); err == nil {
		return exe
	}
	dir := filepath.Dir(exe)
	if !strings.HasPrefix(strings.ToLower(filepath.Base(dir)), "app-") {
		return exe
	}
	parent := filepath.Dir(dir)
	entries, err := os.ReadDir(parent)
	if err != nil {
		return exe
	}
	best, newest := exe, time.Time{}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(strings.ToLower(e.Name()), "app-") {
			continue
		}
		p := filepath.Join(parent, e.Name(), filepath.Base(exe))
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.ModTime().After(newest) {
			best, newest = p, fi.ModTime()
		}
	}
	return best
}

// runViaProxy запускает программу через прокси службы. chromium — как узнано isChromium.
func runViaProxy(exe, proxy string) (chromium bool, err error) {
	if !strings.EqualFold(filepath.Ext(exe), ".exe") {
		return false, errors.New("это не программа")
	}
	if _, err := os.Stat(exe); err != nil {
		return false, errors.New("файл программы не найден — возможно, её обновили или удалили: добавьте её заново")
	}
	chromium = isChromium(exe)
	var args []string
	if chromium {
		args = proxyArgs(proxy)
	}
	cmd := exec.Command(exe, args...)
	cmd.Dir = filepath.Dir(exe)
	cmd.Env = proxyEnv(os.Environ(), proxy)
	if err := cmd.Start(); err != nil {
		return chromium, err
	}
	return chromium, cmd.Process.Release()
}

// programsStatus — что окну нужно знать о прокси программ.
type programsStatus struct {
	Programs struct {
		Proxy string `json:"proxy"`
		Error string `json:"error"`
		Apps  []struct {
			Path string `json:"path"`
		} `json:"apps"`
	} `json:"programs"`
}

// handleProgramAdd открывает окно выбора программы и добавляет её в список.
func (t *tray) handleProgramAdd(w http.ResponseWriter, r *http.Request) {
	path, err := t.app.Dialog.OpenFile().
		SetTitle("Выберите программу, которую запускать через Cloudflare").
		AddFilter("Программы", "*.exe").
		CanChooseFiles(true).
		AttachToWindow(t.window).
		PromptForSingleSelection()
	resp := map[string]any{}
	switch {
	case err != nil:
		resp["error"] = err.Error()
	case path == "":
		// окно выбора закрыли
	default:
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		var status json.RawMessage
		if err := t.call(ctx, "add_app", map[string]string{"path": path}, &status); err != nil {
			resp["error"] = err.Error()
		} else {
			resp["status"], resp["path"] = status, path
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleProgramRun запускает программу из списка через прокси службы. Если программа уже
// запущена, новый запуск ей прокси не задаст: окно спрашивает, закрыть ли её (restart).
func (t *tray) handleProgramRun(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path    string `json:"path"`
		Restart bool   `json:"restart"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	fail := func(msg string) { writeJSON(w, http.StatusOK, map[string]any{"error": msg}) }
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var st programsStatus
	if err := t.call(ctx, "status", nil, &st); err != nil {
		fail("Служба FI не отвечает")
		return
	}
	// Запускать можно только то, что человек сам добавил в список.
	listed := false
	for _, a := range st.Programs.Apps {
		listed = listed || strings.EqualFold(a.Path, req.Path)
	}
	switch {
	case !listed:
		fail("этой программы нет в списке")
		return
	case st.Programs.Proxy == "":
		msg := st.Programs.Error
		if msg == "" {
			msg = "прокси программ ещё не запущен — попробуйте через несколько секунд"
		}
		fail(msg)
		return
	}
	exe := currentExe(req.Path)
	if pids := runningCopies(exe); len(pids) > 0 {
		if !req.Restart {
			writeJSON(w, http.StatusOK, map[string]any{"running": true})
			return
		}
		closeCopies(pids)
	}
	chromium, err := runViaProxy(exe, st.Programs.Proxy)
	if err != nil {
		fail(err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"started": true, "chromium": chromium})
}

// handleCloudflareOpen открывает страницу Cloudflare из мастера настройки. Адреса заданы
// в коде: открыть произвольную ссылку окно попросить не может.
func (t *tray) handleCloudflareOpen(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Page string `json:"page"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	pages := map[string]string{
		"signup":    cfdeploy.SignUpURL,
		"token":     cfdeploy.TokenURL,
		"dashboard": "https://dash.cloudflare.com/?to=/:account/workers-and-pages",
	}
	url, ok := pages[req.Page]
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "нет такой страницы"})
		return
	}
	if err := t.app.Browser.OpenURL(url); err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"error": "Не удалось открыть браузер: " + err.Error(), "url": url})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}
