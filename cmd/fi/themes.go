package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"slices"

	"github.com/wailsapp/wails/v3/pkg/application"
)

// themeBackgrounds — цвет фона окна для каждой темы (--bg в app.css): им окно закрашено,
// пока страница не отрисовалась.
var themeBackgrounds = map[string]application.RGBA{
	"dark":   application.NewRGB(0x0B, 0x0A, 0x0D),
	"gray":   application.NewRGB(0x1E, 0x1F, 0x22),
	"light":  application.NewRGB(0xF6, 0xF4, 0xF8),
	"anime":  application.NewRGB(0x1A, 0x10, 0x1F),
	"mono":   application.NewRGB(0x00, 0x00, 0x00),
	"neon":   application.NewRGB(0x07, 0x08, 0x12),
	"forest": application.NewRGB(0x0C, 0x14, 0x10),
	"ocean":  application.NewRGB(0x08, 0x11, 0x1C),
}

const defaultTheme = "dark"

// Оформление окна, кроме темы: всё необязательно, пусто — как задумано темой.
var (
	lookFonts   = []string{"", "onest", "segoe", "bahnschrift", "mono", "georgia", "handwritten"}
	lookRadii   = []string{"", "sharp", "round"}
	lookScales  = []int{0, 90, 110, 120}
	accentColor = regexp.MustCompile(`^#[0-9A-Fa-f]{6}$`)
)

// look — оформление окна, как его видит страница.
type look struct {
	Theme  string `json:"theme"`
	Accent string `json:"accent"` // #RRGGBB; пусто — цвет темы
	Font   string `json:"font"`
	Radius string `json:"radius"` // sharp, round; пусто — обычные углы
	Scale  int    `json:"scale"`  // 90, 110, 120 процентов; 0 — 100
	Motion bool   `json:"motion"` // анимации
}

// themeOf — сохранённая тема или тёмная, если не выбрана или неизвестна.
func themeOf(s traySettings) string {
	if _, ok := themeBackgrounds[s.Theme]; ok {
		return s.Theme
	}
	return defaultTheme
}

func lookOf(s traySettings) look {
	return look{Theme: themeOf(s), Accent: s.Accent, Font: s.Font, Radius: s.Radius, Scale: s.Scale, Motion: !s.NoMotion}
}

// lookChange — что поменять: только присланные поля.
type lookChange struct {
	Theme  *string `json:"theme"`
	Accent *string `json:"accent"`
	Font   *string `json:"font"`
	Radius *string `json:"radius"`
	Scale  *int    `json:"scale"`
	Motion *bool   `json:"motion"`
}

// apply проверяет и применяет изменения к настройкам окна.
func (c lookChange) apply(s *traySettings) error {
	switch {
	case c.Theme != nil && themeBackgrounds[*c.Theme] == (application.RGBA{}):
		return errors.New("неизвестная тема " + *c.Theme)
	case c.Accent != nil && *c.Accent != "" && !accentColor.MatchString(*c.Accent):
		return errors.New("цвет — в виде #RRGGBB")
	case c.Font != nil && !slices.Contains(lookFonts, *c.Font):
		return errors.New("неизвестный шрифт " + *c.Font)
	case c.Radius != nil && !slices.Contains(lookRadii, *c.Radius):
		return errors.New("неизвестные углы " + *c.Radius)
	case c.Scale != nil && !slices.Contains(lookScales, *c.Scale):
		return errors.New("такого масштаба нет")
	}
	if c.Theme != nil {
		s.Theme = *c.Theme
	}
	if c.Accent != nil {
		s.Accent = *c.Accent
	}
	if c.Font != nil {
		s.Font = *c.Font
	}
	if c.Radius != nil {
		s.Radius = *c.Radius
	}
	if c.Scale != nil {
		s.Scale = *c.Scale
	}
	if c.Motion != nil {
		s.NoMotion = !*c.Motion
	}
	return nil
}

// handleTheme без тела сообщает оформление окна; с телом — меняет присланные поля:
// {"theme": "anime"}, {"accent": "#FF6FAE", "font": "mono"} и так далее.
func (t *tray) handleTheme(w http.ResponseWriter, r *http.Request) {
	var req lookChange
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s := loadTraySettings()
	if req == (lookChange{}) {
		writeJSON(w, http.StatusOK, lookOf(s))
		return
	}
	if err := req.apply(&s); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resp := map[string]any{"look": lookOf(s)}
	if err := saveTraySettings(s); err != nil {
		resp["error"] = "Оформление не сохранится после перезапуска: " + err.Error()
	}
	if req.Theme != nil {
		colour := themeBackgrounds[themeOf(s)]
		application.InvokeAsync(func() { t.window.SetBackgroundColour(colour) })
	}
	writeJSON(w, http.StatusOK, resp)
}
