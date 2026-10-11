package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// themeBackgrounds — цвет фона окна для каждой темы (--bg в app.css): им окно закрашено,
// пока страница не отрисовалась.
var themeBackgrounds = map[string]application.RGBA{
	"dark":  application.NewRGB(0x0B, 0x0A, 0x0D),
	"gray":  application.NewRGB(0x1E, 0x1F, 0x22),
	"light": application.NewRGB(0xF6, 0xF4, 0xF8),
}

const defaultTheme = "dark"

// themeOf — сохранённая тема или тёмная, если не выбрана или неизвестна.
func themeOf(s traySettings) string {
	if _, ok := themeBackgrounds[s.Theme]; ok {
		return s.Theme
	}
	return defaultTheme
}

// handleTheme без тела сообщает выбранную тему; с телом {"theme": "dark"|"gray"|"light"} меняет её.
func (t *tray) handleTheme(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Theme string `json:"theme"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s := loadTraySettings()
	if req.Theme != "" {
		colour, ok := themeBackgrounds[req.Theme]
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "неизвестная тема " + req.Theme})
			return
		}
		s.Theme = req.Theme
		if err := saveTraySettings(s); err != nil {
			writeJSON(w, http.StatusOK, map[string]string{"theme": req.Theme, "error": "Тема не сохранится после перезапуска: " + err.Error()})
			return
		}
		application.InvokeAsync(func() { t.window.SetBackgroundColour(colour) })
	}
	writeJSON(w, http.StatusOK, map[string]string{"theme": themeOf(s)})
}
