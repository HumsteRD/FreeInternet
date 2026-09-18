package daemon

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"fi/internal/config"
	"fi/internal/flowseal"
	"fi/internal/strategy"
)

const manifestName = "manifest.json"

// manifest — sha256 всех файлов набора на момент установки. Служба запускает winws
// от имени системы, поэтому подменённый после установки файл не должен запуститься.
type manifest struct {
	Source   string            `json:"source"`
	Version  string            `json:"version,omitempty"`
	Imported time.Time         `json:"imported"`
	Files    map[string]string `json:"files"` // путь внутри набора → sha256
}

// loadBase проверяет хеши установленного набора и читает его стратегии.
func loadBase(dir string) ([]strategy.Strategy, manifest, error) {
	if dir == "" {
		return nil, manifest{}, errors.New("набор стратегий не установлен")
	}
	m, err := verifyManifest(dir)
	if err != nil {
		return nil, manifest{}, err
	}
	strategies, err := strategy.ImportFlowsealDir(dir)
	return strategies, m, err
}

// ImportBase устанавливает набор из папки — команда fi-service import-base при остановленной службе.
// Возвращает число стратегий.
func ImportBase(dataDir, src string) (int, error) {
	dst, strategies, _, err := installBase(dataDir, src)
	if err != nil {
		return 0, err
	}
	cfg, err := config.Load(configPath(dataDir))
	if err != nil {
		return 0, err
	}
	cfg.BaseDir = dst
	if !hasStrategy(strategies, cfg.Strategy) {
		cfg.Strategy = ""
	}
	return len(strategies), config.Save(configPath(dataDir), cfg)
}

// installBase копирует набор zapret-discord-youtube из src в dataDir/base, запоминает хеши
// файлов и подменяет прежний набор. Движок, который держит файлы набора, должен быть остановлен.
func installBase(dataDir, src string) (string, []strategy.Strategy, manifest, error) {
	fail := func(err error) (string, []strategy.Strategy, manifest, error) { return "", nil, manifest{}, err }

	strategies, err := strategy.ImportFlowsealDir(src)
	if err != nil {
		return fail(err)
	}
	if _, err := os.Stat(filepath.Join(src, "bin", "winws.exe")); err != nil {
		return fail(fmt.Errorf("в наборе нет bin\\winws.exe: %w", err))
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return fail(err)
	}
	if err := secureDir(dataDir); err != nil {
		return fail(fmt.Errorf("не удалось защитить папку данных: %w", err))
	}

	dst := filepath.Join(dataDir, "base")
	tmp, old := dst+".new", dst+".old"
	if err := os.RemoveAll(tmp); err != nil {
		return fail(err)
	}
	if err := copyTree(filepath.Join(src, "bin"), filepath.Join(tmp, "bin"), nil); err != nil {
		return fail(err)
	}
	notUser := func(name string) bool { return !strings.HasSuffix(name, "-user.txt") }
	if err := copyTree(filepath.Join(src, "lists"), filepath.Join(tmp, "lists"), notUser); err != nil {
		return fail(err)
	}
	files := []string{"service.bat"} // в нём версия набора
	for _, s := range strategies {
		files = append(files, s.Source)
	}
	for _, name := range files {
		if err := copyFile(filepath.Join(src, name), filepath.Join(tmp, name)); err != nil && !(name == "service.bat" && errors.Is(err, fs.ErrNotExist)) {
			return fail(err)
		}
	}

	m := manifest{Source: src, Version: flowseal.Version(src), Imported: time.Now()}
	if m.Files, err = hashTree(tmp); err != nil {
		return fail(err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, manifestName), data, 0o644); err != nil {
		return fail(err)
	}

	// Подмена переименованиями: если что-то сорвётся, прежний набор остаётся на месте.
	os.RemoveAll(old)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, old); err != nil {
			os.RemoveAll(tmp)
			return fail(fmt.Errorf("прежний набор занят — остановите обход: %w", err))
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Rename(old, dst)
		return fail(err)
	}
	// Прежний набор может не удалиться: его WinDivert64.sys занят, пока драйвер загружен.
	// Тогда папка уйдёт при следующей уборке — когда движок и драйвер остановлены.
	os.RemoveAll(old)
	return dst, strategies, m, nil
}

// removeOldBase убирает прежний набор, оставшийся после подмены. Вызывается, когда движок
// остановлен: занятый файл драйвера удалить нельзя.
func removeOldBase(dataDir string) {
	os.RemoveAll(filepath.Join(dataDir, "base.old"))
}

func hasStrategy(list []strategy.Strategy, name string) bool {
	return slices.ContainsFunc(list, func(s strategy.Strategy) bool { return s.Name == name })
}

func verifyManifest(dir string) (manifest, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestName))
	if err != nil {
		return manifest{}, fmt.Errorf("у набора нет списка хешей — установите его заново: %w", err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, fmt.Errorf("список хешей набора повреждён: %w", err)
	}
	actual, err := hashTree(dir)
	if err != nil {
		return manifest{}, err
	}
	names := make([]string, 0, len(m.Files)+len(actual))
	for name := range m.Files {
		names = append(names, name)
	}
	for name := range actual {
		if _, ok := m.Files[name]; !ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if m.Files[name] != actual[name] {
			return manifest{}, fmt.Errorf("файл набора %s изменён после установки — установите набор заново", name)
		}
	}
	return m, nil
}

func hashTree(root string) (map[string]string, error) {
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == manifestName {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		files[rel] = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	return files, err
}

func copyTree(src, dst string, keep func(name string) bool) error {
	return filepath.WalkDir(src, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if keep != nil && !keep(e.Name()) {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
