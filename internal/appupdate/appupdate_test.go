package appupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

type release struct {
	manifest, sig, installer []byte
}

func serve(t *testing.T, r *release) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/update.json", func(w http.ResponseWriter, _ *http.Request) { w.Write(r.manifest) })
	mux.HandleFunc("/update.json.sig", func(w http.ResponseWriter, _ *http.Request) { w.Write(r.sig) })
	mux.HandleFunc("/fi-setup.exe", func(w http.ResponseWriter, _ *http.Request) { w.Write(r.installer) })
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestCheckAndDownload(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	key := base64.StdEncoding.EncodeToString(pub)
	r := &release{installer: []byte("MZ — поддельный установщик для теста")}
	srv := serve(t, r)
	sum := sha256.Sum256(r.installer)
	m := Manifest{Version: "0.2.0", URL: srv.URL + "/fi-setup.exe", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(r.installer)), Published: time.Now().UTC()}
	var err error
	if r.manifest, r.sig, err = Sign(priv, m); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	url := srv.URL + "/update.json"

	got, err := check(ctx, srv.Client(), url, key)
	if err != nil || got.Version != "0.2.0" || got.SHA256 != m.SHA256 {
		t.Fatalf("check = %+v, %v", got, err)
	}

	dir := t.TempDir()
	path, err := Download(ctx, srv.Client(), got, dir)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); !bytes.Equal(data, r.installer) {
		t.Fatal("скачан не тот файл")
	}

	// Установщик подменили на сервере — хеш из подписанного манифеста не сойдётся.
	r.installer = []byte("MZ — чужой установщик той же длины!!!!!")[:len(r.installer)]
	if _, err := Download(ctx, srv.Client(), got, t.TempDir()); err == nil {
		t.Fatal("подменённый установщик принят")
	}

	// Манифест подменили, подпись осталась старой.
	signed := r.manifest
	r.manifest = bytes.Replace(signed, []byte("0.2.0"), []byte("9.9.9"), 1)
	if _, err := check(ctx, srv.Client(), url, key); err == nil {
		t.Fatal("подменённый манифест принят")
	}
	r.manifest = signed

	// Подпись другим ключом.
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := check(ctx, srv.Client(), url, base64.StdEncoding.EncodeToString(other)); err == nil {
		t.Fatal("манифест принят с чужим ключом")
	}

	// Подписанный, но с установщиком по http.
	m.URL = "http://example.com/fi-setup.exe"
	r.manifest, r.sig, _ = Sign(priv, m)
	if _, err := check(ctx, srv.Client(), url, key); err == nil {
		t.Fatal("принят установщик по http")
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.9", true},
		{"0.10.0", "0.9.3", true},
		{"1.0", "1.0.0", false},
		{"1.0.1", "1.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.1.0", "0.2.0-beta", false},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q, %q) = %v", c.a, c.b, got)
		}
	}
}

func TestNotConfigured(t *testing.T) {
	if Configured() {
		t.Skip("сборка с настроенным обновлением")
	}
	if _, err := Check(context.Background(), http.DefaultClient); err != ErrNotConfigured {
		t.Fatalf("Check без настройки: %v", err)
	}
}
