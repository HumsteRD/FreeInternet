package daemon

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"fi/internal/strategy"
)

func TestCustomStrategies(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "base")
	for _, f := range []string{"bin/fake.bin", "lists/list-general.txt"} {
		os.MkdirAll(filepath.Dir(filepath.Join(base, f)), 0o755)
		os.WriteFile(filepath.Join(base, f), nil, 0o644)
	}
	d := &Daemon{dataDir: dir, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	d.cfg.BaseDir = base
	d.baseStrategies = []strategy.Strategy{{Name: "general (ALT)"}}
	d.rebuildStrategiesLocked()

	// Пользовательские списки FI создаёт сам при запуске: в наборе их нет, но .bat Flowseal на них ссылается.
	text := "--wf-tcp=443 --filter-tcp=443 --hostlist=%LISTS%list-general.txt --hostlist=%LISTS%list-general-user.txt " +
		"--hostlist-exclude=%LISTS%list-exclude-user.txt --dpi-desync=fake --dpi-desync-fake-tls=%BIN%fake.bin"
	if err := d.AddCustomStrategy("мой", text); err != nil {
		t.Fatal(err)
	}
	if err := d.AddCustomStrategy("нет файла", "--wf-tcp=443 --dpi-desync-fake-tls=%BIN%missing.bin"); err == nil {
		t.Fatal("стратегия со ссылкой на отсутствующий файл принята")
	}
	if !hasStrategy(d.strategies, "свой: мой") || len(d.strategies) != 2 {
		t.Fatalf("стратегии: %v", strategyNames(d.strategies))
	}

	// После перезапуска службы свои стратегии на месте.
	d2 := &Daemon{dataDir: dir, log: d.log}
	d2.loadCustom()
	if !slices.Equal(d2.customNamesLocked(), []string{"свой: мой"}) {
		t.Fatalf("после загрузки: %v", d2.customNamesLocked())
	}

	d.cfg.Strategy = "свой: мой"
	if err := d.RemoveCustomStrategy("свой: мой"); err != nil {
		t.Fatal(err)
	}
	if hasStrategy(d.strategies, "свой: мой") || d.cfg.Strategy != "" {
		t.Fatal("удалённая стратегия осталась выбранной")
	}
}
