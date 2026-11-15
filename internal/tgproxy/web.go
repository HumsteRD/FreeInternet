package tgproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	// PACPath — PAC для окон мини-приложений Telegram, SitesPACPath — для браузеров
	// (системный, с прокси, который был задан до FI, — например VPN-клиента).
	PACPath      = "/telegram.pac"
	SitesPACPath = "/sites.pac"
)

// IsTelegramWeb — сайт принадлежит Telegram и нужен мини-приложениям.
func IsTelegramWeb(host string) bool { return inDomains(host, webDomains) }

func inDomains(host string, domains []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range domains {
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// fallbackPattern — каким может быть прежний прокси в PAC: адрес:порт, без кавычек и пробелов.
var fallbackPattern = regexp.MustCompile(`^[A-Za-z0-9.\-]{1,100}:[0-9]{1,5}$`)

// PAC — файл автонастройки прокси: HTTPS к сайтам Telegram и к сайтам «через воркер» — через FI,
// а если FI не запущен — как обычно; остальное — через прежний прокси (fallback) или напрямую.
func PAC(proxyAddr string, viaWorker []string, fallback string) string {
	other := "DIRECT"
	if fallbackPattern.MatchString(fallback) {
		other = "PROXY " + fallback + "; DIRECT"
	}
	var b strings.Builder
	b.WriteString("function FindProxyForURL(url, host) {\n")
	fmt.Fprintf(&b, "  if (url.substring(0, 6) != \"https:\") return %q;\n", other)
	b.WriteString("  host = host.toLowerCase();\n")
	for _, d := range append(slices.Clone(webDomains), viaWorker...) {
		if !hostPattern.MatchString(d) {
			continue
		}
		fmt.Fprintf(&b, "  if (host == %q || dnsDomainIs(host, %q)) return \"PROXY %s; %s\";\n", d, "."+d, proxyAddr, other)
	}
	fmt.Fprintf(&b, "  return %q;\n}\n", other)
	return b.String()
}

var hostPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}$`)

// WebProxy — локальный HTTP-прокси для мини-приложений Telegram и сайтов «через воркер».
type WebProxy struct {
	Addr   string
	Server *Server // воркер и поиск адресов берутся у прокси Telegram
	// ViaWorker — сайты, которые всегда идут через воркер: их закрыли для России сами сервисы,
	// и нужен не российский IP. Поддомены входят сами.
	ViaWorker func() []string
	// Fallback — сайты, которые идут напрямую, а через воркер — только когда сервер молчит
	// в ответ на приветствие TLS (провайдер закрыл его адрес): видеосерверы YouTube.
	Fallback func() []string

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

func (p *WebProxy) viaWorker() []string {
	if p.ViaWorker == nil {
		return nil
	}
	return p.ViaWorker()
}

func (p *WebProxy) fallback() []string {
	if p.Fallback == nil {
		return nil
	}
	return p.Fallback()
}

// pacSites — всё, что PAC отправляет через FI, кроме сайтов Telegram.
func (p *WebProxy) pacSites() []string { return append(p.viaWorker(), p.fallback()...) }

func (p *WebProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	isGet := r.Method == http.MethodGet && r.URL.Host == ""
	switch {
	case isGet && r.URL.Path == PACPath:
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		io.WriteString(w, PAC(p.Addr, p.viaWorker(), ""))
	case isGet && r.URL.Path == SitesPACPath:
		w.Header().Set("Content-Type", "application/x-ns-proxy-autoconfig")
		io.WriteString(w, PAC(p.Addr, p.pacSites(), r.URL.Query().Get("fallback")))
	case r.Method == http.MethodConnect:
		p.connect(w, r)
	default:
		http.Error(w, "FI: только CONNECT к сайтам Telegram и сайтам через воркер", http.StatusMethodNotAllowed)
	}
}

func (p *WebProxy) connect(w http.ResponseWriter, r *http.Request) {
	host, portText, err := net.SplitHostPort(r.Host)
	port, perr := strconv.Atoi(portText)
	geo := err == nil && inDomains(host, p.viaWorker()) && !IsTelegramWeb(host)
	silent := err == nil && !geo && inDomains(host, p.fallback())
	if err != nil || perr != nil || port != 443 || !(IsTelegramWeb(host) || geo || silent) {
		// Чужие сайты сюда не попадают — их PAC отправляет мимо FI. Открытым прокси быть незачем.
		http.Error(w, "FI: только сайты Telegram и сайты через воркер", http.StatusForbidden)
		return
	}
	if silent {
		p.connectFallback(w, r, host, port)
		return
	}
	var upstream net.Conn
	switch {
	case geo && p.cantUseWorker(r.Context(), host):
		// Сайт стоит за Cloudflare, и воркер к нему не подключится: напрямую он хотя бы откроется
		// так же, как без FI. Окно об этом говорит (Status.ViaWorkerBlocked).
		dctx, cancel := context.WithTimeout(r.Context(), dialTimeout)
		upstream, err = p.direct(dctx, r.Host)
		cancel()
	case geo:
		// Сайт закрыт для России самим сервисом: напрямую он ответит «недоступно в вашей стране».
		upstream, err = p.workerOnly(r.Context(), host, port)
	default:
		upstream, err = p.dial(r.Context(), host, port)
	}
	if err != nil {
		if r.Context().Err() == nil { // браузер сам закрыл вкладку — это не сбой
			p.logf("%s недоступен: %v", host, err)
		}
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

// helloTimeout — сколько ждать от браузера приветствия TLS после CONNECT.
const helloTimeout = 10 * time.Second

// silentTimeout — сколько ждать ответа сервера на приветствие TLS напрямую. Провайдер может
// пускать TCP к адресу, но глотать TLS: тогда соединение есть, а ответа нет никогда.
var silentTimeout = 3 * time.Second

// connectFallback — туннель к сайту из Fallback: напрямую, а если сервер молчит в ответ на
// приветствие TLS — то же приветствие уходит через воркер. Браузер видит только задержку.
func (p *WebProxy) connectFallback(w http.ResponseWriter, r *http.Request, host string, port int) {
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
	client.SetReadDeadline(time.Now().Add(helloTimeout))
	hello, err := readTLSRecord(buf.Reader)
	client.SetReadDeadline(time.Time{})
	if err != nil {
		return
	}
	upstream, answer, err := p.dialAnswering(r.Context(), host, port, hello)
	if err != nil {
		if r.Context().Err() == nil { // браузер сам закрыл вкладку — это не сбой
			p.logf("%s недоступен: %v", host, err)
		}
		return
	}
	defer upstream.Close()
	if _, err := client.Write(answer); err != nil {
		return
	}
	relay(client, buf.Reader, upstream)
}

// readTLSRecord читает первую запись TLS (приветствие клиента) целиком: браузеры с длинным
// приветствием (ключи ML-KEM) отправляют его двумя пакетами, и сервер ответит только на всё.
func readTLSRecord(r *bufio.Reader) ([]byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	if hdr[0] != 0x16 { // не TLS — отдаём как есть, сервер ответит или нет
		return hdr, nil
	}
	n := int(hdr[3])<<8 | int(hdr[4])
	if n > 1<<14+2048 {
		return nil, fmt.Errorf("запись TLS длиной %d", n)
	}
	rec := make([]byte, 5+n)
	copy(rec, hdr)
	if _, err := io.ReadFull(r, rec[5:]); err != nil {
		return nil, err
	}
	return rec, nil
}

// hedgeDelay — сколько ждать ответа сервера напрямую, прежде чем отправить то же приветствие
// ещё и через воркер. Сервер Google отвечает на приветствие за один обмен пакетами — десятки
// миллисекунд; молчит дольше — скорее всего, провайдер его закрыл. Раньше браузер ждал все три
// секунды silentTimeout на каждом новом видеосервере, и ролик заметно думал перед стартом.
var hedgeDelay = 300 * time.Millisecond

// workerAnswerWait — сколько ждать первого ответа сервера через воркер.
const workerAnswerWait = 10 * time.Second

// videoNode — видеосервер YouTube: rr4---sn-ixh7yn7e.googlevideo.com.
var videoNode = regexp.MustCompile(`^rr\d+---(sn-[a-z0-9-]+\.googlevideo\.com)$`)

// blockKey — под каким именем помнить, что сервер закрыт провайдером. Видеосерверы одного узла
// YouTube (rr1---sn-xxx … rr6---sn-xxx) стоят в одной подсети и закрыты разом: узнав про один,
// остальные FI сразу ведёт через воркер.
func blockKey(host string) string {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if m := videoNode.FindStringSubmatch(host); m != nil {
		return m[1]
	}
	return host
}

// tunnelTry — чем закончилась попытка открыть туннель одним из путей.
type tunnelTry struct {
	conn   net.Conn
	answer []byte // первые байты ответа сервера, если путь их дожидался
	err    error
	direct bool
}

// race открывает туннель двумя путями внахлёст: сначала напрямую, а если прямой путь не ответил
// за hedgeDelay или упал — ещё и через воркер; берётся путь, ответивший первым. Сервер, который
// помнится закрытым (key), идёт сразу через воркер, и только если воркер подвёл — напрямую.
// Без воркера остаётся один прямой путь.
func (p *WebProxy) race(ctx context.Context, key string, direct, worker func(context.Context) tunnelTry) (tunnelTry, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel() // опоздавший путь не доделывает подключение; готовым соединениям отмена не мешает
	hasWorker := p.hasWorker()
	results := make(chan tunnelTry, 2)
	pending, directStarted, workerStarted := 0, false, false
	start := func(isDirect bool) {
		pending++
		path := worker
		if isDirect {
			path, directStarted = direct, true
		} else {
			workerStarted = true
		}
		go func() {
			r := path(ctx)
			r.direct = isDirect
			results <- r
		}()
	}
	var hedge <-chan time.Time
	if hasWorker && p.directBlocked(key) {
		start(false)
	} else {
		start(true)
		if hasWorker {
			timer := time.NewTimer(hedgeDelay)
			defer timer.Stop()
			hedge = timer.C
		}
	}
	var errs []error
	for pending > 0 {
		select {
		case <-hedge:
			hedge = nil
			start(false)
		case r := <-results:
			pending--
			if r.err == nil {
				if pending > 0 {
					if !r.direct {
						// Воркер успел раньше, а напрямую сервер всё молчит: соседние соединения
						// (браузер открывает их пачкой) идут сразу через воркер. Ответит — reap
						// снимет отметку.
						p.markBlocked(key)
					}
					go p.reap(results, pending, key) // опоздавший путь уже не нужен
				}
				return r, nil
			}
			errs = append(errs, r.err)
			switch {
			case r.direct:
				// Не нашёлся адрес — это сбой DNS, а не закрытый сервер: запоминать нечего.
				if !errors.Is(r.err, errNoAddress) {
					p.markBlocked(key)
				}
				if hasWorker && !workerStarted {
					hedge = nil
					start(false) // напрямую не вышло — ждать незачем
				}
			case !directStarted:
				start(true) // воркер подвёл — вдруг сервер уже отвечает сам
			}
		}
	}
	return tunnelTry{}, errors.Join(errs...)
}

// errNoAddress — адрес сайта не нашёлся ни в системном DNS, ни через DoH.
var errNoAddress = errors.New("адрес не найден")

// dialAnswering отправляет приветствие напрямую и ждёт первых байт ответа. Если сервер молчит
// дольше hedgeDelay, то же приветствие уходит ещё и через воркер, и берётся путь, ответивший
// первым: приветствие TLS можно отправить дважды, сеанс продолжится с тем сервером, чей ответ
// получил браузер. answer — уже полученные байты ответа.
func (p *WebProxy) dialAnswering(ctx context.Context, host string, port int, hello []byte) (net.Conn, []byte, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	r, err := p.race(ctx, blockKey(host),
		func(ctx context.Context) tunnelTry {
			dctx, cancel := context.WithTimeout(ctx, webDirectTimeout)
			conn, err := p.direct(dctx, addr)
			cancel()
			if err != nil {
				return tunnelTry{err: fmt.Errorf("напрямую: %w", err)}
			}
			return answering(conn, hello, silentTimeout, "напрямую")
		},
		func(ctx context.Context) tunnelTry {
			conn, err := p.workerOnly(ctx, host, port)
			if err != nil {
				return tunnelTry{err: err}
			}
			return answering(conn, hello, workerAnswerWait, "через воркер")
		})
	if err != nil {
		if !p.hasWorker() {
			err = errors.Join(err, errNoWorker)
		}
		return nil, nil, err
	}
	return r.conn, r.answer, nil
}

// answering отправляет приветствие и ждёт первых байт ответа: путь годится, только если сервер ответил.
func answering(conn net.Conn, hello []byte, wait time.Duration, path string) tunnelTry {
	answer, err := firstAnswer(conn, hello, wait)
	if err != nil {
		conn.Close()
		return tunnelTry{err: fmt.Errorf("%s: %w", path, err)}
	}
	return tunnelTry{conn: conn, answer: answer}
}

// reap дожидается опоздавших путей и закрывает их соединения. Если напрямую сервер так и не
// ответил, он остаётся закрытым: следующие соединения идут сразу через воркер. Ответил, хоть
// и медленнее воркера, — отметка снимается.
func (p *WebProxy) reap(results <-chan tunnelTry, n int, key string) {
	for range n {
		r := <-results
		if r.conn != nil {
			r.conn.Close()
		}
		switch {
		case !r.direct:
		case r.err != nil:
			p.markBlocked(key)
		default:
			p.unblock(key)
		}
	}
}

var errNoWorker = errors.New("сайты через воркер работают только со своим Cloudflare Worker")

func (p *WebProxy) hasWorker() bool { return p.Server != nil && p.Server.Worker != "" }

// firstAnswer отправляет данные и ждёт первых байт ответа не дольше wait.
func firstAnswer(conn net.Conn, data []byte, wait time.Duration) ([]byte, error) {
	conn.SetDeadline(time.Now().Add(wait))
	defer conn.SetDeadline(time.Time{})
	if _, err := conn.Write(data); err != nil {
		return nil, err
	}
	buf := make([]byte, 32<<10)
	n, err := conn.Read(buf)
	if n > 0 {
		return buf[:n], nil
	}
	if err == nil {
		err = io.ErrNoProgress
	}
	return nil, fmt.Errorf("сервер молчит: %w", err)
}

// WorkerTLS проверяет, что сайт открывается через воркер: рукопожатие TLS до него.
func (s *Server) WorkerTLS(ctx context.Context, host string) error {
	return s.workerTLS(ctx, host, s.plainPump.Load())
}

// VerifyPump проверяет пересылку данных сайта через воркер настоящим рукопожатием TLS. Восьмая
// версия воркера читает записи TLS целиком — это быстрее, но держится на возможности Cloudflare,
// которой в старом коде не было. Если так соединение не проходит, а по-простому проходит, FI
// дальше просит воркер пересылать по-простому: сайты работают, как с прежним кодом, вместо того
// чтобы не работать вовсе. Ошибка — не прошло никак: дело не в способе пересылки.
func (s *Server) VerifyPump(ctx context.Context, host string) error {
	return s.verifyPump(func(plain bool) error {
		tctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		return s.workerTLS(tctx, host, plain)
	})
}

func (s *Server) verifyPump(try func(plain bool) error) error {
	fast := try(false)
	if fast == nil {
		s.plainPump.Store(false)
		return nil
	}
	if err := try(true); err != nil {
		return errors.Join(fast, err)
	}
	s.plainPump.Store(true)
	if s.Log != nil {
		s.Log.Warn("быстрая пересылка в воркере не сработала — FI просит воркер пересылать данные по-простому, как прежний код", "worker", s.Worker, "err", fast)
	}
	return nil
}

// PlainPump — воркер пересылает данные сайтов по-простому: быстрая пересылка не прошла проверку.
func (s *Server) PlainPump() bool { return s.plainPump.Load() }

func (s *Server) workerTLS(ctx context.Context, host string, plain bool) error {
	conn, err := s.dialWorkerTCPWith(ctx, host, 443, plain)
	if err != nil {
		return err
	}
	defer conn.Close()
	tc := tls.Client(conn, &tls.Config{ServerName: host})
	if dl, ok := ctx.Deadline(); ok {
		tc.SetDeadline(dl)
	}
	return tc.HandshakeContext(ctx)
}

// dial открывает TCP до сайта Telegram: напрямую, а если адрес закрыт — через воркер. Пути идут
// внахлёст, как у видеосерверов: закрытый адрес не отвечает на подключение секундами, и
// мини-приложение не должно их ждать.
func (p *WebProxy) dial(ctx context.Context, host string, port int) (net.Conn, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	r, err := p.race(ctx, host,
		func(ctx context.Context) tunnelTry {
			dctx, cancel := context.WithTimeout(ctx, webDirectTimeout)
			defer cancel()
			conn, err := p.direct(dctx, addr)
			if err != nil {
				return tunnelTry{err: fmt.Errorf("напрямую: %w", err)}
			}
			return tunnelTry{conn: conn}
		},
		func(ctx context.Context) tunnelTry {
			conn, err := p.worker(ctx, host, port)
			if err != nil {
				return tunnelTry{err: err}
			}
			return tunnelTry{conn: conn}
		})
	if err != nil {
		if !p.hasWorker() {
			err = errors.Join(err, errors.New("адрес закрыт провайдером, а свой Cloudflare Worker не задан"))
		}
		return nil, err
	}
	return r.conn, nil
}

// cantUseWorker — воркер до сайта не дотянется (сайт стоит за Cloudflare).
func (p *WebProxy) cantUseWorker(ctx context.Context, host string) bool {
	if p.Server == nil || p.dialWorker != nil { // в тестах воркер подменён и открывает всё
		return false
	}
	return p.Server.BehindCloudflare(ctx, host)
}

func (p *WebProxy) workerOnly(ctx context.Context, host string, port int) (net.Conn, error) {
	if !p.hasWorker() {
		return nil, errNoWorker
	}
	return p.worker(ctx, host, port)
}

// direct подключается к сайту напрямую. Адрес ищет прокси Telegram: с кэшем и через DNS поверх
// HTTPS, когда системный DNS сайта не знает, — провайдер подменяет ответы обычного DNS, и без
// этого каждое соединение сразу уходило бы через воркер.
func (p *WebProxy) direct(ctx context.Context, addr string) (net.Conn, error) {
	if p.dialDirect != nil {
		return p.dialDirect(ctx, addr)
	}
	var d net.Dialer
	host, port, err := net.SplitHostPort(addr)
	if err != nil || p.Server == nil || net.ParseIP(host) != nil {
		return d.DialContext(ctx, "tcp4", addr)
	}
	ips := p.Server.resolve(ctx, host, 0)
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s: %w", host, errNoAddress)
	}
	var conn net.Conn
	for _, ip := range ips[:min(len(ips), 2)] {
		if conn, err = d.DialContext(ctx, "tcp4", net.JoinHostPort(ip, port)); err == nil {
			return conn, nil
		}
	}
	return nil, err
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

func (p *WebProxy) unblock(host string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.blocked, host)
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

// dialWorkerTCP открывает через Cloudflare Worker сырое TCP-соединение с сайтом.
func (s *Server) dialWorkerTCP(ctx context.Context, host string, port int) (net.Conn, error) {
	return s.dialWorkerTCPWith(ctx, host, port, s.plainPump.Load())
}

// dialWorkerTCPWith — то же, но способ пересылки задан явно: plain — по мере прихода.
func (s *Server) dialWorkerTCPWith(ctx context.Context, host string, port int, plain bool) (net.Conn, error) {
	if s.workerLimited() {
		return nil, s.limitError()
	}
	if s.workerCantReach(host) {
		return nil, fmt.Errorf("%s: %w", host, ErrBehindCloudflare)
	}
	path := fmt.Sprintf("/?host=%s&port=%d", url.QueryEscape(host), port)
	if s.WorkerKey != "" {
		path += "&key=" + url.QueryEscape(s.WorkerKey)
	}
	if plain {
		path += "&p=0"
	}
	var errs []error
	for _, ip := range s.cfAddrs(ctx, s.Worker, s.edges(s.Worker)) {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		ws, err := dialWS(dialCtx, net.JoinHostPort(ip, workerPort), s.Worker, path)
		cancel()
		if s.noteWorkerDial(err) {
			return nil, s.limitError() // лимит один на все адреса: другой адрес ответит тем же
		}
		if err == nil {
			ws.raw, ws.onCut = true, s.noteCut
			stream := newWSStream(ws)
			stream.onRefused = func() { s.refused(host) }
			return stream, nil
		}
		// Ответ самого воркера — приговор: через другой адрес Cloudflare он ответит то же.
		// Отказ самого Cloudflare (ошибка 1034 на адресе, который не обслуживает воркер) — нет:
		// запрос до воркера не дошёл, и стоит пойти через следующий адрес.
		var se *statusError
		if errors.As(err, &se) && !se.fromEdge() {
			switch se.code {
			case http.StatusBadRequest:
				return nil, fmt.Errorf("воркер %s не умеет открывать сайты Telegram — обновите его код из FI", s.Worker)
			case http.StatusBadGateway:
				return nil, fmt.Errorf("воркер %s не смог подключиться к %s", s.Worker, host)
			case http.StatusForbidden:
				// Воркер до версии 8 пускал только к порту 443 и только по имени сайта: программам,
				// запущенным через Cloudflare, этого мало.
				if strings.Contains(se.body, "порт 443") || strings.Contains(se.body, "имя сайта") {
					return nil, fmt.Errorf("воркер %s пускает только к сайтам на порту 443 — обновите его код, чтобы он открыл %s:%d", s.Worker, host, port)
				}
				return nil, fmt.Errorf("воркер %s не пускает к %s: обновите его код в разделе Cloudflare", s.Worker, host)
			}
		}
		s.dropEdge(ip, err)
		errs = append(errs, fmt.Errorf("воркер %s через %s: %w", s.Worker, ip, err))
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("воркер %s: адрес не найден", s.Worker)
	}
	return nil, errors.Join(errs...)
}

// WorkerVersionWeb — версия кода воркера, которая правильно пересылает данные: версии 1–4
// неверно разбирали двоичные сообщения WebSocket, и до Telegram доходили не те байты.
const WorkerVersionWeb = 5

// WorkerVersionFast — версия кода воркера, которая пересылает данные крупными блоками. Прежние
// гоняли их кусочками по 4 КБ и на быстрой загрузке тратили процессорное время бесплатного
// тарифа за доли секунды: Cloudflare обрывал соединение без объяснений — то на первых сотнях
// килобайт, то через сотню мегабайт (замер 2026-11-11, одинаково через провайдера и через VPN).
// Видео и файлы через воркер поэтому шли рывками.
const WorkerVersionFast = 7

// WorkerVersionCurrent — версия кода воркера, вшитого в эту сборку FI. Восьмая читает запись
// TLS целиком одним вызовом (вчетверо меньше процессорного времени на мегабайт, чем кусками по
// 4 КБ) и с ключом FI открывает любой порт — это нужно программам, запущенным через Cloudflare.
const WorkerVersionCurrent = 8

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

// WorkerKeyOK проверяет, что в воркере стоит код с этим ключом FI.
func WorkerKeyOK(ctx context.Context, client *http.Client, worker, key string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+worker+"/?keycheck=1&key="+url.QueryEscape(key), nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// wsStream — соединение WebSocket с воркером как обычный поток байтов.
type wsStream struct {
	ws   *wsConn
	buf  []byte
	once sync.Once
	done chan struct{}
	// waiting — с какого времени чтение ждёт данных от воркера; 0 — не ждёт (занято передачей
	// браузеру). dead — сторож закрыл соединение, потому что оно замерло.
	waiting atomic.Int64
	dead    atomic.Bool
	// onRefused — Cloudflare не дал воркеру подключиться к сайту: прокси это запоминает.
	onRefused func()
}

// wsPingEvery — сколько соединение с воркером может молчать, прежде чем сторож пошлёт ping,
// и столько же он ждёт ответа: замершее соединение закрывается через два таких срока.
var wsPingEvery = 3 * time.Second

func newWSStream(ws *wsConn) *wsStream {
	s := &wsStream{ws: ws, done: make(chan struct{})}
	ws.lastRecv.Store(time.Now().UnixNano())
	go s.watch()
	return s
}

// watch — сторож соединения с воркером. Провайдер умеет «замораживать» соединение: оно открыто,
// а пакеты в обе стороны пропадают, и браузер ждал бы данных, пока система секунд через двадцать
// не признает соединение мёртвым. Когда чтение ждёт данных дольше wsPingEvery, сторож шлёт ping:
// Cloudflare отвечает на него сам, за десятую долю секунды. Нет за следующий такой срок ни
// ответа, ни данных — соединение закрывается, и браузер сразу повторяет запрос по новому.
func (s *wsStream) watch() {
	t := time.NewTicker(wsPingEvery / 4)
	defer t.Stop()
	var pinged time.Time
	for {
		select {
		case <-s.done:
			return
		case now := <-t.C:
			since := s.waiting.Load()
			last := time.Unix(0, s.ws.lastRecv.Load())
			switch {
			case since == 0 || !last.Before(pinged):
				// Чтение занято (данные есть, браузер их ещё не забрал) или после ping что-то
				// пришло — соединение живо.
				pinged = time.Time{}
			case !pinged.IsZero():
				if now.Sub(pinged) >= wsPingEvery && time.Unix(0, since).Before(pinged) {
					s.dead.Store(true)
					s.ws.conn.Close()
					return
				}
				continue
			}
			if since != 0 && now.Sub(last) >= wsPingEvery {
				pinged = now
				if s.ws.writeFrame(opPing, nil) != nil {
					return
				}
			}
		}
	}
}

func (s *wsStream) Read(p []byte) (int, error) {
	for len(s.buf) == 0 {
		s.waiting.Store(time.Now().UnixNano())
		data, err := s.ws.recv()
		s.waiting.Store(0)
		s.buf = data
		if err != nil && len(s.buf) == 0 {
			switch {
			case s.dead.Load():
				return 0, errors.New("соединение с воркером замерло: ни данных, ни ответа на ping")
			case err == io.EOF && s.ws.closeCode != 0 && s.ws.closeCode != 1000 && s.ws.closeReason != "":
				// Воркер оборвал соединение сам и назвал причину («tcp: …» — не смог подключиться
				// к сайту): без неё в журнале был бы голый EOF.
				if refusedByPlatform(s.ws.closeReason) {
					if s.onRefused != nil {
						s.onRefused()
					}
					return 0, fmt.Errorf("%w (%s)", ErrBehindCloudflare, s.ws.closeReason)
				}
				return 0, fmt.Errorf("воркер закрыл соединение: %s", s.ws.closeReason)
			}
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
	s.once.Do(func() {
		if s.done != nil {
			close(s.done)
		}
		s.ws.close()
	})
	return nil
}

func (s *wsStream) LocalAddr() net.Addr                { return s.ws.conn.LocalAddr() }
func (s *wsStream) RemoteAddr() net.Addr               { return s.ws.conn.RemoteAddr() }
func (s *wsStream) SetDeadline(t time.Time) error      { return s.ws.conn.SetDeadline(t) }
func (s *wsStream) SetReadDeadline(t time.Time) error  { return s.ws.conn.SetReadDeadline(t) }
func (s *wsStream) SetWriteDeadline(t time.Time) error { return s.ws.conn.SetWriteDeadline(t) }
