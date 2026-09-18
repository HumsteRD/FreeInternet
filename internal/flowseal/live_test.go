//go:build live

// Живая проверка загрузки настоящего релиза:
//
//	go test -tags live -run Live -v ./internal/flowseal
package flowseal

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"fi/internal/strategy"
)

func TestLiveRelease(t *testing.T) {
	LatestURL = "https://api.github.com/repos/Flowseal/zapret-discord-youtube/releases/latest"
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	client := &http.Client{Timeout: time.Minute}

	rel, err := Latest(ctx, client)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("релиз %s от %s, %d байт, sha256 %s…", rel.Version, rel.Published.Format("2006-01-02"), rel.Size, rel.SHA256[:12])

	start := time.Now()
	set, err := Download(ctx, client, rel, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	strategies, err := strategy.ImportFlowsealDir(set)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(set, "bin", "winws.exe")); err != nil {
		t.Fatal(err)
	}
	if v := Version(set); v != rel.Version {
		t.Errorf("версия в service.bat %q, у релиза %q", v, rel.Version)
	}
	t.Logf("скачан и распакован за %s: стратегий %d", time.Since(start).Round(time.Millisecond), len(strategies))
}
