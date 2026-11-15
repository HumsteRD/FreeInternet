package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"testing"

	"fi/internal/config"
	"fi/internal/probe"
	"fi/internal/tgproxy"
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

// TestAddViaWorkerBehindCloudflare: сайт, который сам стоит за Cloudflare, воркер открыть не
// может. Раньше FI молча добавлял его в список, и сайт просто не открывался; теперь человек
// сразу узнаёт, что через воркер это не получится.
func TestAddViaWorkerBehindCloudflare(t *testing.T) {
	d := &Daemon{
		dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runCtx: context.Background(), cfg: config.Default(),
	}
	d.cfg.Telegram.Enabled = false
	d.tg = &tgproxy.Server{LookupIPs: func(_ context.Context, host string) []string {
		switch host {
		case "chat.example": // адреса Cloudflare
			return []string{"104.18.32.47", "172.64.155.209"}
		case "music.example":
			return []string{"18.66.122.5"}
		}
		return nil
	}}

	err := d.AddViaWorker("https://chat.example/app")
	if err == nil || !strings.Contains(err.Error(), "за Cloudflare") || !strings.Contains(err.Error(), "VPN") {
		t.Fatalf("сайт за Cloudflare добавлен без объяснения: %v", err)
	}
	if len(d.cfg.ViaWorker) != 0 {
		t.Fatalf("сайт за Cloudflare попал в список: %v", d.cfg.ViaWorker)
	}
	if err := d.AddViaWorker("music.example"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(d.cfg.ViaWorker, "music.example") {
		t.Fatalf("обычный сайт не добавлен: %v", d.cfg.ViaWorker)
	}

	// Сайт из списка прежней версии FI, оказавшийся за Cloudflare, окно помечает.
	d.mu.Lock()
	d.cfg.ViaWorker = append(d.cfg.ViaWorker, "chat.example")
	d.mu.Unlock()
	d.markCloudflareSites(context.Background())
	d.mu.Lock()
	blocked := d.tg.KnownBehindCloudflare(d.cfg.ViaWorker)
	d.mu.Unlock()
	if !slices.Equal(blocked, []string{"chat.example"}) {
		t.Fatalf("помечены %v, ждали chat.example", blocked)
	}
}
