package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"testing"

	"fi/internal/probe"
)

func TestYouTubeViaWorker(t *testing.T) {
	stopped, cancel := context.WithCancel(context.Background())
	cancel() // проверки после переключения не ходят в сеть
	d := &Daemon{
		dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runCtx: stopped, prober: probe.New(), http: &http.Client{},
	}
	if err := d.SetYouTubeViaWorker(true); err == nil || d.cfg.YouTubeViaWorker {
		t.Fatal("без своего воркера включать нечего")
	}

	d.cfg.Telegram.Enabled, d.cfg.Telegram.Worker = true, "fi.example.workers.dev"
	d.cfg.Telegram.MiniApps = true // локальный прокси уже нужен — перезапуск прокси Telegram не требуется
	if err := d.SetYouTubeViaWorker(true); err != nil {
		t.Fatal(err)
	}
	if !d.cfg.ViaWorkerBrowsers || !slices.Equal(d.fallbackList(), []string{"googlevideo.com"}) {
		t.Fatalf("видеосерверы должны пойти через FI и в браузерах: browsers=%v list=%v", d.cfg.ViaWorkerBrowsers, d.fallbackList())
	}
	// Человек убрал сценарий прокси — без него свежие ролики через воркер не работают.
	if err := d.SetViaWorkerBrowsers(false); err != nil {
		t.Fatal(err)
	}
	if d.cfg.YouTubeViaWorker || d.fallbackList() != nil {
		t.Fatal("свежие ролики через воркер остались включены без сценария прокси")
	}
	d.tasks.Wait()
}
