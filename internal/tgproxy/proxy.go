package tgproxy

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	handshakeTimeout = 10 * time.Second
	dialTimeout      = 5 * time.Second
	failCooldown     = time.Minute // неудачный адрес не пробуем повторно минуту
	dnsCacheTTL      = 10 * time.Minute
	// dnsFailTTL — сколько помнить, что адрес не нашёлся: DNS часто сбоит на минуту при смене
	// сети или включении VPN, и десятиминутная пауза оставила бы Telegram без запасного пути.
	dnsFailTTL = 20 * time.Second
	// raceDelay — через сколько запускать следующий путь, если предыдущий ещё не ответил.
	// Рабочий путь открывает WebSocket за 0,2–0,5 с; дольше — скорее всего, он закрыт, и
	// Telegram не должен ждать его таймаутов.
	raceDelay = 600 * time.Millisecond
)

// workerPort — порт Cloudflare; в тестах подменяется на адрес поддельного воркера.
var workerPort = "443"

// Адреса веб-серверов Telegram на случай, если DNS не отвечает или подменён (проверены 2026-09-14).
var knownWSIPs = map[int][]string{
	1: {"149.154.174.100"},
	2: {"149.154.167.99", "149.154.167.220"},
	3: {"149.154.174.100"},
	4: {"149.154.167.99", "149.154.167.220"},
	5: {"149.154.170.100"},
}

// Прямые адреса дата-центров — последний вариант, если WebSocket недоступен.
var (
	dcIPs     = map[int]string{1: "149.154.175.50", 2: "149.154.167.51", 3: "149.154.175.100", 4: "149.154.167.91", 5: "149.154.171.5", 203: "91.105.192.100"}
	testDCIPs = map[int]string{1: "149.154.175.10", 2: "149.154.167.40", 3: "149.154.175.117"}
)

// errNoRoute — все адреса WebSocket недавно не ответили и стоят на паузе.
var errNoRoute = errors.New("адреса WebSocket на паузе после ошибок")

// upstream — соединение прокси с Telegram.
type upstream interface {
	framed() bool // нужен ли один пакет на сообщение
	send(p []byte) error
	recv() ([]byte, error)
	close()
}

// Stats — состояние прокси для окна.
type Stats struct {
	Listening  bool      `json:"listening"`
	Active     int       `json:"active"`
	Total      int       `json:"total"`
	WebSocket  int       `json:"websocket"`  // соединений через WebSocket
	Worker     int       `json:"via_worker"` // из них через Cloudflare Worker
	CF         int       `json:"via_cf"`     // через общие домены Cloudflare (tg-ws-proxy)
	FromPool   int       `json:"from_pool"`  // из них взято готовыми из пула
	Ready      int       `json:"ready"`      // готовых соединений в пуле сейчас
	Direct     int       `json:"direct"`     // напрямую по TCP
	Failed     int       `json:"failed"`
	BadSecret  int       `json:"bad_secret"` // клиенты с чужим секретом: в Telegram сохранён старый прокси
	BadDC      int       `json:"bad_dc"`     // ответов «неверный дата-центр» (-444), не переданных клиенту
	LastError  string    `json:"last_error,omitempty"`
	LastActive time.Time `json:"last_active,omitzero"`
}

