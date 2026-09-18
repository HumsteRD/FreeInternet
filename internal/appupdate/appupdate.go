// Package appupdate обновляет сам FI. Служба раз в сутки читает манифест с адресом и хешем
// установщика и проверяет его подпись ed25519 открытым ключом, вшитым при сборке, затем скачивает
// установщик и сверяет хеш. Подменить обновление, не зная секретного ключа, нельзя.
package appupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ManifestURL и PublicKey (ed25519, base64) задаются при сборке через -ldflags -X.
// Пока хотя бы одно пусто, автообновление приложения выключено.
var (
	ManifestURL string
	PublicKey   string
)

// ErrNotConfigured — сборка без адреса обновлений или ключа.
var ErrNotConfigured = errors.New("автообновление не настроено в этой сборке")

const (
	maxManifest  = 64 << 10
	maxInstaller = 256 << 20
)

// Manifest — описание вышедшей версии.
type Manifest struct {
	Version   string    `json:"version"`
	URL       string    `json:"url"` // установщик fi-setup.exe
	SHA256    string    `json:"sha256"`
	Size      int64     `json:"size"`
	Published time.Time `json:"published"`
	Notes     string    `json:"notes,omitempty"`
}

var (
	versionRe = regexp.MustCompile(`^[0-9A-Za-z.\-]{1,32}$`)
	sha256Re  = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Configured сообщает, вшиты ли адрес манифеста и ключ.
func Configured() bool { return ManifestURL != "" && PublicKey != "" }

// Check читает манифест последней версии и проверяет его подпись.
func Check(ctx context.Context, client *http.Client) (Manifest, error) {
	if !Configured() {
		return Manifest{}, ErrNotConfigured
	}
	return check(ctx, client, ManifestURL, PublicKey)
}

func check(ctx context.Context, client *http.Client, url, publicKey string) (Manifest, error) {
	pub, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return Manifest{}, errors.New("в сборке неверный открытый ключ обновлений")
	}
	body, err := fetch(ctx, client, url, maxManifest)
	if err != nil {
		return Manifest{}, err
	}
	sigText, err := fetch(ctx, client, url+".sig", 1<<10)
	if err != nil {
		return Manifest{}, err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigText)))
	if err != nil || len(sig) != ed25519.SignatureSize || !ed25519.Verify(pub, body, sig) {
		return Manifest{}, errors.New("подпись обновления не сходится — обновление отклонено")
	}

	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, fmt.Errorf("манифест обновления: %w", err)
	}
	switch {
	case !versionRe.MatchString(m.Version):
		return Manifest{}, fmt.Errorf("манифест обновления: странная версия %q", m.Version)
	case !strings.HasPrefix(m.URL, "https://"):
		return Manifest{}, errors.New("манифест обновления: установщик должен скачиваться по https")
	case !sha256Re.MatchString(m.SHA256):
		return Manifest{}, errors.New("манифест обновления: нет хеша установщика")
	case m.Size <= 0 || m.Size > maxInstaller:
		return Manifest{}, fmt.Errorf("манифест обновления: странный размер %d", m.Size)
	}
	return m, nil
}

// Download скачивает установщик в dir и сверяет размер и хеш. Папка должна быть закрыта от записи
// пользователями, иначе файл можно подменить между проверкой и запуском.
func Download(ctx context.Context, client *http.Client, m Manifest, dir string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.URL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("установщик не скачался: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("установщик не скачался: HTTP %d", resp.StatusCode)
	}

	path := filepath.Join(dir, "fi-setup-"+m.Version+".exe")
	tmp, err := os.CreateTemp(dir, "download-*.tmp")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, m.Size+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return "", fmt.Errorf("установщик не скачался: %w", err)
	case n != m.Size:
		return "", fmt.Errorf("установщик скачался не целиком: %d из %d байт", n, m.Size)
	case hex.EncodeToString(h.Sum(nil)) != m.SHA256:
		return "", errors.New("хеш установщика не совпал с подписанным — обновление отклонено")
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}

// Sign готовит манифест и подпись к нему (base64) — для утилиты fi-sign.
func Sign(priv ed25519.PrivateKey, m Manifest) (manifest, sig []byte, err error) {
	manifest, err = json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, nil, err
	}
	manifest = append(manifest, '\n')
	sig = []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest)) + "\n")
	return manifest, sig, nil
}

// Newer сообщает, новее ли версия a, чем b: «0.10.0» новее «0.9.3».
func Newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(pa), len(pb)) {
		x, y := part(pa, i), part(pb, i)
		if x != y {
			return x > y
		}
	}
	return false
}

func part(parts []string, i int) int {
	if i >= len(parts) {
		return 0
	}
	digits := strings.TrimRightFunc(parts[i], func(r rune) bool { return r < '0' || r > '9' })
	n, _ := strconv.Atoi(digits)
	return n
}

func fetch(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("сервер обновлений: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("сервер обновлений: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("сервер обновлений: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, errors.New("сервер обновлений прислал слишком большой ответ")
	}
	return data, nil
}
