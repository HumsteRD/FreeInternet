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
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Вход в Cloudflare. Провайдер ведёт разные диапазоны Cloudflare в разные его дата-центры. Замер
// у одного провайдера (2026-11-06): адреса воркера из DNS (104.21.x, 172.67.x) уходили в Стокгольм —
// путь до Telegram 121–138 мс, новое соединение 305–330 мс; а 162.159.144.x и 173.245.x — в Москву:
// 86–98 мс и 247–270 мс. FI меряет рукопожатие TLS с воркером через образцы из диапазонов
// Cloudflare и ставит самые быстрые адреса первыми.
//
// Но не каждый адрес Cloudflare обслуживает чужой сайт: часть отдаёт сертификат воркера, а на
// запрос отвечает 403 — «error code: 1034» (Edge IP Restricted) или страницей «403 Forbidden»
// (2026-11-11: 172.65.50.139, 104.16.123.96, 141.101.90.17, 172.64.32.17). В 0.4.0 такой адрес
// мог оказаться самым быстрым, и всё, что шло через воркер, получало отказ: свежие ролики
// YouTube, сайты через воркер. Поэтому вход считается годным, только когда через него ответил
// сам воркер, а отказавший при работе вход сразу убирается.

// edgeCandidates — по несколько адресов из каждого диапазона Cloudflare, которые отвечают на 443.
var edgeCandidates = []string{
	"104.17.96.17", "104.18.32.47", "104.19.160.17", "104.20.16.17", "104.21.48.17", "104.22.64.17", "104.23.96.17",
	"104.24.32.17", "104.25.128.17", "104.26.0.17", "104.27.192.17",
	"172.66.0.17", "172.67.64.17", "172.68.96.17", "172.69.128.17", "172.70.160.17", "172.71.192.17",
	"162.158.48.17", "162.158.192.17", "162.159.128.17", "162.159.144.17", "162.159.200.17",
	"141.101.121.126", "141.101.115.104",
	"173.245.49.54", "188.114.96.17", "188.114.98.17", "190.93.244.165", "190.93.246.39",
}

const (
	edgeTTL      = 30 * time.Minute // сеть та же — пинг до дата-центров почти не меняется
	edgeKeep     = 3                // сколько быстрых адресов ставить вперёд
	edgeDialWait = 3 * time.Second
	// edgeChecks — сколько самых быстрых адресов проверять запросом к воркеру: каждая проверка —
	// запрос из дневного лимита воркера, поэтому не все подряд.
	edgeChecks = 9
	// edgeBadFor — сколько не брать адрес, который отказал: правила Cloudflare меняются редко.
	edgeBadFor = 12 * time.Hour
)

// Edge — выбранный вход в Cloudflare для окна и журнала.
type Edge struct {
	IP   string        `json:"ip"`
	RTT  time.Duration `json:"rtt"`
	Colo string        `json:"colo,omitempty"` // код дата-центра Cloudflare: DME — Москва, ARN — Стокгольм
}

type edgeState struct {
	mu   sync.Mutex
	list []Edge
	host string // для какого воркера проверены
	at   time.Time
	busy bool
	bad  map[string]time.Time // адрес отказал воркеру: до этого времени его не берём
}

// Подмены для тестов.
var (
	edgePing  = pingEdge
	edgeCheck = checkEdge
)

// edges — быстрые входы в Cloudflare для воркера host. Если замера нет или он устарел, замер
// идёт в фоне, а до него возвращается прошлый результат — соединение не ждёт.
func (s *Server) edges(host string) []string {
	e := &s.edge
	e.mu.Lock()
	defer e.mu.Unlock()
	if host == "" {
		return nil
	}
	stale := e.host != host || time.Since(e.at) > edgeTTL
	if stale && !e.busy {
		e.busy = true
		go s.measureEdges(host)
	}
	if e.host != host {
		return nil
	}
	ips := make([]string, len(e.list))
	for i, x := range e.list {
		ips[i] = x.IP
	}
	return ips
}

// Edges — выбранные входы в Cloudflare; пусто — ещё не измерены.
func (s *Server) Edges() []Edge {
	s.edge.mu.Lock()
	defer s.edge.mu.Unlock()
	return slices.Clone(s.edge.list)
}

// measureEdges меряет пинг до всех кандидатов и проверяет самых быстрых запросом к воркеру.
func (s *Server) measureEdges(host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := &s.edge
	e.mu.Lock()
	now := time.Now()
	candidates := slices.DeleteFunc(slices.Clone(edgeCandidates), func(ip string) bool { return now.Before(e.bad[ip]) })
	e.mu.Unlock()

	list := pickEdges(ctx, host, candidates)
	e.mu.Lock()
	e.busy, e.at, e.host = false, time.Now(), host
	// Пустой замер (сети нет, воркер не ответил) прежние входы не стирает — кроме отказавших.
	if len(list) > 0 {
		e.list = list
	}
	e.mu.Unlock()
	if len(list) > 0 && s.Log != nil {
		parts := make([]string, len(list))
		for i, x := range list {
			parts[i] = strings.Join(strings.Fields(fmt.Sprintf("%s %s %d мс", x.IP, x.Colo, x.RTT.Milliseconds())), " ")
		}
		s.Log.Info("быстрые входы в Cloudflare", "адреса", strings.Join(parts, ", "))
	}
}

