package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
