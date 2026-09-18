// Package lists готовит рабочую папку списков winws: списки базового набора,
// пользовательские сайты и режим ipset.
package lists

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// NoneMarker — ipset из одного этого адреса означает «ipset выключен» (соглашение Flowseal).
const NoneMarker = "203.0.113.113/32"

// placeholderHost: Flowseal не оставляет пользовательские списки пустыми,
// пустой список winws понимает не как «ничего».
const placeholderHost = "domain.example.abc"

const userHostsFile = "list-general-user.txt"

// IpsetMode — какие адреса обходить по IP, а не по имени сайта.
type IpsetMode string

const (
	IpsetNone   IpsetMode = "none"   // только по спискам сайтов
	IpsetAny    IpsetMode = "any"    // любой адрес: помогает от обрыва на 16 КБ, но может ломать другие сайты
	IpsetLoaded IpsetMode = "loaded" // адреса из ipset-all набора
)

// Prepare заполняет dst списками из baseDir, пользовательскими сайтами и режимом ipset.
// Файлы базового набора не изменяются.
func Prepare(dst, baseDir string, userHosts []string, mode IpsetMode) error {
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || strings.HasSuffix(e.Name(), "-user.txt") {
			continue
		}
		if err := copyFile(filepath.Join(baseDir, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}

	files := map[string]string{
		"list-exclude-user.txt":  placeholderHost + "\n",
		"ipset-exclude-user.txt": NoneMarker + "\n",
	}
	switch mode {
	case IpsetNone, "":
		files["ipset-all.txt"] = NoneMarker + "\n"
	case IpsetAny:
		files["ipset-all.txt"] = ""
	case IpsetLoaded:
		// Flowseal при выключенном ipset хранит полный список в .backup.
		backup := filepath.Join(baseDir, "ipset-all.txt.backup")
		if _, err := os.Stat(backup); err == nil {
			if err := copyFile(backup, filepath.Join(dst, "ipset-all.txt")); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("неизвестный режим ipset %q", mode)
	}
	for name, content := range files {
		if err := writeAtomic(filepath.Join(dst, name), []byte(content)); err != nil {
			return err
		}
	}
	return WriteUserHosts(dst, userHosts)
}

// WriteUserHosts перезаписывает пользовательский список сайтов. winws перечитывает список,
// когда меняется время изменения файла, поэтому перезапускать движок не нужно.
func WriteUserHosts(dir string, hosts []string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(placeholderHost + "\n")
	for _, h := range hosts {
		b.WriteString(h + "\n")
	}
	return writeAtomic(filepath.Join(dir, userHostsFile), []byte(b.String()))
}

// writeAtomic пишет во временный файл и подменяет им целевой, чтобы winws не прочитал
// список наполовину. На Windows замена упирается в файл, открытый на чтение, — повторяем.
func writeAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		if err = os.Rename(tmp, path); err == nil {
			return nil
		}
		time.Sleep(time.Duration(attempt) * 50 * time.Millisecond)
	}
	os.Remove(tmp)
	return err
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
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
