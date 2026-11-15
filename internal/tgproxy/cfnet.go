package tgproxy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// Сайты за Cloudflare. Воркер открывает TCP сам, а к адресам самого Cloudflare платформа ему
// подключаться не даёт: соединение закрывается с «proxy request failed, cannot connect to the
// specified address». За Cloudflare стоит заметная часть сайтов — Discord, ChatGPT, X, Medium
// (проверено 2026-11-15, TestLiveWorkerReach). Раньше такой сайт, добавленный «через воркер»,
// молча не открывался, а программа, запущенная через Cloudflare, не могла до него достучаться.
//
// Теперь FI такие адреса узнаёт: заранее — по диапазонам Cloudflare, а если адрес в них не
// попал — по отказу воркера. Сменить страну для такого сайта воркером нельзя, поэтому окно
// говорит об этом прямо, а программы идут к нему напрямую (через обход) и не ломаются.

// cloudflareNets — адреса Cloudflare: https://www.cloudflare.com/ips/ (список не меняется годами).
var cloudflareNets = parseNets(
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
)

func parseNets(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}

// IsCloudflareIP — адрес принадлежит Cloudflare.
func IsCloudflareIP(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, n := range cloudflareNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ErrBehindCloudflare — сайт стоит за Cloudflare, и воркер к нему не подключится.
var ErrBehindCloudflare = errors.New("сайт стоит за Cloudflare, а воркер Cloudflare к таким не подключается")

// errWorkerRefused — Cloudflare не дал воркеру подключиться к сайту, хотя адрес не из его
// диапазонов: порт закрыт, сервер не отвечает. Тем же ответом Cloudflare встречает и свои адреса,
// поэтому причину FI уточняет по адресу сайта.
var errWorkerRefused = errors.New("воркер не смог подключиться к сайту")

const (
	// behindCFFor — сколько помнить, что сайт стоит за Cloudflare: хостинг меняют редко.
	behindCFFor = 6 * time.Hour
	// refusedFor — сколько не водить через воркер сайт, к которому он не смог подключиться, хотя
	// адрес не из диапазонов Cloudflare: причина может быть временной.
	refusedFor   = 10 * time.Minute
	cantReachMax = 2000
)

// unreachable — воркер к сайту не подключится до времени until; cloudflare — потому что сайт
// стоит за Cloudflare, а не потому, что сервер сейчас не отвечает.
type unreachable struct {
	until      time.Time
	cloudflare bool
}

// refusedByPlatform — воркер закрыл соединение, потому что Cloudflare не дал ему подключиться
// к адресу сайта.
func refusedByPlatform(reason string) bool {
	return strings.Contains(reason, "cannot connect to the specified address")
}

func hostKey(host string) string { return strings.ToLower(strings.TrimSuffix(host, ".")) }

func (s *Server) noteCantReach(host string, d time.Duration, cloudflare bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cantReach == nil || len(s.cantReach) > cantReachMax {
		s.cantReach = map[string]unreachable{}
	}
	s.cantReach[hostKey(host)] = unreachable{until: time.Now().Add(d), cloudflare: cloudflare}
}

// workerCantReach — помнится, что воркер к этому сайту подключиться не может; cloudflare —
// потому что сайт стоит за Cloudflare.
func (s *Server) workerCantReach(host string) (cant, cloudflare bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.cantReach[hostKey(host)]
	if !time.Now().Before(u.until) {
		return false, false
	}
	return true, u.cloudflare
}

// cantReachError — почему воркер к сайту не подключится; nil — такого не помнится.
func (s *Server) cantReachError(host string) error {
	cant, cloudflare := s.workerCantReach(host)
	switch {
	case !cant:
		return nil
	case cloudflare:
		return fmt.Errorf("%s: %w", host, ErrBehindCloudflare)
	}
	return fmt.Errorf("%s: %w", host, errWorkerRefused)
}

// refused — Cloudflare не дал воркеру подключиться к сайту. Ближайшие минуты сайт через воркер
// не водим; если его адреса из диапазонов Cloudflare — это надолго. Возвращает причину.
func (s *Server) refused(host string) error {
	if err := s.cantReachError(host); err != nil {
		return err
	}
	s.noteCantReach(host, refusedFor, false)
	if s.Log != nil {
		s.Log.Info("воркер не смог подключиться к сайту — ближайшие минуты этот сайт идёт мимо воркера", "site", host)
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		if s.allCloudflare(s.lookupIPs(ctx, host)) {
			s.noteCantReach(host, behindCFFor, true)
		}
	}()
	return fmt.Errorf("%s: %w", host, errWorkerRefused)
}

// allCloudflare — у сайта есть адреса, и все они принадлежат Cloudflare.
func (s *Server) allCloudflare(ips []string) bool {
	for _, ip := range ips {
		if !IsCloudflareIP(net.ParseIP(ip)) {
			return false
		}
	}
	return len(ips) > 0
}

// lookupIPs — адреса сайта; в тестах подменяется.
func (s *Server) lookupIPs(ctx context.Context, host string) []string {
	if s.LookupIPs != nil {
		return s.LookupIPs(ctx, host)
	}
	return s.resolve(ctx, host, 0)
}

// BehindCloudflare — сайт сам стоит за Cloudflare: все его адреса принадлежат Cloudflare. Воркер
// к такому не подключится никогда. Адрес ищется с кэшем, так что повторный вопрос ничего не стоит.
func (s *Server) BehindCloudflare(ctx context.Context, host string) bool {
	host = hostKey(host)
	if cant, cloudflare := s.workerCantReach(host); cant && cloudflare {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return IsCloudflareIP(ip)
	}
	if !s.allCloudflare(s.lookupIPs(ctx, host)) {
		return false
	}
	s.noteCantReach(host, behindCFFor, true)
	return true
}

// WorkerCantReach — вести сайт через воркер сейчас бессмысленно: он стоит за Cloudflare или
// воркер только что не смог к нему подключиться.
func (s *Server) WorkerCantReach(ctx context.Context, host string) bool {
	if cant, _ := s.workerCantReach(host); cant {
		return true
	}
	return s.BehindCloudflare(ctx, host)
}

// KnownBehindCloudflare — какие из сайтов, как помнится, стоят за Cloudflare. В сеть не ходит:
// годится для статуса, который окно спрашивает каждые несколько секунд.
func (s *Server) KnownBehindCloudflare(hosts []string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	now := time.Now()
	for _, h := range hosts {
		if u := s.cantReach[hostKey(h)]; u.cloudflare && now.Before(u.until) {
			out = append(out, h)
		}
	}
	return out
}

// directTCP подключается к сайту напрямую, мимо воркера. Адрес ищет прокси Telegram: с кэшем и
// через DNS поверх HTTPS, когда системный DNS сайта не знает.
func (s *Server) directTCP(ctx context.Context, host string, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: dialTimeout}
	portText := strconv.Itoa(port)
	if net.ParseIP(host) != nil {
		return d.DialContext(ctx, "tcp", net.JoinHostPort(host, portText))
	}
	ips := s.lookupIPs(ctx, host)
	if len(ips) == 0 {
		return nil, fmt.Errorf("%s: %w", host, errNoAddress)
	}
	var conn net.Conn
	var err error
	for _, ip := range ips[:min(len(ips), 2)] {
		if conn, err = d.DialContext(ctx, "tcp4", net.JoinHostPort(ip, portText)); err == nil {
			return conn, nil
		}
	}
	return nil, err
}
