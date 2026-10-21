package probe

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"strconv"
	"sync"
	"time"
)

// Prober выполняет проверки. Нулевые поля заменяются значениями из New.
type Prober struct {
	ConnectTimeout time.Duration // резолв, TCP, рукопожатие, ожидание ответа STUN
	StallTimeout   time.Duration // сколько ждать следующих байт, прежде чем считать передачу замершей
	MaxBytes       int64         // сколько читать тела страницы
	SlowKBps       float64       // ниже этой скорости передача считается замедленной
	Parallel       int
	// Retries — повторы после сбоя, который бывает и от случайных потерь:
	// один таймаут не должен решать судьбу стратегии.
	Retries int

	// Подмены для тестов.
	lookup  func(ctx context.Context, host string) (ip string, st Status, detail string)
	port    string
	rootCAs *x509.CertPool
}

func New() *Prober {
	return &Prober{
		ConnectTimeout: 5 * time.Second,
		StallTimeout:   5 * time.Second,
		MaxBytes:       256 << 10,
		SlowKBps:       100,
		Parallel:       16, // цели — разные серверы; больше параллельных проверок — быстрее подбор
		Retries:        1,
	}
}

func (p *Prober) portFor(t Target, def int) string {
	switch {
	case p.port != "":
		return p.port
	case t.Port != 0:
		return strconv.Itoa(t.Port)
	default:
		return strconv.Itoa(def)
	}
}

// Run проверяет цели параллельно; порядок результатов совпадает с порядком целей.
func (p *Prober) Run(ctx context.Context, targets []Target) []Result {
	results := make([]Result, len(targets))
	sem := make(chan struct{}, max(1, p.Parallel))
	var wg sync.WaitGroup
	for i, t := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = p.Check(ctx, t)
		}()
	}
	wg.Wait()
	return results
}

// Check проверяет одну цель, повторяя попытку после сбоев, которые бывают случайными.
func (p *Prober) Check(ctx context.Context, t Target) Result {
	start := time.Now()
	var r Result
	for attempt := 0; attempt <= p.Retries; attempt++ {
		r = p.attempt(ctx, t)
		if !retryable(r.Status) || ctx.Err() != nil {
			break
		}
	}
	r.Target = t
	r.Duration = time.Since(start)
	return r
}

func (p *Prober) attempt(ctx context.Context, t Target) Result {
	lookup := p.lookup
	if lookup == nil {
		lookup = p.resolve
	}
	var r Result
	ip, dnsStatus, dnsDetail := lookup(ctx, t.Host)
	if ip == "" {
		r.Status, r.Detail = dnsStatus, dnsDetail
		return r
	}

	r.IP = ip
	// Адрес нашёлся только через DoH: проверка идёт дальше, но браузер, который спрашивает
	// системный DNS, этот сайт не откроет — окно должно об этом сказать.
	r.SysDNSFailed = dnsStatus == OK && dnsDetail != ""
	switch t.Kind {
	case KindQUIC:
		p.checkQUIC(ctx, t, &r)
	case KindSTUN:
		p.checkSTUN(ctx, t, &r)
	default:
		p.checkTLS(ctx, t, &r)
	}
	if t.Neutral != "" && (r.Status == TLSTimeout || r.Status == TLSReset) && p.addressClosed(ctx, t, ip) {
		r.Status, r.Detail = IPBlocked, "сервер молчит и для "+t.Neutral+": "+r.Detail
	}
	// Подмену DNS показываем в первую очередь: приложения пользователя пойдут
	// на адрес-заглушку, даже если по настоящему адресу всё работает.
	if dnsStatus == DNSSpoof {
		r.Detail = dnsDetail + "; по адресу из DoH: " + string(r.Status) + " " + r.Detail
		r.Status = DNSSpoof
	}
	return r
}

// addressClosed — сервер не отвечает и на рукопожатие с именем t.Neutral, которого нет в списках
// блокировок. Чужой сертификат в ответ — значит, сервер отвечает, и молчал он из-за имени цели.
func (p *Prober) addressClosed(ctx context.Context, t Target, ip string) bool {
	conn, err := p.dialTLS(ctx, t.Neutral, net.JoinHostPort(ip, p.portFor(t, 443)))
	if err == nil {
		conn.Close()
		return false
	}
	var se *stageError
	return errors.As(err, &se) && (se.status == TCPFail || se.status == TLSTimeout)
}

// retryable — сбои, которые случаются и без блокировки, от потерь в сети.
func retryable(s Status) bool {
	switch s {
	case TCPFail, TLSReset, TLSTimeout, Freeze16K, ConnReset, QUICFail, UDPFail, Failed:
		return true
	}
	return false
}
