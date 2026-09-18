package flowseal

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for name, content := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		f.Write([]byte(content))
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// releaseServer отдаёт ответ GitHub API и архив; digest можно испортить.
func releaseServer(t *testing.T, archive []byte, digest string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	mux.HandleFunc("/latest", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"tag_name":     "1.10.3",
			"published_at": "2026-09-14T10:00:00Z",
			"assets": []map[string]any{
				{"name": "zapret-discord-youtube-1.10.3.rar", "size": 1, "digest": "sha256:00", "browser_download_url": srv.URL + "/rar"},
				{"name": "zapret-discord-youtube-1.10.3.zip", "size": len(archive), "digest": "sha256:" + digest, "browser_download_url": srv.URL + "/zip"},
			},
		})
	})
	mux.HandleFunc("/zip", func(w http.ResponseWriter, r *http.Request) { w.Write(archive) })
	LatestURL = srv.URL + "/latest"
	t.Cleanup(func() { LatestURL = "" })
	return srv
}

func TestDownload(t *testing.T) {
	archive := makeZip(t, map[string]string{
		"zapret-discord-youtube-1.10.3/general.bat":       "start \"x\" /min \"%BIN%winws.exe\"\r\n",
		"zapret-discord-youtube-1.10.3/service.bat":       "@echo off\r\nset \"LOCAL_VERSION=1.10.3\"\r\n",
		"zapret-discord-youtube-1.10.3/bin/winws.exe":     "binary",
		"zapret-discord-youtube-1.10.3/lists/general.txt": "discord.com\n",
	})
	sum := sha256.Sum256(archive)
	srv := releaseServer(t, archive, hex.EncodeToString(sum[:]))
	ctx := context.Background()

	rel, err := Latest(ctx, srv.Client())
	if err != nil || rel.Version != "1.10.3" || rel.URL != srv.URL+"/zip" {
		t.Fatalf("Latest = %+v, %v", rel, err)
	}
	set, err := Download(ctx, srv.Client(), rel, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(set) != "zapret-discord-youtube-1.10.3" || Version(set) != "1.10.3" {
		t.Fatalf("набор %s, версия %q", set, Version(set))
	}
	if _, err := os.Stat(filepath.Join(set, "bin", "winws.exe")); err != nil {
		t.Fatal(err)
	}

	rel.SHA256 = hex.EncodeToString(make([]byte, 32))
	if _, err := Download(ctx, srv.Client(), rel, t.TempDir()); err == nil {
		t.Fatal("архив с чужим хешем принят")
	}
}

func TestExtractRejectsEscape(t *testing.T) {
	dir := t.TempDir()
	archive := filepath.Join(dir, "evil.zip")
	os.WriteFile(archive, makeZip(t, map[string]string{"../../escape.txt": "x"}), 0o644)
	if err := extract(archive, filepath.Join(dir, "out")); err == nil {
		t.Fatal("путь за пределами папки принят")
	}
	if _, err := os.Stat(filepath.Join(dir, "..", "escape.txt")); err == nil {
		t.Fatal("файл вышел за пределы папки")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.10.3", "1.10.2", true},
		{"1.10.2", "1.10.10", false},
		{"1.11", "1.10.9", true},
		{"1.10.2", "1.10.2", false},
		{"1.10.2", "", true},
		{"", "1.10.2", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