// pickEdges — до edgeKeep адресов с самым быстрым рукопожатием TLS, через которые ответил воркер host.
func pickEdges(ctx context.Context, host string, candidates []string) []Edge {
	var mu sync.Mutex
	var measured []Edge
	var wg sync.WaitGroup
	for _, ip := range candidates {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if rtt, ok := edgePing(ctx, ip, host); ok {
				mu.Lock()
				measured = append(measured, Edge{IP: ip, RTT: rtt})
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	sort.Slice(measured, func(i, j int) bool { return measured[i].RTT < measured[j].RTT })

	// Самые быстрые проверяются разом, а берутся по порядку пинга: кто ответил за воркер.
	top := measured[:min(len(measured), edgeChecks)]
	ok := make([]bool, len(top))
	for i := range top {
		wg.Add(1)
		go func() {
			defer wg.Done()
			colo, err := edgeCheck(ctx, top[i].IP, host)
			top[i].Colo, ok[i] = colo, err == nil
		}()
	}
	wg.Wait()
	var picked []Edge
	for i, e := range top {
		if ok[i] && len(picked) < edgeKeep {
			picked = append(picked, e)
		}
	}
	return picked
}

// pingEdge — лучшее из двух рукопожатий TLS с host через ip, от начала соединения: сертификат
// заодно проверяется по host. TCP-подключение само по себе не годится — VPN в режиме TUN
// принимает его у себя мгновенно, и у всех адресов выходил бы «0 мс».
func pingEdge(ctx context.Context, ip, host string) (time.Duration, bool) {
	best := time.Duration(0)
	for range 2 {
		dctx, cancel := context.WithTimeout(ctx, edgeDialWait)
		start := time.Now()
		conn, err := dialEdgeTLS(dctx, ip, host)
		cancel()
		if err != nil {
			return 0, false
		}
		rtt := time.Since(start)
		conn.Close()
		if best == 0 || rtt < best {
			best = rtt
		}
	}
	return best, true
}

// dialEdgeTLS открывает TLS к host через ip; сертификат проверяется по host.
func dialEdgeTLS(ctx context.Context, ip, host string) (*tls.Conn, error) {
	d := net.Dialer{LocalAddr: wsLocalAddr}
	raw, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(ip, workerPort))
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}, RootCAs: rootCAs})
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return conn, nil
}

// workerGreeting — с этого начинается ответ воркера FI на обычный запрос.
const workerGreeting = "FI worker"

// checkEdge спрашивает воркер host через ip обычным запросом: вход годен, только если ответил
// сам воркер, а не Cloudflare отказом. Заодно узнаёт код дата-центра из заголовка CF-RAY.
func checkEdge(ctx context.Context, ip, host string) (colo string, err error) {
	dctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := dialEdgeTLS(dctx, ip, host)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n"); err != nil {
		return "", err
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), workerGreeting) {
		return "", &statusError{code: resp.StatusCode, body: string(body)}
	}
	if ray := resp.Header.Get("CF-RAY"); strings.Contains(ray, "-") {
		colo = ray[strings.LastIndex(ray, "-")+1:]
	}
	return colo, nil
}

// cfAddrs — адреса воркера: сначала быстрые входы, затем из DNS.
func (s *Server) cfAddrs(ctx context.Context, host string, fast []string) []string {
	ips := s.resolve(ctx, host, 0)
	out := slices.Clone(fast)
	for _, ip := range ips {
		if !slices.Contains(out, ip) {
			out = append(out, ip)
		}
	}
	return out
}

// dropEdge убирает быстрый вход ip, если на нём воркеру отказал сам Cloudflare, не передав
// запрос: полсуток этот адрес не берётся. true — вход убран; о воркере по такой ошибке судить
// нельзя, и адрес из DNS за неё на паузу не ставится.
func (s *Server) dropEdge(ip string, err error) bool {
	var se *statusError
	if !errors.As(err, &se) || !se.fromEdge() {
		return false
	}
	e := &s.edge
	e.mu.Lock()
	was := len(e.list)
	e.list = slices.DeleteFunc(e.list, func(x Edge) bool { return x.IP == ip })
	dropped := len(e.list) < was
	if dropped {
		if e.bad == nil {
			e.bad = map[string]time.Time{}
		}
		e.bad[ip] = time.Now().Add(edgeBadFor)
		e.at = time.Time{} // следующий запрос входов запустит новый замер
	}
	e.mu.Unlock()
	if dropped && s.Log != nil {
		s.Log.Warn("вход в Cloudflare отказал воркеру — убран из быстрых", "ip", ip, "err", se.Error())
	}
	return dropped
}

// resetEdges забывает замер: в новой сети пинг до дата-центров другой.
func (s *Server) resetEdges() {
	s.edge.mu.Lock()
	s.edge.at = time.Time{}
	s.edge.mu.Unlock()
}
