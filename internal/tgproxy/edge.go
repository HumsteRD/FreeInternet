package tgproxy

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// Вход в Cloudflare. Любой адрес Cloudflare принимает соединение к любому сайту на нём — к воркеру
// и к общим доменам, — а провайдер ведёт разные диапазоны в разные дата-центры Cloudflare. Замер
// у одного провайдера (2026-11-06): адреса воркера из DNS (104.21.x, 172.67.x) уходили в Стокгольм —
// путь до Telegram 121–138 мс, новое соединение 305–330 мс; а 162.159.144.x и 172.65.x — в Москву:
// 86–98 мс и 247–270 мс. FI меряет рукопожатие TLS с воркером через образцы из диапазонов
// Cloudflare (сертификат проверяется по воркеру) и ставит самые быстрые адреса первыми.

// edgeCandidates — по несколько адресов из каждого диапазона Cloudflare, которые отвечают на 443.
var edgeCandidates = []string{
	"104.16.123.96", "104.17.96.17", "104.18.32.47", "104.19.160.17", "104.20.16.17", "104.21.48.17", "104.22.64.17", "104.23.96.17",
	"104.24.32.17", "104.25.128.17", "104.26.0.17", "104.27.192.17",
	"172.64.32.17", "172.65.50.139", "172.66.0.17", "172.67.64.17", "172.68.96.17", "172.69.128.17", "172.70.160.17", "172.71.192.17",
	"162.158.48.17", "162.158.192.17", "162.159.128.17", "162.159.144.17", "162.159.200.17",
	"141.101.121.126", "141.101.90.17", "141.101.115.104",
	"173.245.49.54", "188.114.96.17", "188.114.98.17", "190.93.244.165", "190.93.246.39",
}

const (
	edgeTTL      = 30 * time.Minute // сеть та же — пинг до дата-центров почти не меняется
	edgeKeep     = 3                // сколько быстрых адресов ставить вперёд
	edgeDialWait = 3 * time.Second
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
	host string // для какого домена проверены
	at   time.Time
	busy bool
}

// Подмены для тестов.
var (
	edgePing  = pingEdge
	edgeCheck = checkEdge
)

// edges — быстрые входы в Cloudflare для домена host (воркера). Если замера нет или он устарел,
// замер идёт в фоне, а до него возвращается прошлый результат — соединение не ждёт.
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

// measureEdges меряет пинг до всех кандидатов и проверяет самых быстрых.
func (s *Server) measureEdges(host string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	list := pickEdges(ctx, host, edgeCandidates)
	e := &s.edge
	e.mu.Lock()
	e.busy, e.at, e.host = false, time.Now(), host
	if len(list) > 0 {
		e.list = list
	}
	e.mu.Unlock()
	if len(list) > 0 && s.Log != nil {
		parts := make([]string, len(list))
		for i, x := range list {
			parts[i] = fmt.Sprintf("%s %s %d мс", x.IP, x.Colo, x.RTT.Milliseconds())
		}
		s.Log.Info("быстрые входы в Cloudflare", "адреса", strings.Join(parts, ", "))
	}
}

// pickEdges — до edgeKeep адресов с самым быстрым рукопожатием TLS с host.
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
	picked := measured[:min(len(measured), edgeKeep)]
	for i := range picked {
		picked[i].Colo, _ = edgeCheck(ctx, picked[i].IP, host) // код дата-центра — только для окна
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
	raw, err := d.DialContext(ctx, "tcp4", net.JoinHostPort(ip, "443"))
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

// checkEdge узнаёт код дата-центра Cloudflare из /cdn-cgi/trace.
func checkEdge(ctx context.Context, ip, host string) (string, error) {
	dctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	conn, err := dialEdgeTLS(dctx, ip, host)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	io.WriteString(conn, "GET /cdn-cgi/trace HTTP/1.1\r\nHost: "+host+"\r\nConnection: close\r\n\r\n")
	sc := bufio.NewScanner(io.LimitReader(conn, 8<<10))
	for sc.Scan() {
		if colo, ok := strings.CutPrefix(sc.Text(), "colo="); ok {
			return colo, nil
		}
	}
	return "", nil
}

// cfAddrs — адреса для домена на Cloudflare: сначала быстрые входы, затем из DNS.
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

// resetEdges забывает замер: в новой сети пинг до дата-центров другой.
func (s *Server) resetEdges() {
	s.edge.mu.Lock()
	s.edge.at = time.Time{}
	s.edge.mu.Unlock()
}

// edgeRef — по какому домену проверять входы для общих доменов Cloudflare: по воркеру, а без
// него — по сайту самого Cloudflare (любой его адрес отдаёт и общие домены).
func (s *Server) edgeRef() string {
	if s.Worker != "" {
		return s.Worker
	}
	return "www.cloudflare.com"
}
