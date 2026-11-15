package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"fi/internal/config"
	"fi/internal/probe"
	"fi/internal/tgproxy"
)

// cloud — поддельный Cloudflare для службы: и API (api.cloudflare.com), и сам воркер
// (*.workers.dev). Запросы службы попадают сюда вместо сети.
type cloud struct {
	mu       sync.Mutex
	token    string // какой токен API принимает
	uploads  int    // сколько раз загружали код воркера
	code     string // последний загруженный код
	metadata string
	version  int // какой версией отвечает воркер
	calls    []string
	apiErr   func(path string) (status int, body string) // отказ API, если нужен
	pumpErr  error                                       // чем кончается проверка пересылки через воркер
}

func (c *cloud) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	reply := func(status int, body string) (*http.Response, error) {
		return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	}
	if strings.HasSuffix(r.URL.Host, ".workers.dev") {
		if r.URL.Query().Get("keycheck") != "" {
			return reply(200, "ключ верный")
		}
		return reply(200, fmt.Sprintf("FI worker: работает (версия %d, с ключом)", c.version))
	}
	if r.URL.Host != "api.cloudflare.com" {
		return reply(502, "чужой адрес "+r.URL.Host)
	}
	path := strings.TrimPrefix(r.URL.Path, "/client/v4")
	c.calls = append(c.calls, r.Method+" "+path)
	ok := func(result string) (*http.Response, error) {
		return reply(200, `{"success":true,"errors":[],"result":`+result+`}`)
	}
	if r.Header.Get("Authorization") != "Bearer "+c.token {
		return reply(403, `{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`)
	}
	if c.apiErr != nil {
		if status, body := c.apiErr(path); status != 0 {
			return reply(status, body)
		}
	}
	switch {
	case path == "/accounts":
		return ok(`[{"id":"acc1"}]`)
	case path == "/accounts/acc1/workers/subdomain":
		return ok(`{"subdomain":"fi-abcd"}`)
	case path == "/accounts/acc1/workers/scripts/fi-tg" && r.Method == http.MethodPut:
		r.ParseMultipartForm(1 << 20)
		c.uploads++
		c.metadata = r.FormValue("metadata")
		if f, _, err := r.FormFile("worker.js"); err == nil {
			data, _ := io.ReadAll(f)
			c.code = string(data)
		}
		c.version = tgproxy.WorkerVersionCurrent // загруженный код начинает отвечать
		return ok(`{}`)
	case path == "/accounts/acc1/workers/scripts/fi-tg/subdomain":
		return ok(`{}`)
	}
	return reply(404, `{"success":false,"errors":[{"code":7003,"message":"нет такого пути"}]}`)
}

const goodToken = "aB3dE6gH9jK2mN5pQ8sT1vW4yZ7cF0hJ3lM6oR9u"

func cloudDaemon(t *testing.T, c *cloud) *Daemon {
	t.Helper()
	old, oldVerify := workerPoll, verifyPump
	workerPoll = 5 * time.Millisecond
	verifyPump = func(context.Context, *tgproxy.Server) error { return c.pumpErr } // в сеть тест не ходит
	t.Cleanup(func() { workerPoll, verifyPump = old, oldVerify })
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		dataDir: t.TempDir(), log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		runCtx: ctx, prober: probe.New(), http: &http.Client{Transport: c},
		cfg: config.Default(),
	}
	d.cfg.Telegram.Enabled = false // прокси включит сама настройка воркера
	d.cfg.Telegram.Port = freePort(t)
	d.cfg.Telegram.WorkerKey = "0123456789abcdef0123456789abcdef"
	// Настройку окон Telegram в реестре Windows тест не трогает.
	oldPAC := setTelegramPAC
	setTelegramPAC = func(string) error { return nil }
	t.Cleanup(func() { setTelegramPAC = oldPAC })
	t.Cleanup(func() {
		cancel()
		d.tasks.Wait()
		d.stopTelegram()
	})
	return d
}

// freePort — свободный порт, за которым свободны и два следующих: их занимают прокси
// мини-приложений и прокси программ.
func freePort(t *testing.T) int {
	t.Helper()
	for range 50 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		l.Close()
		free := true
		for _, p := range []int{port + 1, port + 2} {
			n, err := net.Listen("tcp", telegramAddr(p))
			if err != nil {
				free = false
				break
			}
			n.Close()
		}
		if free {
			return port
		}
	}
	t.Fatal("нет трёх свободных портов подряд")
	return 0
}

func waitSetup(t *testing.T, d *Daemon) WorkerSetup {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if st := d.Status().Cloudflare.Setup; st != nil && !st.Busy {
			return *st
		}
	}
	t.Fatal("настройка воркера не закончилась")
	return WorkerSetup{}
}