// Server — MTProto-прокси на локальном адресе.
type Server struct {
	Addr     string // например 127.0.0.1:1453
	Secret   []byte // 16 байт
	PoolSize int    // готовых соединений на дата-центр: 0 — по умолчанию, меньше нуля — без пула
	Worker   string // домен Cloudflare Worker: путь в Telegram, когда его адреса закрыты по IP
	// WorkerKey — ключ FI в коде воркера: с ним воркер пускает к сайтам «через воркер».
	WorkerKey string
	Log       *slog.Logger
	// LookupFallback ищет адрес, когда системный DNS не ответил (например, DNS поверх HTTPS):
	// провайдеры часто не резолвят общие домены Cloudflare. nil — только системный DNS.
	LookupFallback func(ctx context.Context, host string) (string, error)

	// wsUpstream — подмена подключения по WebSocket в тестах.
	wsUpstream func(ctx context.Context, k poolKey) (upstream, error)

	mu       sync.Mutex
	stats    Stats
	pool     *pool
	failed   map[string]time.Time
	dnsCache map[string]dnsEntry
	logged   map[string]*logEntry  // когда какая ошибка попала в журнал
	active   map[upstream]struct{} // соединения с Telegram, через которые сейчас идут данные

	workerPausedUntil time.Time // воркер молча закрывал соединения — до этого времени в обход него

	cfMu      sync.Mutex
	cfList    []string       // общие домены Cloudflare
	cfActive  map[int]string // домен, сработавший последним для дата-центра
	prefRoute string         // путь, выигравший последним: его пробуем первым
	prefUntil time.Time
}

type dnsEntry struct {
	ips []string
	at  time.Time
}

// ttl — сколько верить записи: неудачный поиск быстро повторяется.
func (e dnsEntry) ttl() time.Duration {
	if len(e.ips) == 0 {
		return dnsFailTTL
	}
	return dnsCacheTTL
}

// Link — ссылка tg://proxy для настройки Telegram одним нажатием.
// Префикс dd включает транспорт со случайным дополнением пакетов.
func Link(addr string, secret []byte) string {
	host, port, _ := net.SplitHostPort(addr)
	return "tg://proxy?server=" + host + "&port=" + port + "&secret=dd" + hex.EncodeToString(secret)
}

// Stats возвращает снимок состояния.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	st, p := s.stats, s.pool
	s.mu.Unlock()
	if p != nil {
		st.Ready = p.ready()
	}
	return st
}

// ListenAndServe принимает соединения Telegram, пока не отменён ctx.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if len(s.Secret) != 16 {
		return errors.New("секрет прокси должен быть 16 байт")
	}
	l, err := net.Listen("tcp", s.Addr)
	if err != nil {
		s.setError(fmt.Errorf("адрес %s занят: %w", s.Addr, err))
		return err
	}

	size := s.PoolSize
	if size == 0 {
		size = defaultPoolSize
	}
	var p *pool
	if size > 0 {
		p = newPool(size, s.dialUpstream)
		defer p.close() // после того как закончились все соединения
	}
	s.mu.Lock()
	s.stats.Listening, s.stats.LastError = true, ""
	s.pool = p
	s.mu.Unlock()
	stop := context.AfterFunc(ctx, func() { l.Close() })
	defer stop()

	var wg sync.WaitGroup
	defer func() {
		wg.Wait()
		s.mu.Lock()
		s.stats.Listening = false
		s.pool = nil
		s.mu.Unlock()
	}()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(ctx, conn)
		}()
	}
}

func (s *Server) handle(ctx context.Context, client net.Conn) {
	defer client.Close()
	s.mu.Lock()
	s.stats.Active++
	s.stats.LastActive = time.Now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.stats.Active--
		s.mu.Unlock()
	}()

	client.SetReadDeadline(time.Now().Add(handshakeTimeout))
	init := make([]byte, initLen)
	if _, err := io.ReadFull(client, init); err != nil {
		return
	}
	client.SetReadDeadline(time.Time{})

	ci, err := parseInit(init, s.Secret)
	if err != nil {
		s.mu.Lock()
		s.stats.BadSecret++
		s.mu.Unlock()
		s.setError(err)
		return
	}
	s.mu.Lock()
	s.stats.Total++ // считаем только клиентов с нашим секретом: они действительно пользуются прокси
	s.mu.Unlock()
	test := ci.dc >= 10000 // тестовые дата-центры Telegram Desktop помечает сдвигом на 10000
	dc := ci.dc
	if test {
		dc -= 10000
	}
	idx := int16(dc)
	if ci.media {
		idx = -idx
	}
	relayInit := newRelayInit(ci.proto, idx)
	crypto := newCrypto(ci, s.Secret, relayInit)

	up, pooled, err := s.connect(ctx, dc, ci.media, test)
	if err != nil {
		s.setError(fmt.Errorf("дата-центр %d: %w", dc, err))
		return
	}
	stop := context.AfterFunc(ctx, func() {
		client.Close()
		up.close()
	})
	defer stop()
	defer up.close()
	s.track(up, true)
	defer s.track(up, false)

	if err := up.send(relayInit); err != nil {
		return
	}
	// Готовое соединение из пула Telegram мог успеть закрыть сам (простаивающие он закрывает
	// за минуту-другую) — по нему о воркере судить нельзя.
	if silent := bridge(client, up, crypto, ci.proto, func() { s.noteBadDC(dc, ci.media, up) }); silent && !pooled && isViaWorker(up) {
		s.pauseWorker(up.(*wsConn).closeReason)
	}
}

