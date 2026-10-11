package tgproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Мини-приложения Telegram Desktop открываются во встроенном браузере (WebView2), который
// ходит в сеть мимо прокси Telegram. Почти каждое мини-приложение грузит скрипт с telegram.org,
// а у многих провайдеров адреса сайтов Telegram закрыты по IP, и приложение висит на загрузке.
// FI даёт окнам мини-приложений файл PAC: сайты Telegram идут через этот локальный прокси,
// остальное — напрямую. Прокси сначала пробует сайт напрямую, а если адрес закрыт — открывает
// TCP через Cloudflare Worker пользователя. Шифрование TLS при этом остаётся сквозным:
// прокси и воркер видят только имя сайта.

// webDomains — сайты Telegram, которые нужны мини-приложениям. Воркер пускает только к ним.
var webDomains = []string{
	"telegram.org", "t.me", "telegram.me", "telesco.pe", "cdn-telegram.org",
	"telegra.ph", "graph.org", "tdesktop.com", "fragment.com",
}

const (
	webDirectTimeout = 2 * time.Second
	webDirectPause   = 5 * time.Minute // после неудачи напрямую сайт какое-то время идёт сразу через воркер
	// PACPath — путь файла PAC на локальном прокси.
	PACPath = "/telegram.pac"
)

// IsTelegramWeb — сайт принадлежит Telegram и нужен мини-приложениям.
func IsTelegramWeb(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range webDomains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// PAC — файл автонастройки прокси для окон мини-приложений: HTTPS к сайтам Telegram — через
// FI, а если FI не запущен — напрямую; всё остальное — напрямую.
func PAC(proxyAddr string) string {
	var b strings.Builder
	b.WriteString("function FindProxyForURL(url, host) {\n")
	b.WriteString("  if (url.substring(0, 6) != \"https:\") return \"DIRECT\";\n")
	b.WriteString("  host = host.toLowerCase();\n")
	for _, d := range webDomains {
		fmt.Fprintf(&b, "  if (host == %q || dnsDomainIs(host, %q)) return \"PROXY %s; DIRECT\";\n", d, "."+d, proxyAddr)
	}
	b.WriteString("  return \"DIRECT\";\n}\n")
	return b.String()
}

// WebProxy — локальный HTTP-прокси для мини-приложений.
type WebProxy struct {
	Addr   string
	Server *Server // воркер и поиск адресов берутся у прокси Telegram

	// dialDirect и dialWorker подменяются в тестах.
	dialDirect func(ctx context.Context, addr string) (net.Conn, error)
	dialWorker func(ctx context.Context, host string, port int) (net.Conn, error)

	mu        sync.Mutex
	blocked   map[string]time.Time // сайт не открылся напрямую: до этого времени — сразу воркер
	listening bool
}

// Listening сообщает, принимает ли прокси соединения.
func (p *WebProxy) Listening() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.listening
}

// ListenAndServe принимает соединения, пока не отменён ctx.
func (p *WebProxy) ListenAndServe(ctx context.Context) error {
	l, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return fmt.Errorf("адрес %s занят: %w", p.Addr, err)
	}
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	stop := context.AfterFunc(ctx, func() { srv.Close() })
	defer stop()
	p.mu.Lock()
	p.listening = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.listening = false
		p.mu.Unlock()
	}()
	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (p *WebProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == PACPath && r.URL.Host == "":
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		io.WriteString(w, PAC(p.Addr))
	case r.Method == http.MethodConnect:
		p.connect(w, r)
	default:
		http.Error(w, "FI: только CONNECT к сайтам Telegram", http.StatusMethodNotAllowed)
	}
}

func (p *WebProxy) connect(w http.ResponseWriter, r *http.Request) {
	host, portText, err := net.SplitHostPort(r.Host)
	port, perr := strconv.Atoi(portText)
	if err != nil || perr != nil || port != 443 || !IsTelegramWeb(host) {
		// Чужие сайты сюда не попадают — их PAC отправляет напрямую. Открытым прокси быть незачем.
		http.Error(w, "FI: только сайты Telegram", http.StatusForbidden)
		return
	}
	upstream, err := p.dial(r.Context(), host, port)
	if err != nil {
		p.logf("мини-приложения: %s недоступен: %v", host, err)
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()

	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "нет перехвата соединения", http.StatusInternalServerError)
		return
	}
	client, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	relay(client, buf.Reader, upstream)
}

// dial открывает TCP до сайта Telegram: напрямую, а если адрес закрыт — через воркер.
func (p *WebProxy) dial(ctx context.Context, host string, port int) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var errs []error
	if !p.directBlocked(host) {
		dctx, cancel := context.WithTimeout(ctx, webDirectTimeout)
		conn, err := p.direct(dctx, addr)
		cancel()
		if err == nil {
			return conn, nil
		}
		errs = append(errs, fmt.Errorf("напрямую: %w", err))
		p.markBlocked(host)
	}
	if p.Server == nil || p.Server.Worker == "" {
		return nil, errors.Join(append(errs, errors.New("адрес закрыт провайдером, а свой Cloudflare Worker не задан"))...)
	}
	conn, err := p.worker(ctx, host, port)
	if err != nil {
		return nil, errors.Join(append(errs, err)...)
	}
	return conn, nil
}

