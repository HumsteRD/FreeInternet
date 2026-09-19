package tgproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Общие домены Cloudflare из tg-ws-proxy (Flowseal, MIT): их DNS-записи ведут через Cloudflare
// на веб-серверы Telegram. Когда адреса Telegram закрыты по IP, а Cloudflare открыт, прокси
// открывает WebSocket на kwsN.<домен>. Список закодирован, лежит в репозитории tg-ws-proxy и там
// обновляется; FI берёт его оттуда, а не хранит копию у себя.

// CFDomainsURL — список общих доменов tg-ws-proxy.
const CFDomainsURL = "https://raw.githubusercontent.com/Flowseal/tg-ws-proxy/main/.github/cfproxy-domains.txt"

const (
	minCFDomains = 3 // меньше доменов в ответе — список испорчен, прежний лучше
	cfTries      = 4 // сколько доменов пробовать за одно соединение, если сеть лежит
	cfPreferFor  = 30 * time.Minute
)

// errNoCF — общих доменов нет или путь не подходит (тестовые дата-центры).
var errNoCF = errors.New("общих доменов Cloudflare нет")

var cfDomainPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// DecodeCFDomain раскодирует домен из списка tg-ws-proxy: буквы сдвинуты на число букв в имени,
// а «.com» на конце означает «.co.uk».
func DecodeCFDomain(s string) string {
	p, ok := strings.CutSuffix(s, ".com")
	if !ok {
		return s
	}
	n := 0
	for _, r := range p {
		if isASCIILetter(r) {
			n++
		}
	}
	var b strings.Builder
	for _, r := range p {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune('a' + ((r-'a'-rune(n))%26+26)%26)
		case r >= 'A' && r <= 'Z':
			b.WriteRune('A' + ((r-'A'-rune(n))%26+26)%26)
		default:
			b.WriteRune(r)
		}
	}
	return b.String() + ".co.uk"
}

func isASCIILetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

// ParseCFDomains разбирает список: строка — домен, «#» — комментарий. Неверные и повторы пропускаются.
func ParseCFDomains(text string) []string {
	var list []string
	for line := range strings.Lines(text) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		domain := strings.ToLower(DecodeCFDomain(line))
		if cfDomainPattern.MatchString(domain) && !slices.Contains(list, domain) {
			list = append(list, domain)
		}
	}
	return list
}

// FetchCFDomains скачивает свежий список общих доменов.
func FetchCFDomains(ctx context.Context, client *http.Client) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	// Случайный хвост — чтобы не получить устаревшую копию из кэша GitHub.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s?%d", CFDomainsURL, rand.Uint32()), nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("список доменов Cloudflare: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("список доменов Cloudflare: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return nil, fmt.Errorf("список доменов Cloudflare: %w", err)
	}
	list := ParseCFDomains(string(body))
	if len(list) < minCFDomains {
		return nil, fmt.Errorf("список доменов Cloudflare подозрительно короткий: %d", len(list))
	}
	return list, nil
}

// SetCFDomains задаёт общие домены Cloudflare; пустой список выключает этот путь.
func (s *Server) SetCFDomains(list []string) {
	s.cfMu.Lock()
	defer s.cfMu.Unlock()
	s.cfList = slices.Clone(list)
	for dc, domain := range s.cfActive {
		if !slices.Contains(list, domain) {
			delete(s.cfActive, dc)
		}
	}
}

// cfOrder — в каком порядке пробовать домены для дата-центра: сначала тот, что сработал
// последним, потом остальные вразнобой, чтобы не нагружать один.
func (s *Server) cfOrder(dc int) []string {
	s.cfMu.Lock()
	defer s.cfMu.Unlock()
	if len(s.cfList) == 0 {
		return nil
	}
	order := make([]string, 0, len(s.cfList))
	active := s.cfActive[dc]
	if active != "" {
		order = append(order, active)
	}
	for _, i := range rand.Perm(len(s.cfList)) {
		if s.cfList[i] != active {
			order = append(order, s.cfList[i])
		}
	}
	return order
}

// dialCF открывает WebSocket к веб-серверу Telegram через общий домен Cloudflare.
func (s *Server) dialCF(ctx context.Context, k poolKey) (upstream, error) {
	if k.test {
		return nil, errNoCF
	}
	order := s.cfOrder(k.dc)
	if len(order) == 0 {
		return nil, errNoCF
	}
	var errs []error
	tried := 0
	for _, base := range order {
		if tried == cfTries {
			break
		}
		key := "cf@" + base
		if s.coolingDown(key) {
			continue
		}
		tried++
		host := fmt.Sprintf("kws%d.%s", k.dc, base)
		ws, err := s.dialHost(ctx, host, "/apiws")
		if err == nil {
			s.cfMu.Lock()
			if s.cfActive == nil {
				s.cfActive = map[int]string{}
			}
			s.cfActive[k.dc] = base
			s.cfMu.Unlock()
			ws.viaCF, ws.cfBase = true, base
			return ws, nil
		}
		if ctx.Err() == nil {
			s.markFailed(key)
		}
		errs = append(errs, fmt.Errorf("Cloudflare %s: %w", host, err))
	}
	if len(errs) == 0 {
		return nil, errNoRoute
	}
	return nil, errors.Join(errs...)
}

// dialHost открывает WebSocket к домену по первым его адресам из DNS.
func (s *Server) dialHost(ctx context.Context, host, path string) (*wsConn, error) {
	ips := s.resolve(ctx, host, 0)
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s: адрес не найден", host)
	}
	var err error
	for _, ip := range ips[:min(len(ips), 2)] {
		dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
		var ws *wsConn
		ws, err = dialWS(dialCtx, net.JoinHostPort(ip, "443"), host, path)
		cancel()
		if err == nil {
			return ws, nil
		}
	}
	return nil, err
}

// preferCF сообщает, что прямой путь недавно не прошёл, а Cloudflare сработал: пока это так,
// первым пробуем Cloudflare и не ждём таймаутов прямого.
func (s *Server) preferCF() bool {
	s.cfMu.Lock()
	defer s.cfMu.Unlock()
	return time.Now().Before(s.cfPreferUntil)
}

func (s *Server) setPreferCF(on bool) {
	s.cfMu.Lock()
	defer s.cfMu.Unlock()
	if on {
		s.cfPreferUntil = time.Now().Add(cfPreferFor)
	} else {
		s.cfPreferUntil = time.Time{}
	}
}