// workerPause — на сколько отложить воркер, который закрывает соединения, ничего не передав.
const workerPause = time.Minute

// pauseWorker откладывает воркер: соединение с ним открылось, а данных от Telegram не было.
// Без этого неисправный воркер (например, со старым кодом) оставил бы Telegram без связи —
// до прямого пути и Cloudflare дело бы не доходило.
func (s *Server) pauseWorker(reason string) {
	s.mu.Lock()
	already := time.Now().Before(s.workerPausedUntil)
	s.workerPausedUntil = time.Now().Add(workerPause)
	p := s.pool
	s.mu.Unlock()
	if already {
		return
	}
	if p != nil {
		p.flush() // в пуле — соединения через тот же воркер
	}
	if s.Log != nil {
		s.Log.Warn("воркер закрывает соединения, ничего не передав, — минуту Telegram идёт в обход него", "worker", s.Worker, "reason", reason)
	}
}

func (s *Server) workerPaused() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.workerPausedUntil)
}

func isViaWorker(up upstream) bool {
	w, ok := up.(*wsConn)
	return ok && w.viaWorker
}

func (s *Server) track(up upstream, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !on {
		delete(s.active, up)
		return
	}
	if s.active == nil {
		s.active = map[upstream]struct{}{}
	}
	s.active[up] = struct{}{}
}

// NetworkChanged вызывается, когда сменилась сеть (включили или выключили VPN). Соединения,
// открытые через прежний путь, молча перестают работать: Telegram ждал бы их таймаутов и
// показывал «connecting…». Поэтому пул и активные соединения закрываются — Telegram сразу
// переподключается, — а адреса из DNS и паузы неудачных путей забываются.
func (s *Server) NetworkChanged() {
	s.mu.Lock()
	p := s.pool
	active := make([]upstream, 0, len(s.active))
	for up := range s.active {
		active = append(active, up)
	}
	s.dnsCache, s.failed, s.workerPausedUntil = nil, nil, time.Time{}
	s.mu.Unlock()
	s.setPreferred("")
	if p != nil {
		p.flush()
	}
	for _, up := range active {
		up.close()
	}
}

