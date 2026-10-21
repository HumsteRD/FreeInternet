package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"fi/internal/lists"
	"fi/internal/strategy"
)

// customName — свои стратегии человека: лежат в папке данных, закрытой от записи
// пользователями, и при каждой загрузке проверяются заново.
const customName = "custom-strategies.json"

// loadCustom читает свои стратегии; негодные пропускаются с записью в журнал.
func (d *Daemon) loadCustom() {
	data, err := os.ReadFile(filepath.Join(d.dataDir, customName))
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			d.log.Warn("свои стратегии не прочитаны", "err", err)
		}
		return
	}
	var list []strategy.Strategy
	if err := json.Unmarshal(data, &list); err != nil {
		d.log.Warn("свои стратегии повреждены", "err", err)
		return
	}
	d.custom = d.custom[:0]
	for _, s := range list {
		if !strings.HasPrefix(s.Name, strategy.CustomPrefix) {
			continue
		}
		if err := strategy.ValidateCustom(s.Args); err != nil {
			d.log.Warn("своя стратегия пропущена", "name", s.Name, "err", err)
			continue
		}
		d.custom = append(d.custom, s)
	}
}

func (d *Daemon) saveCustomLocked() error {
	data, err := json.MarshalIndent(d.custom, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(d.dataDir, customName)
	if err := os.WriteFile(path+".tmp", data, 0o644); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

// rebuildStrategiesLocked — стратегии для выбора и подбора: набор и свои. Свои без набора не
// работают: их файлы подделок и списки — из набора.
func (d *Daemon) rebuildStrategiesLocked() {
	d.strategies = slices.Clone(d.baseStrategies)
	if d.baseErr == nil {
		d.strategies = append(d.strategies, d.custom...)
	}
}

// AddCustomStrategy проверяет и добавляет свою стратегию. text — содержимое .bat или аргументы winws.
func (d *Daemon) AddCustomStrategy(name, text string) error {
	s, err := strategy.ParseCustom(name, text)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.baseErr != nil {
		return errors.New("сначала установите набор стратегий: свои берут из него файлы подделок и списки")
	}
	for _, file := range s.CustomFiles() {
		// Пользовательские списки FI пишет сам при запуске обхода — в наборе их нет.
		if name, ok := strings.CutPrefix(file, "lists/"); ok && lists.Generated(name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(d.cfg.BaseDir, filepath.FromSlash(file))); err != nil {
			return fmt.Errorf("в наборе нет файла %s — стратегия не запустится", file)
		}
	}
	if hasStrategy(d.baseStrategies, s.Name) {
		return fmt.Errorf("стратегия %q уже есть", s.Name)
	}
	d.custom = slices.DeleteFunc(d.custom, func(c strategy.Strategy) bool { return c.Name == s.Name }) // замена
	d.custom = append(d.custom, s)
	d.rebuildStrategiesLocked()
	if err := d.saveCustomLocked(); err != nil {
		return err
	}
	d.log.Info("добавлена своя стратегия", "name", s.Name)
	return nil
}

// RemoveCustomStrategy удаляет свою стратегию. Если она выбрана, нужен новый подбор.
func (d *Daemon) RemoveCustomStrategy(name string) error {
	d.mu.Lock()
	n := len(d.custom)
	d.custom = slices.DeleteFunc(d.custom, func(c strategy.Strategy) bool { return c.Name == name })
	if len(d.custom) == n {
		d.mu.Unlock()
		return fmt.Errorf("своей стратегии %q нет", name)
	}
	d.rebuildStrategiesLocked()
	d.cfg.Ranking = slices.DeleteFunc(d.cfg.Ranking, func(s string) bool { return s == name })
	current := d.cfg.Strategy == name
	if current {
		d.cfg.Strategy = ""
	}
	err := errors.Join(d.saveCustomLocked(), d.saveLocked())
	d.mu.Unlock()
	if current {
		d.stopEngine() // стратегии больше нет — без подбора обход не запустить
	}
	return err
}

// customNamesLocked — названия своих стратегий для окна.
func (d *Daemon) customNamesLocked() []string {
	names := make([]string, len(d.custom))
	for i, s := range d.custom {
		names[i] = s.Name
	}
	return names
}
