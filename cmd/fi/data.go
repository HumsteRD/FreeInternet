package main

import (
	"archive/zip"
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fi/internal/diag"
	"fi/internal/logfile"
)

// version — версия окна; задаётся при сборке: -ldflags "-X main.version=…".
var version = "dev"

// serviceDataDir — папка службы: журналы и настройки. Пользователям она доступна для чтения.
func serviceDataDir() string { return filepath.Join(os.Getenv("ProgramData"), "FI") }

// trayLogPath — журнал окна: служба пишет свой в serviceDataDir, окно — в профиль пользователя.
func trayLogPath() string {
	dir, err := os.UserCacheDir() // %LOCALAPPDATA%
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "FI", "fi.log")
}

// openTrayLog направляет журнал окна в файл: без консоли записи иначе пропадают.
func openTrayLog() {
	path := trayLogPath()
	if path == "" {
		return
	}
	if f, err := logfile.Open(path, 1<<20); err == nil {
		log.SetOutput(f)
	}
}

// siteEntry — сайт из списка службы.
type siteEntry struct {
	Host   string `json:"host"`
	Parent string `json:"parent,omitempty"`
}

// bom — метка UTF-8 в начале файла, которую ставит Блокнот.
const bom = "\xef\xbb\xbf"

const sitesHeader = "# Сайты FI. Строка — сайт; строки с отступом — домены, добавленные вместе с ним.\n" +
	"# Загрузить список: FI → Добавить сайт → «Загрузить из файла».\n"

// formatSites записывает список сайтов в текст: связанные домены — с отступом под своим сайтом.
func formatSites(sites []siteEntry) string {
	var b strings.Builder
	b.WriteString(sitesHeader)
	for _, s := range sites {
		if s.Parent != "" {
			continue
		}
		b.WriteString(s.Host + "\n")
		for _, c := range sites {
			if c.Parent == s.Host {
				b.WriteString("    " + c.Host + "\n")
			}
		}
	}
	// Связанные домены, чей сайт уже удалён, не теряем.
	for _, c := range sites {
		if c.Parent != "" && !hasHost(sites, c.Parent) {
			b.WriteString(c.Host + "\n")
		}
	}
	return b.String()
}

func hasHost(sites []siteEntry, host string) bool {
	for _, s := range sites {
		if s.Host == host {
			return true
		}
	}
	return false
}

// parseSites читает список сайтов: formatSites или просто по адресу в строке.
func parseSites(r io.Reader) []siteEntry {
	var out []siteEntry
	parent := ""
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		text, _, _ := strings.Cut(line, "#")
		text = strings.TrimSpace(strings.TrimPrefix(text, bom)) // Блокнот сохраняет с BOM
		if text == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			out = append(out, siteEntry{Host: text, Parent: parent})
			continue
		}
		parent = text
		out = append(out, siteEntry{Host: text})
	}
	return out
}