func (p *WebProxy) direct(ctx context.Context, addr string) (net.Conn, error) {
	if p.dialDirect != nil {
		return p.dialDirect(ctx, addr)
	}
	var d net.Dialer
	return d.DialContext(ctx, "tcp4", addr)
}

func (p *WebProxy) worker(ctx context.Context, host string, port int) (net.Conn, error) {
	if p.dialWorker != nil {
		return p.dialWorker(ctx, host, port)
	}
	return p.Server.dialWorkerTCP(ctx, host, port)
}

// NetworkChanged забывает, какие сайты не открывались напрямую: в новой сети это могло измениться.
func (p *WebProxy) NetworkChanged() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.blocked = nil
}

func (p *WebProxy) directBlocked(host string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return time.Now().Before(p.blocked[host])
}

func (p *WebProxy) markBlocked(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked == nil || len(p.blocked) > 100 {
		p.blocked = map[string]time.Time{}
	}
	p.blocked[host] = time.Now().Add(webDirectPause)
}

func (p *WebProxy) logf(format string, args ...any) {
	if p.Server != nil && p.Server.Log != nil {
		p.Server.Log.Warn(fmt.Sprintf(format, args...))
	}
}

// relay перекладывает байты в обе стороны, пока одна из сторон не закроется.
func relay(client net.Conn, clientBuf *bufio.Reader, upstream net.Conn) {
	done := make(chan struct{}, 2)
	go func() {
		io.Copy(upstream, clientBuf) // в буфере могут быть байты, пришедшие вместе с CONNECT
		done <- struct{}{}
	}()
	go func() {
		io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
	client.Close()
	upstream.Close()
	<-done
}

// dialWorkerTCP открывает через Cloudflare Worker сырое TCP-соединение с сайтом Telegram.
func (s *Server) dialWorkerTCP(ctx context.Context, host string, port int) (net.Conn, error) {
	path := fmt.Sprintf("/?host=%s&port=%d", host, port)
	var errs []error
	for _, ip := range s.resolve(ctx, s.Worker, 0) {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		ws, err := dialWS(dialCtx, net.JoinHostPort(ip, workerPort), s.Worker, path)
		cancel()
		if err == nil {
			ws.raw = true
			return &wsStream{ws: ws}, nil
		}
		var se *statusError
		switch {
		case errors.As(err, &se) && se.code == http.StatusBadRequest:
			return nil, fmt.Errorf("воркер %s не умеет открывать сайты Telegram — обновите его код из FI", s.Worker)
		case errors.As(err, &se) && se.code == http.StatusBadGateway:
			return nil, fmt.Errorf("воркер %s не смог подключиться к %s", s.Worker, host)
		}
		errs = append(errs, fmt.Errorf("воркер %s через %s: %w", s.Worker, ip, err))
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("воркер %s: адрес не найден", s.Worker)
	}
	return nil, errors.Join(errs...)
}

// WorkerVersionWeb — версия кода воркера, с которой работают мини-приложения: первая открывала
// сайты Telegram по имени, а так из воркера они не открываются; во второй не шли данные.
const WorkerVersionWeb = 3

var workerVersionRe = regexp.MustCompile(`версия (\d+)`)

// WorkerVersion узнаёт версию кода воркера по его ответу на обычный запрос:
// «FI worker: работает (версия 2)»; без номера — первая версия.
func WorkerVersion(ctx context.Context, client *http.Client, worker string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+worker+"/", nil)
	if err != nil {
		return 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "FI worker") {
		return 0, fmt.Errorf("по адресу %s не воркер FI (HTTP %d)", worker, resp.StatusCode)
	}
	if m := workerVersionRe.FindStringSubmatch(string(body)); m != nil {
		return strconv.Atoi(m[1])
	}
	return 1, nil
}

// wsStream — соединение WebSocket с воркером как обычный поток байтов.
type wsStream struct {
	ws   *wsConn
	buf  []byte
	once sync.Once
}

func (s *wsStream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		data, err := s.ws.recv()
		s.buf = data
		if err != nil && len(s.buf) == 0 {
			return 0, err
		}
	}
	n := copy(p, s.buf)
	s.buf = s.buf[n:]
	return n, nil
}

func (s *wsStream) Write(p []byte) (int, error) {
	if err := s.ws.send(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *wsStream) Close() error {
	s.once.Do(s.ws.close)
	return nil
}

func (s *wsStream) LocalAddr() net.Addr                { return s.ws.conn.LocalAddr() }
func (s *wsStream) RemoteAddr() net.Addr               { return s.ws.conn.RemoteAddr() }
func (s *wsStream) SetDeadline(t time.Time) error      { return s.ws.conn.SetDeadline(t) }
func (s *wsStream) SetReadDeadline(t time.Time) error  { return s.ws.conn.SetReadDeadline(t) }
func (s *wsStream) SetWriteDeadline(t time.Time) error { return s.ws.conn.SetWriteDeadline(t) }
