package daemon

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"fi/internal/engine"
	"fi/internal/strategy"
)

func TestNoRestartsDuringAntiCheatGame(t *testing.T) {
	old := antiCheat
	antiCheat = func() string { return "EasyAntiCheat" }
	defer func() { antiCheat = old }()

	d := &Daemon{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.strategies = []strategy.Strategy{{Name: "general (ALT)"}}
	for name, err := range map[string]error{
		"стратегия":     d.SetStrategy("general (ALT)"),
		"игровой режим": d.SetGameMode("all"),
		"набор":         d.StartBaseUpdate(),
	} {
		if err == nil || !strings.Contains(err.Error(), "EasyAntiCheat") {
			t.Errorf("%s: перезапуск во время игры с античитом не запрещён: %v", name, err)
		}
	}
	if d.cfg.Strategy != "" || d.cfg.GameMode != "" {
		t.Fatal("настройки изменились, хотя перезапуск запрещён")
	}

	stuck := &engine.RunError{AtStart: true, Output: "windivert: error opening filter: The object is referenced by other objects so cannot be deleted."}
	if !stuck.WFPInUse() {
		t.Fatal("FWP_E_IN_USE не распознан")
	}
	if text := engineErrorText(stuck); !strings.Contains(text, "Выйдите из игры") || strings.Contains(text, "перезагрузите") {
		t.Fatalf("во время игры окно должно просить выйти из игры, а не перезагружаться: %q", text)
	}
	antiCheat = func() string { return "" }
	if text := engineErrorText(stuck); !strings.Contains(text, "перезагрузите") {
		t.Fatalf("без игры и без лечения — перезагрузка: %q", text)
	}
	if err := restartAllowed(); err != nil {
		t.Fatal(err)
	}
}