func (t *tray) handleSitesExport(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	var st struct {
		Sites []siteEntry `json:"sites"`
	}
	if err := t.call(ctx, "status", nil, &st); err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"error": "Служба FI не отвечает"})
		return
	}
	if len(st.Sites) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"error": "Список сайтов пуст — сохранять нечего"})
		return
	}
	path, err := t.app.Dialog.SaveFile().
		SetFilename("FI-сайты.txt").
		AddFilter("Текст", "*.txt").
		AttachToWindow(t.window).
		PromptForSingleSelection()
	resp := map[string]string{}
	switch {
	case err != nil:
		resp["error"] = err.Error()
	case path == "":
	default:
		if !strings.EqualFold(filepath.Ext(path), ".txt") {
			path += ".txt"
		}
		if err := os.WriteFile(path, []byte(formatSites(st.Sites)), 0o644); err != nil {
			resp["error"] = err.Error()
		} else {
			resp["path"] = path
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func (t *tray) handleSitesImport(w http.ResponseWriter, r *http.Request) {
	path, err := t.app.Dialog.OpenFile().
		SetTitle("Список сайтов FI").
		AddFilter("Текст", "*.txt").
		CanChooseFiles(true).
		AttachToWindow(t.window).
		PromptForSingleSelection()
	if err != nil || path == "" {
		resp := map[string]string{}
		if err != nil {
			resp["error"] = err.Error()
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
		return
	}
	sites := parseSites(io.LimitReader(f, 1<<20))
	f.Close()
	if len(sites) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"error": "В файле нет адресов сайтов"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	var res struct {
		Added  int             `json:"added"`
		Status json.RawMessage `json:"status"`
	}
	if err := t.call(ctx, "import_sites", map[string]any{"sites": sites}, &res); err != nil {
		writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"added": res.Added, "status": res.Status})
}

// handleLogs открывает папку с журналом службы.
func (t *tray) handleLogs(w http.ResponseWriter, _ *http.Request) {
	resp := map[string]string{}
	if err := exec.Command("explorer.exe", serviceDataDir()).Start(); err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleReport сохраняет отчёт для разработчика одним архивом туда, куда укажет человек.
func (t *tray) handleReport(w http.ResponseWriter, r *http.Request) {
	path, err := t.app.Dialog.SaveFile().
		SetFilename("FI-отчёт-"+time.Now().Format("2006-01-02-1504")+".zip").
		AddFilter("Архив", "*.zip").
		AttachToWindow(t.window).
		PromptForSingleSelection()
	resp := map[string]string{}
	switch {
	case err != nil:
		resp["error"] = err.Error()
	case path == "":
	default:
		if !strings.EqualFold(filepath.Ext(path), ".zip") {
			path += ".zip"
		}
		ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
		defer cancel()
		if err := t.writeReport(ctx, path); err != nil {
			resp["error"] = "Отчёт не сохранён: " + err.Error()
		} else {
			resp["path"] = path
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// writeReport собирает журналы службы и окна, настройки, состояние и диагностику.
// Секрет прокси Telegram в отчёт не попадает.
func (t *tray) writeReport(ctx context.Context, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	add := func(name string, data []byte) {
		if w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()}); err == nil {
			w.Write(data)
		}
	}
	var notes []string
	addFile := func(name, src string) {
		data, err := os.ReadFile(src)
		switch {
		case err == nil:
			add(name, data)
		case !errors.Is(err, os.ErrNotExist):
			notes = append(notes, fmt.Sprintf("%s: %v", name, err))
		}
	}

	dataDir := serviceDataDir()
	for _, name := range []string{"fi-service.log", "fi-service.log.old", "update.log", "telegram-cf.txt"} {
		addFile(name, filepath.Join(dataDir, name))
	}
	if p := trayLogPath(); p != "" {
		addFile("fi.log", p)
		addFile("fi.log.old", p+".old")
	}
	if data, err := os.ReadFile(filepath.Join(dataDir, "config.json")); err == nil {
		add("config.json", redact(data))
	}

	var status json.RawMessage
	if err := t.call(ctx, "status", nil, &status); err == nil {
		add("status.json", redact(status))
	} else {
		notes = append(notes, "состояние службы: "+err.Error())
	}
	var report diag.Report
	if err := t.call(ctx, "diagnose", nil, &report); err == nil {
		report.Findings = append(report.Findings, diag.EvaluateUser(diag.CollectUser())...)
		data, _ := json.MarshalIndent(report, "", "  ")
		add("diagnostics.json", data)
	} else {
		notes = append(notes, "диагностика: "+err.Error())
	}

	info := fmt.Sprintf("FI %s\nWindows, %s/%s\nОтчёт создан %s\n", version, runtime.GOOS, runtime.GOARCH, time.Now().Format(time.RFC3339))
	if len(notes) > 0 {
		info += "\nНе удалось собрать:\n" + strings.Join(notes, "\n") + "\n"
	}
	add("info.txt", []byte(info))

	if err := zw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// redact убирает из настроек и состояния секрет прокси Telegram и ссылку с ним.
func redact(data []byte) []byte {
	var v map[string]any
	if json.Unmarshal(data, &v) != nil {
		return data
	}
	if tg, ok := v["telegram"].(map[string]any); ok {
		for _, key := range []string{"secret", "link"} {
			if _, ok := tg[key]; ok {
				tg[key] = "(скрыто)"
			}
		}
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return data
	}
	return out
}