// bridge перешифровывает трафик в обе стороны, пока одна из сторон не закроется.
// onBadDC вызывается, когда Telegram прислал код -444: соединение тогда закрывается.
// silent — соединение закрыла сторона Telegram, не прислав ни байта.
func bridge(client net.Conn, up upstream, c *cryptoCtx, proto uint32, onBadDC func()) (silent bool) {
	clientDone, upDone := make(chan struct{}), make(chan bool, 1)
	go func() { // клиент → Telegram
		defer close(clientDone)
		sp := &splitter{proto: proto}
		buf := make([]byte, 64<<10)
		send := func(p []byte) error {
			c.tgEnc.XORKeyStream(p, p)
			return up.send(p)
		}
		for {
			n, err := client.Read(buf)
			if n > 0 {
				data := buf[:n]
				c.clientDec.XORKeyStream(data, data)
				if !up.framed() {
					if send(data) != nil {
						return
					}
				} else {
					for _, p := range sp.push(data) {
						if send(p) != nil {
							return
						}
					}
				}
			}
			if err != nil {
				for _, p := range sp.flush() {
					send(p)
				}
				return
			}
		}
	}()
	go func() { // Telegram → клиент
		got := false
		defer func() { upDone <- got }()
		w := &watcher{proto: proto}
		for {
			data, err := up.recv()
			if len(data) > 0 {
				got = true
				c.tgDec.XORKeyStream(data, data)
				data, bad := w.scan(data)
				c.clientEnc.XORKeyStream(data, data)
				if _, werr := client.Write(data); werr != nil {
					return
				}
				if bad {
					onBadDC()
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-clientDone:
		client.Close()
		up.close()
		<-upDone
		return false
	case got := <-upDone:
		client.Close()
		up.close()
		<-clientDone
		return !got
	}
}

// Probe проверяет, доходит ли прокси до Telegram: открывает соединение с дата-центром 2
// теми же путями, что и для клиентов, но мимо пула и счётчиков, и сразу закрывает его.
// Возвращает, каким путём прошло соединение.
func (s *Server) Probe(ctx context.Context) (string, error) {
	up, err := s.dialUpstream(ctx, poolKey{dc: 2})
	if err == nil {
		up.close()
		return routeName(up), nil
	}
	d := net.Dialer{Timeout: dialTimeout}
	conn, derr := d.DialContext(ctx, "tcp4", net.JoinHostPort(dcIPs[2], "443"))
	if derr != nil {
		return "", errors.Join(err, fmt.Errorf("напрямую %s: %w", dcIPs[2], derr))
	}
	conn.Close()
	return routeName(nil), nil
}

// routeName — каким путём идёт соединение с Telegram, для окна и журнала.
func routeName(up upstream) string {
	w, ok := up.(*wsConn)
	switch {
	case !ok:
		return "напрямую"
	case w.viaWorker:
		return "свой воркер"
	case w.viaCF:
		return "Cloudflare"
	}
	return "веб-версию Telegram"
}

// connect подключается к дата-центру: готовое соединение из пула, новый WebSocket веб-версии
// Telegram по адресу из DNS или запасного списка, и только затем напрямую по TCP.
func (s *Server) connect(ctx context.Context, dc int, media, test bool) (up upstream, pooled bool, err error) {
	k := poolKey{dc: dc, media: media, test: test}
	s.mu.Lock()
	p := s.pool
	s.mu.Unlock()
	if p != nil {
		defer p.use(k) // взятое или неудачное — пул дата-центра пополнится в фоне
		if up := p.take(k); up != nil {
			s.count(true, true)
			s.countWorker(up)
			return up, true, nil
		}
	}

	up, err = s.dialUpstream(ctx, k)
	if err == nil {
		s.count(true, false)
		s.countWorker(up)
		return up, false, nil
	}
	errs := []error{err}

	ips := dcIPs
	if test {
		ips = testDCIPs
	}
	if ip, ok := ips[dc]; ok {
		d := net.Dialer{Timeout: dialTimeout}
		conn, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(ip, "443"))
		if err == nil {
			s.count(false, false)
			return &tcpUpstream{conn: conn, buf: make([]byte, 64<<10)}, false, nil
		}
		errs = append(errs, fmt.Errorf("напрямую %s: %w", ip, err))
	}
	return nil, false, errors.Join(errs...)
}

// dialRoute — путь к дата-центру Telegram.
type dialRoute struct {
	name string // worker, ws, cf
	dial func(context.Context, poolKey) (upstream, error)
}

// preferFor — сколько первым пробовать путь, который выиграл последним.
const preferFor = 10 * time.Minute

// dialUpstream открывает новое соединение с дата-центром. Пути — свой Cloudflare Worker,
// WebSocket веб-версии Telegram и общие домены Cloudflare — запускаются внахлёст: первым тот,
// что выиграл недавно, следующий — если предыдущий упал или не ответил за raceDelay. Берётся
// самое быстрое соединение: Telegram не ждёт таймаутов закрытого пути.
func (s *Server) dialUpstream(ctx context.Context, k poolKey) (upstream, error) {
	routes := s.routeOrder()
	dials := make([]func(context.Context, poolKey) (upstream, error), len(routes))
	for i, r := range routes {
		dials[i] = r.dial
	}
	up, i, err := raceRoutes(ctx, k, dials)
	if err != nil {
		return nil, err
	}
	s.setPreferred(routes[i].name)
	return up, nil
}

// routeOrder — пути по очереди: недавний победитель первым, остальные — воркер, WebSocket, Cloudflare.
func (s *Server) routeOrder() []dialRoute {
	var routes []dialRoute
	if s.Worker != "" && !s.workerPaused() {
		routes = append(routes, dialRoute{"worker", s.dialWorker})
	}
	routes = append(routes, dialRoute{"ws", s.dialWebSocket}, dialRoute{"cf", s.dialCF})
	pref := s.preferred()
	if i := slices.IndexFunc(routes, func(r dialRoute) bool { return r.name == pref }); i > 0 {
		r := routes[i]
		routes = append(append([]dialRoute{r}, routes[:i]...), routes[i+1:]...)
	}
	return routes
}

func (s *Server) preferred() string {
	s.cfMu.Lock()
	defer s.cfMu.Unlock()
	if time.Now().After(s.prefUntil) {
		return ""
	}
	return s.prefRoute
}

func (s *Server) setPreferred(name string) {
	s.cfMu.Lock()
	defer s.cfMu.Unlock()
	s.prefRoute, s.prefUntil = name, time.Now().Add(preferFor)
	if name == "" {
		s.prefUntil = time.Time{}
	}
}

// raceRoutes запускает пути по очереди: следующий — если предыдущий упал или не ответил за
// raceDelay. Берётся соединение, открывшееся раньше; опоздавшие закрываются. winner — номер пути.
func raceRoutes(ctx context.Context, k poolKey, routes []func(context.Context, poolKey) (upstream, error)) (up upstream, winner int, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		up  upstream
		err error
		i   int
	}
	results := make(chan result, len(routes))
	started := 0
	start := func() {
		i := started
		started++
		go func() {
			up, err := routes[i](ctx, k)
			results <- result{up, err, i}
		}()
	}
	start()
	timer := time.NewTimer(raceDelay)
	defer timer.Stop()

	var errs []error
	for done := 0; done < started; {
		select {
		case <-timer.C:
			if started < len(routes) {
				start()
				timer.Reset(raceDelay)
			}
		case r := <-results:
			done++
			if r.err == nil {
				if pending := started - done; pending > 0 {
					go func() { // опоздавшие пути: соединения уже не нужны
						for range pending {
							if late := <-results; late.err == nil {
								late.up.close()
							}
						}
					}()
				}
				return r.up, r.i, nil
			}
			if !errors.Is(r.err, errNoCF) {
				errs = append(errs, r.err)
			}
			if started < len(routes) {
				start() // путь не прошёл — следующий ждать незачем
				timer.Reset(raceDelay)
			}
		}
	}
	return nil, -1, errors.Join(errs...)
}

// workerPath — запрос к нашему воркеру: какой дата-центр открыть.
func workerPath(k poolKey) string {
	path := fmt.Sprintf("/?dc=%d", k.dc)
	if k.test {
		path += "&test=1"
	}
	return path
}

// dialWorker подключается к Telegram через Cloudflare Worker: тот открывает TCP до дата-центра
// и перекладывает байты в WebSocket. Границы сообщений ему не важны — соединение потоковое.
func (s *Server) dialWorker(ctx context.Context, k poolKey) (upstream, error) {
	var errs []error
	for _, ip := range s.resolve(ctx, s.Worker, 0) {
		key := "worker@" + ip
		if s.coolingDown(key) {
			continue
		}
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		ws, err := dialWS(dialCtx, net.JoinHostPort(ip, workerPort), s.Worker, workerPath(k))
		cancel()
		if err == nil {
			ws.raw, ws.viaWorker = true, true
			return ws, nil
		}
		if ctx.Err() == nil {
			s.markFailed(key)
		}
		errs = append(errs, fmt.Errorf("воркер %s через %s: %w", s.Worker, ip, err))
	}
	if len(errs) == 0 {
		return nil, fmt.Errorf("воркер %s: адрес не найден", s.Worker)
	}
	return nil, errors.Join(errs...)
}

// NormalizeWorker приводит адрес воркера к домену: из «https://x.workers.dev/» выходит «x.workers.dev».
func NormalizeWorker(value string) (string, error) {
	host := strings.TrimSpace(value)
	if host == "" {
		return "", nil
	}
	host = strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://")
	if i := strings.IndexAny(host, "/?#"); i >= 0 {
		host = host[:i]
	}
	host = strings.ToLower(strings.Trim(host, "."))
	if !strings.Contains(host, ".") || strings.ContainsAny(host, " \t:@\\") {
		return "", fmt.Errorf("%q не похоже на домен воркера", value)
	}
	return host, nil
}

// dialWebSocket перебирает адреса WebSocket дата-центра; неудачные на минуту пропускает.
func (s *Server) dialWebSocket(ctx context.Context, k poolKey) (upstream, error) {
	if s.wsUpstream != nil {
		return s.wsUpstream(ctx, k)
	}
	path := "/apiws"
	if k.test {
		path = "/apiws_test"
	}

	var errs []error
	cooling := false
	for _, host := range wsHosts(k.dc, k.media) {
		for _, ip := range s.resolve(ctx, host, k.dc) {
			key := host + "@" + ip
			if s.coolingDown(key) {
				cooling = true
				continue
			}
			dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
			ws, err := dialWS(dialCtx, net.JoinHostPort(ip, "443"), host, path)
			cancel()
			if err == nil {
				return ws, nil
			}
			if ctx.Err() == nil { // закрытие прокси — не повод ставить адрес на паузу
				s.markFailed(key)
			}
			errs = append(errs, fmt.Errorf("%s через %s: %w", host, ip, err))
		}
	}
	switch {
	case len(errs) > 0:
		return nil, errors.Join(errs...)
	case cooling:
		return nil, errNoRoute
	default:
		return nil, fmt.Errorf("неизвестный дата-центр %d", k.dc)
	}
}

// wsHosts — адреса WebSocket дата-центра; файлы идут через «-1», но годится и основной.
func wsHosts(dc int, media bool) []string {
	if dc == 203 { // дата-центр для рекламы и CDN обслуживается вторым
		dc = 2
	}
	main, files := fmt.Sprintf("kws%d.web.telegram.org", dc), fmt.Sprintf("kws%d-1.web.telegram.org", dc)
	if media {
		return []string{files, main}
	}
	return []string{main, files}
}

// resolve — адреса хоста: из системного DNS (с кэшем), дополненные запасным списком.
func (s *Server) resolve(ctx context.Context, host string, dc int) []string {
	s.mu.Lock()
	entry, ok := s.dnsCache[host]
	s.mu.Unlock()

	if !ok || time.Since(entry.at) > entry.ttl() {
		lookupCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		addrs, err := net.DefaultResolver.LookupIP(lookupCtx, "ip4", host)
		cancel()
		entry = dnsEntry{at: time.Now()}
		if err == nil {
			for _, a := range addrs {
				if !a.IsPrivate() && !a.IsLoopback() && !a.IsUnspecified() {
					entry.ips = append(entry.ips, a.String())
				}
			}
		}
		if len(entry.ips) == 0 && s.LookupFallback != nil {
			lookupCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
			if ip, err := s.LookupFallback(lookupCtx, host); err == nil {
				entry.ips = []string{ip}
			}
			cancel()
		}
		s.mu.Lock()
		if s.dnsCache == nil {
			s.dnsCache = map[string]dnsEntry{}
		}
		s.dnsCache[host] = entry
		s.mu.Unlock()
	}

	ips := append([]string(nil), entry.ips...)
	for _, ip := range knownWSIPs[dc] {
		if !contains(ips, ip) {
			ips = append(ips, ip)
		}
	}
	return ips
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Server) coolingDown(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.failed[key])
}