// TestWorkerSetup: человек вставил токен и нажал «Создать воркер» — служба проверяет токен,
// загружает код с ключом FI, ждёт воркер и вписывает его адрес; токен остаётся для обновлений.
func TestWorkerSetup(t *testing.T) {
	c := &cloud{token: goodToken}
	d := cloudDaemon(t, c)

	if err := d.StartWorkerSetup("", "de", true); err == nil {
		t.Fatal("пустой токен принят")
	}
	if err := d.StartWorkerSetup("https://dash.cloudflare.com/profile/api-tokens", "de", true); err == nil || !strings.Contains(err.Error(), "не похоже на токен") {
		t.Fatalf("вместо токена вставлена ссылка: %v", err)
	}
	if st := d.Status().Cloudflare; st.Setup != nil || st.HasToken || st.Latest != tgproxy.WorkerVersionCurrent || st.TokenURL == "" {
		t.Fatalf("до настройки: %+v", st)
	}

	if err := d.StartWorkerSetup("  "+goodToken+"\n", "nl", true); err != nil {
		t.Fatal(err)
	}
	setup := waitSetup(t, d)
	if setup.Error != "" || !setup.Done || setup.Domain != "fi-tg.fi-abcd.workers.dev" {
		t.Fatalf("настройка: %+v", setup)
	}
	st := d.Status()
	if st.Telegram.Worker != "fi-tg.fi-abcd.workers.dev" || !st.Telegram.Enabled || st.Cloudflare.Region != "nl" || !st.Cloudflare.HasToken {
		t.Fatalf("после настройки: воркер %q, прокси %v, %+v", st.Telegram.Worker, st.Telegram.Enabled, st.Cloudflare)
	}
	if !strings.Contains(c.code, `const KEY = "0123456789abcdef0123456789abcdef";`) || !strings.Contains(c.metadata, `"region":"gcp:europe-west4"`) {
		t.Fatalf("в воркер ушёл код без ключа FI или без закрепления: %s", c.metadata)
	}
	// Токен лежит в отдельной папке, и в настройках, которые читают все пользователи, его нет.
	if got := d.loadToken(); got != goodToken {
		t.Fatalf("сохранён токен %q", got)
	}
	cfg, _ := os.ReadFile(configPath(d.dataDir))
	status, _ := json.Marshal(st)
	if strings.Contains(string(cfg), goodToken) || strings.Contains(string(status), goodToken) {
		t.Fatal("токен Cloudflare попал в настройки или в статус для окна")
	}

	// Итог окно показало — он убирается; токен можно стереть.
	d.ClearWorkerSetup()
	if d.Status().Cloudflare.Setup != nil {
		t.Fatal("итог настройки не убран")
	}
	if err := d.ForgetCloudflareToken(); err != nil || d.Status().Cloudflare.HasToken || d.loadToken() != "" {
		t.Fatalf("токен не стёрт: %v", err)
	}
	if err := d.UpdateWorker(); err == nil {
		t.Fatal("обновление без токена должно объяснить, что токена нет")
	}
}

// TestWorkerSetupErrors: отказы Cloudflare доходят до человека понятными словами, и токен,
// с которым ничего не вышло, не сохраняется.
func TestWorkerSetupErrors(t *testing.T) {
	c := &cloud{token: goodToken}
	d := cloudDaemon(t, c)
	wrong := strings.Repeat("x", 40)
	if err := d.StartWorkerSetup(wrong, "de", true); err != nil {
		t.Fatal(err)
	}
	if setup := waitSetup(t, d); !strings.Contains(setup.Error, "не принял токен") || setup.Done {
		t.Fatalf("чужой токен: %+v", setup)
	}
	if d.loadToken() != "" || d.cfg.Telegram.Worker != "" {
		t.Fatal("после отказа остался токен или адрес воркера")
	}

	c.apiErr = func(path string) (int, string) {
		if strings.HasSuffix(path, "/workers/subdomain") {
			return 403, `{"success":false,"errors":[{"code":10034,"message":"You need to verify your email address to use Workers."}]}`
		}
		return 0, ""
	}
	if err := d.StartWorkerSetup(goodToken, "de", true); err != nil {
		t.Fatal(err)
	}
	if setup := waitSetup(t, d); !strings.Contains(setup.Error, "подтвердить почту") {
		t.Fatalf("почта не подтверждена: %+v", setup)
	}

	// Воркер загружен и отвечает, но соединение через него не проходит: мастер говорит об этом
	// сразу, а не оставляет человека с воркером, через который ничего не открывается.
	c.apiErr = nil
	c.pumpErr = errors.New("сервер молчит")
	if err := d.StartWorkerSetup(goodToken, "de", true); err != nil {
		t.Fatal(err)
	}
	if setup := waitSetup(t, d); !strings.Contains(setup.Error, "соединение через него не прошло") || setup.Done {
		t.Fatalf("пересылка не работает: %+v", setup)
	}
	if d.cfg.Telegram.Worker != "" {
		t.Fatal("неработающий воркер вписан в настройки")
	}
}

