// Package flowseal скачивает набор стратегий zapret-discord-youtube (Flowseal, MIT) с GitHub.
package flowseal

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// LatestURL — адрес последнего релиза; переменная, чтобы подменить в тестах.
var LatestURL = "https://api.github.com/repos/Flowseal/zapret-discord-youtube/releases/latest"

const (
	userAgent       = "FI"
	maxArchive      = 32 << 20 // архив релиза весит ~1,5 МБ
	maxUncompressed = 64 << 20
)

// Release — релиз набора.
type Release struct {
	Version   string    `json:"version"`
	Published time.Time `json:"published"`
	URL       string    `json:"-"`
	SHA256    string    `json:"-"`
	Size      int64     `json:"-"`
}

// Latest узнаёт последний релиз и его zip-архив.
func Latest(ctx context.Context, client *http.Client) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, LatestURL, nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("GitHub ответил %d", resp.StatusCode)
	}

	var body struct {
		Tag       string    `json:"tag_name"`
		Published time.Time `json:"published_at"`
		Assets    []struct {
			Name   string `json:"name"`
			Size   int64  `json:"size"`
			Digest string `json:"digest"`
			URL    string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return Release{}, fmt.Errorf("ответ GitHub не разобран: %w", err)
	}
	for _, a := range body.Assets {
		if !strings.HasSuffix(a.Name, ".zip") {
			continue
		}
		sum, ok := strings.CutPrefix(a.Digest, "sha256:")
		if !ok || len(sum) != 64 {
			return Release{}, fmt.Errorf("у архива %s нет хеша sha256", a.Name)
		}
		return Release{Version: body.Tag, Published: body.Published, URL: a.URL, SHA256: sum, Size: a.Size}, nil
	}
	return Release{}, fmt.Errorf("в релизе %s нет zip-архива", body.Tag)
}

// Download скачивает архив релиза, сверяет sha256 и распаковывает в dir.
// Возвращает папку набора — ту, где лежат general*.bat.
func Download(ctx context.Context, client *http.Client, rel Release, dir string) (string, error) {
	archive := filepath.Join(dir, "release.zip")
	if err := fetch(ctx, client, rel, archive); err != nil {
		return "", err
	}
	if err := extract(archive, filepath.Join(dir, "release")); err != nil {
		return "", err
	}
	return findSet(filepath.Join(dir, "release"))
}

func fetch(ctx context.Context, client *http.Client, rel Release, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rel.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("скачивание: HTTP %d", resp.StatusCode)
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxArchive+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return fmt.Errorf("скачивание: %w", err)
	case n > maxArchive:
		return errors.New("архив набора подозрительно большой")
	case rel.Size > 0 && n != rel.Size:
		return fmt.Errorf("архив скачан не полностью: %d из %d байт", n, rel.Size)
	case !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), rel.SHA256):
		return errors.New("хеш архива не совпал с опубликованным — файл повреждён или подменён")
	}
	return nil
}

// extract распаковывает только обычные файлы и не выпускает их за пределы dst.
func extract(archive, dst string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer r.Close()

	var total int64
	for _, f := range r.File {
		name := path.Clean(strings.ReplaceAll(f.Name, `\`, "/"))
		if strings.HasSuffix(f.Name, "/") || f.FileInfo().IsDir() {
			continue
		}
		if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, ":") {
			return fmt.Errorf("в архиве недопустимый путь %q", f.Name)
		}
		if !f.Mode().IsRegular() {
			return fmt.Errorf("в архиве не обычный файл %q", f.Name)
		}
		total += int64(f.UncompressedSize64)
		if total > maxUncompressed {
			return errors.New("распакованный набор подозрительно большой")
		}
		if err := extractFile(f, filepath.Join(dst, filepath.FromSlash(name))); err != nil {
			return fmt.Errorf("%s: %w", f.Name, err)
		}
	}
	return nil
}

func extractFile(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	in, err := f.Open()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	// Лимит на случай, если заголовок архива врёт о размере.
	if _, err := io.Copy(out, io.LimitReader(in, int64(f.UncompressedSize64)+1)); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// findSet ищет папку с general*.bat: в релизе она лежит на уровень глубже.
func findSet(root string) (string, error) {
	var found string
	err := filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil || found != "" {
			return err
		}
		if !e.IsDir() && strings.HasPrefix(strings.ToLower(e.Name()), "general") && strings.HasSuffix(strings.ToLower(e.Name()), ".bat") {
			found = filepath.Dir(p)
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", errors.New("в архиве нет стратегий general*.bat")
	}
	return found, nil
}

var versionLine = regexp.MustCompile(`(?m)^set "LOCAL_VERSION=([^"\r\n]+)"`)

// Version читает версию набора из service.bat; пусто, если не нашлась.
func Version(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, "service.bat"))
	if err != nil {
		return ""
	}
	if m := versionLine.FindSubmatch(data); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

// Newer сообщает, новее ли версия a, чем b («1.10.2» против «1.10.0»).
// Если версии не разобрать — новой считается любая отличающаяся.
func Newer(a, b string) bool {
	if a == "" || a == b {
		return false
	}
	pa, oka := parseVersion(a)
	pb, okb := parseVersion(b)
	if !oka || !okb {
		return a != b
	}
	for i := range max(len(pa), len(pb)) {
		x, y := at(pa, i), at(pb, i)
		if x != y {
			return x > y
		}
	}
	return false
}

func parseVersion(v string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	nums := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, false
		}
		nums[i] = n
	}
	return nums, true
}

func at(v []int, i int) int {
	if i < len(v) {
		return v[i]
	}
	return 0
}