func (s *Server) markFailed(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failed == nil {
		s.failed = map[string]time.Time{}
	}
	s.failed[key] = time.Now().Add(failCooldown)
}

// countWorker отмечает соединения, прошедшие через Cloudflare: воркер или общие домены.
func (s *Server) countWorker(up upstream) {
	w, ok := up.(*wsConn)
	if !ok || !w.viaWorker && !w.viaCF {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if w.viaWorker {
		s.stats.Worker++
	} else {
		s.stats.CF++
	}
}

// noteBadDC отмечает, что Telegram ответил кодом -444 через этот путь. Код клиенту не передан,
// соединение закрыто; общий домен Cloudflare, через который он пришёл, отдыхает.
func (s *Server) noteBadDC(dc int, media bool, up upstream) {
	route := "напрямую"
	if w, ok := up.(*wsConn); ok {
		switch {
		case w.viaWorker:
			route = "воркер " + w.host
		case w.viaCF:
			route = "Cloudflare " + w.host
			s.markFailed("cf@" + w.cfBase)
		default:
			route = "WebSocket " + w.host
		}
	}
	s.mu.Lock()
	s.stats.BadDC++
	s.mu.Unlock()
	if s.Log != nil {
		s.Log.Warn("Telegram ответил «неверный дата-центр» (-444): соединение закрыто, чтобы клиент не отключил прокси",
			"dc", dc, "media", media, "route", route)
	}
}

func isViaCF(up upstream) bool {
	w, ok := up.(*wsConn)
	return ok && w.viaCF
}

func (s *Server) count(websocket, pooled bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case pooled:
		s.stats.WebSocket++
		s.stats.FromPool++
	case websocket:
		s.stats.WebSocket++
	default:
		s.stats.Direct++
	}
}