// TestWorkerAutoUpdate: в воркере старый код, токен сохранён — FI обновляет воркер сам, один раз.
// Отозванный токен запоминается, и окно просит новый.
func TestWorkerAutoUpdate(t *testing.T) {
	c := &cloud{token: goodToken, version: tgproxy.WorkerVersionCurrent - 1}
	d := cloudDaemon(t, c)
	d.cfg.Telegram.Worker, d.cfg.Telegram.WorkerRegion = "fi-tg.fi-abcd.workers.dev", "us"

	d.maybeUpdateWorker(tgproxy.WorkerVersionCurrent - 1)
	if c.uploads != 0 {
		t.Fatal("без сохранённого токена воркер обновлять нечем")
	}
	if err := d.saveToken(goodToken); err != nil {
		t.Fatal(err)
	}
	d.maybeUpdateWorker(tgproxy.WorkerVersionCurrent) // код свежий — обновлять незачем
	if c.uploads != 0 {
		t.Fatal("свежий код обновлён зря")
	}
	d.maybeUpdateWorker(tgproxy.WorkerVersionCurrent - 1)
	setup := waitSetup(t, d)
	if !setup.Done || !setup.Update || c.uploads != 1 || !strings.Contains(c.metadata, `"region":"aws:us-east-1"`) {
		t.Fatalf("обновление: %+v, загрузок %d, %s", setup, c.uploads, c.metadata)
	}
	if v := d.Status().Cloudflare.Version; v != tgproxy.WorkerVersionCurrent {
		t.Fatalf("версия воркера после обновления: %d", v)
	}
	// Сразу повторять незачем: следующая попытка — через несколько часов.
	d.maybeUpdateWorker(tgproxy.WorkerVersionCurrent - 1)
	d.tasks.Wait()
	if c.uploads != 1 {
		t.Fatalf("воркер обновлён дважды подряд: %d", c.uploads)
	}

	// Токен отозвали: обновление не выходит, и FI перестаёт пробовать, пока не дадут новый.
	c.token = "другой"
	d.cfUpdateTried = time.Time{}
	d.maybeUpdateWorker(tgproxy.WorkerVersionCurrent - 1)
	if setup := waitSetup(t, d); setup.Done || d.Status().Cloudflare.TokenError == "" {
		t.Fatalf("отозванный токен: %+v, %q", setup, d.Status().Cloudflare.TokenError)
	}
	d.cfUpdateTried = time.Time{}
	calls := len(c.calls)
	d.maybeUpdateWorker(tgproxy.WorkerVersionCurrent - 1)
	d.tasks.Wait()
	if len(c.calls) != calls {
		t.Fatal("с отозванным токеном FI снова пошёл в Cloudflare")
	}
}

// TestUsageSurvivesRestart: расход воркера сохраняется между запусками службы.
func TestUsageSurvivesRestart(t *testing.T) {
	c := &cloud{token: goodToken}
	d := cloudDaemon(t, c)
	day := time.Now().UTC().Format("2006-01-02")
	d.usage = tgproxy.WorkerUsage{Day: day, Requests: 4321}
	d.saveUsage()

	d2 := &Daemon{dataDir: d.dataDir, log: d.log, cfg: config.Default()}
	d2.loadUsage()
	st := d2.cloudflareStatusLocked()
	if st.Requests != 4321 || st.Limit != tgproxy.WorkerDailyLimit || st.Saving {
		t.Fatalf("расход после перезапуска: %+v", st)
	}
	d2.usage.Requests = 85000
	if st := d2.cloudflareStatusLocked(); !st.Saving {
		t.Fatalf("85%% лимита — пора беречь запросы: %+v", st)
	}
}

// TestSitesPACOffDuringVPN: пока интернет идёт через VPN, сценарий прокси для браузеров не
// нужен — окно его убирает и возвращает, когда VPN выключат. Сама функция остаётся включённой.
func TestSitesPACOffDuringVPN(t *testing.T) {
	c := &cloud{token: goodToken}
	d := cloudDaemon(t, c)
	d.cfg.Telegram.Enabled, d.cfg.Telegram.Worker = true, "fi.example.workers.dev"
	d.cfg.Telegram.MiniApps = true
	d.cfg.YouTubeViaWorker, d.cfg.ViaWorkerBrowsers = true, true
	d.startTelegram()
	for deadline := time.Now().Add(3 * time.Second); !d.web.Listening(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("локальный прокси не запустился")
		}
	}
	d.mu.Lock()
	direct, vpn := d.telegramStatusLocked(""), d.telegramStatusLocked("happ-xray")
	d.mu.Unlock()
	if direct.SitesPAC == "" {
		t.Fatal("без VPN сценарий прокси должен стоять")
	}
	if vpn.SitesPAC != "" {
		t.Fatalf("через VPN сценарий не нужен: %q", vpn.SitesPAC)
	}
	if !d.cfg.ViaWorkerBrowsers || !d.cfg.YouTubeViaWorker {
		t.Fatal("функции выключились из-за VPN")
	}
}
