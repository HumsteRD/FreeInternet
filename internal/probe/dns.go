package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

// DoH-серверы указаны по IP, чтобы проверка не зависела от системного DNS.
var dohEndpoints = []string{
	"https://1.1.1.1/dns-query",
	"https://8.8.8.8/resolve",
}

// resolve возвращает IPv4 цели: сначала системным резолвером, при неудаче — через DoH.
// Подмену на публичный адрес-заглушку так не поймать — её выдаст сертификат (TLS_CERT).
func (p *Prober) resolve(ctx context.Context, host string) (ip string, st Status, detail string) {
	sysCtx, cancel := context.WithTimeout(ctx, p.ConnectTimeout)
	sys, sysErr := net.DefaultResolver.LookupIP(sysCtx, "ip4", host)
	cancel()
	if ip := firstPublic(sys); ip != "" {
		return ip, OK, ""
	}

	dohCtx, cancel := context.WithTimeout(ctx, p.ConnectTimeout)
	defer cancel()
	dohIP, dohErr := lookupDoH(dohCtx, host)
	return classifyDNS(sys, sysErr, dohIP, dohErr)
}

// classifyDNS решает, подменяет ли системный DNS адрес. Подмена — только настоящий ответ
// резолвера: «такого хоста нет» или адрес-заглушка, когда DoH знает настоящий адрес.
// Медленный или пустой ответ системного DNS подменой не считается: проверка идёт дальше
// по адресу из DoH, иначе случайная задержка DNS выглядела бы как блокировка.
func classifyDNS(sys []net.IP, sysErr error, dohIP string, dohErr error) (string, Status, string) {
	var dnsErr *net.DNSError
	notFound := errors.As(sysErr, &dnsErr) && dnsErr.IsNotFound
	switch {
	case dohErr != nil && (sysErr != nil || len(sys) == 0):
		return "", DNSFail, fmt.Sprintf("системный DNS: %v; DoH: %v", sysErr, dohErr)
	case dohErr != nil:
		return "", DNSSpoof, fmt.Sprintf("системный DNS вернул %v, DoH недоступен", sys)
	case notFound:
		return dohIP, DNSSpoof, "системный DNS не знает хост, DoH знает"
	case sysErr != nil || len(sys) == 0:
		return dohIP, OK, "системный DNS не ответил вовремя, проверено по адресу из DoH"
	default:
		return dohIP, DNSSpoof, fmt.Sprintf("системный DNS вернул %v", sys)
	}
}

func firstPublic(ips []net.IP) string {
	for _, ip := range ips {
		if ip.To4() == nil || ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			continue
		}
		return ip.String()
	}
	return ""
}

type dohAnswer struct {
	Status int `json:"Status"`
	Answer []struct {
		Type int    `json:"type"`
		Data string `json:"data"`
	} `json:"Answer"`
}

// LookupDoH — IPv4-адрес хоста через DNS поверх HTTPS, мимо системного DNS.
func LookupDoH(ctx context.Context, host string) (string, error) { return lookupDoH(ctx, host) }

// dohWait — сколько ждать DoH, когда системный DNS хоста не нашёл.
const dohWait = 5 * time.Second

// Подмены для тестов.
var (
	systemDial = func(ctx context.Context, network, addr string) (net.Conn, error) {
		d := net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
		return d.DialContext(ctx, network, addr)
	}
	dohLookup = lookupDoH
)

// DialContext подключается к addr; если системный DNS не знает хост, адрес ищется через DoH.
// Провайдер подменяет ответы обычного DNS («такого сайта нет» — у одного провайдера так было
// с www.youtube.com, raw.githubusercontent.com и workers.dev), и запросы самой службы без этого
// не доходили: не определялся запасной видеосервер YouTube, не обновлялся список доменов
// Cloudflare, воркер считался пропавшим. Сертификат всё равно проверяется по имени сайта.
func DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	conn, err := systemDial(ctx, network, addr)
	var dnsErr *net.DNSError
	if err == nil || !errors.As(err, &dnsErr) {
		return conn, err
	}
	host, port, splitErr := net.SplitHostPort(addr)
	if splitErr != nil || net.ParseIP(host) != nil {
		return nil, err
	}
	dohCtx, cancel := context.WithTimeout(ctx, dohWait)
	ip, dohErr := dohLookup(dohCtx, host)
	cancel()
	if dohErr != nil {
		return nil, err
	}
	return systemDial(ctx, network, net.JoinHostPort(ip, port))
}

// Transport — транспорт HTTP для запросов самой службы: как обычный, но с DialContext.
func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.DialContext = DialContext
	return t
}

func lookupDoH(ctx context.Context, host string) (string, error) {
	var errs []error
	for _, endpoint := range dohEndpoints {
		ip, err := queryDoH(ctx, endpoint, host)
		if err == nil {
			return ip, nil
		}
		errs = append(errs, err)
	}
	return "", errors.Join(errs...)
}

func queryDoH(ctx context.Context, endpoint, host string) (string, error) {
	u := endpoint + "?type=A&name=" + url.QueryEscape(host)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/dns-json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var ans dohAnswer
	if err := json.NewDecoder(resp.Body).Decode(&ans); err != nil {
		return "", fmt.Errorf("%s: %w", endpoint, err)
	}
	for _, a := range ans.Answer {
		if a.Type != 1 { // A
			continue
		}
		if ip := firstPublic([]net.IP{net.ParseIP(a.Data)}); ip != "" {
			return ip, nil
		}
	}
	return "", fmt.Errorf("%s: нет A-записей (status %d)", endpoint, ans.Status)
}