func (s *Server) setError(err error) {
	msg := err.Error()
	now := time.Now()
	s.mu.Lock()
	s.stats.Failed++
	s.stats.LastError = msg
	// Одинаковые ошибки идут очередями (клиент со старым секретом стучится десятки раз
	// в секунду): в журнал такая попадает не чаще раза в минуту, с числом пропущенных.
	if s.logged == nil || len(s.logged) > 200 {
		s.logged = map[string]*logEntry{}
	}
	e := s.logged[msg]
	if e != nil && now.Sub(e.at) < logEvery {
		e.skipped++
		s.mu.Unlock()
		return
	}
	skipped := 0
	if e != nil {
		skipped = e.skipped
	}
	s.logged[msg] = &logEntry{at: now}
	s.mu.Unlock()
	if s.Log == nil {
		return
	}
	if skipped > 0 {
		s.Log.Warn("прокси Telegram", "err", err, "repeats", skipped)
	} else {
		s.Log.Warn("прокси Telegram", "err", err)
	}
}

// logEvery — одна и та же ошибка прокси попадает в журнал не чаще.
const logEvery = time.Minute

type logEntry struct {
	at      time.Time
	skipped int // сколько раз ошибка повторилась, не попав в журнал
}

// tcpUpstream — прямое соединение с дата-центром.
type tcpUpstream struct {
	conn net.Conn
	buf  []byte
}

func (t *tcpUpstream) framed() bool { return false }

func (t *tcpUpstream) send(p []byte) error {
	_, err := t.conn.Write(p)
	return err
}

func (t *tcpUpstream) recv() ([]byte, error) {
	n, err := t.conn.Read(t.buf)
	return t.buf[:n], err
}

func (t *tcpUpstream) close() { t.conn.Close() }
