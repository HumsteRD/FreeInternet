package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// telegramClient — программа Telegram на компьютере: официальная или форк вроде AyuGram.
type telegramClient struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// clientExes — исполняемые файлы клиентов Telegram и их названия.
var clientExes = map[string]string{
	"telegram.exe":     "Telegram",
	"ayugram.exe":      "AyuGram",
	"kotatogram.exe":   "Kotatogram",
	"64gram.exe":       "64Gram",
	"materialgram.exe": "materialgram",
	"ime.exe":          "iMe",
}

// clientName — как показать программу: известное имя или имя файла.
func clientName(path string) string {
	base := filepath.Base(path)
	if name, ok := clientExes[strings.ToLower(base)]; ok {
		return name
	}
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// collectClients оставляет существующие файлы .exe без повторов, в порядке поиска.
func collectClients(paths []string) []telegramClient {
	var clients []telegramClient
	seen := map[string]bool{}
	for _, p := range paths {
		if p == "" || !strings.EqualFold(filepath.Ext(p), ".exe") {
			continue
		}
		key := strings.ToLower(filepath.Clean(p))
		if seen[key] {
			continue
		}
		if info, err := os.Stat(p); err != nil || info.IsDir() {
			continue
		}
		seen[key] = true
		clients = append(clients, telegramClient{Name: clientName(p), Path: p})
	}
	return clients
}

// openInClient открывает ссылку прокси в конкретном клиенте: Telegram Desktop и его форки
// принимают ссылку после «--», так же их вызывает сама Windows.
func openInClient(path, link string) error {
	if !strings.EqualFold(filepath.Ext(path), ".exe") {
		return errors.New("это не программа")
	}
	if _, err := os.Stat(path); err != nil {
		return err
	}
	cmd := exec.Command(path, "--", link)
	cmd.Dir = filepath.Dir(path)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

// traySettings — настройки окна, которые принадлежат пользователю, а не службе.
type traySettings struct {
	TelegramClient string `json:"telegram_client,omitempty"` // чем открывать ссылку прокси
}

func traySettingsPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "FI", "tray.json")
}

func loadTraySettings() traySettings {
	var s traySettings
	if data, err := os.ReadFile(traySettingsPath()); err == nil {
		json.Unmarshal(data, &s)
	}
	return s
}

func saveTraySettings(s traySettings) error {
	path := traySettingsPath()
	if path == "" {
		return errors.New("не найдена папка настроек пользователя")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
