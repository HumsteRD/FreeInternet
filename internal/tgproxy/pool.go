package tgproxy

import (
	"context"
	"sync"
	"time"
)

const (
	defaultPoolSize = 2
	// Простаивающее основное соединение сервер Telegram закрывает через 60–100 с, а соединение
	// для файлов держит дольше 6 минут (замер 2026-09-15) — меняем их заранее.
	poolMaxAge      = 45 * time.Second
	poolMaxAgeMedia = 5 * time.Minute
	poolWarmFor     = 10 * time.Minute // пул дата-центра пополняется, пока Telegram к нему обращается
	poolTick        = 15 * time.Second
)

// poolKey — куда ведёт соединение.
type poolKey struct {
	dc          int
	media, test bool
}

type idleConn struct {
	up upstream
	at time.Time
}

type poolEntry struct {
	idle    []idleConn
	used    time.Time
	filling bool
}

// pool держит заранее открытые WebSocket-соединения к дата-центрам, которыми Telegram
// пользовался недавно. TLS и переход на WebSocket занимают сотни миллисекунд, а Telegram
// открывает соединение на каждую загрузку файла — с готовым соединением она начинается сразу.
type pool struct {
	size                int
	maxAge, maxAgeMedia time.Duration
	warmFor             time.Duration
	dial                func(ctx context.Context, k poolKey) (upstream, error)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	entries map[poolKey]*poolEntry
	closed  bool
}

func newPool(size int, dial func(context.Context, poolKey) (upstream, error)) *pool {
	ctx, cancel := context.WithCancel(context.Background())
	p := &pool{
		size:        size,
		maxAge:      poolMaxAge,
		maxAgeMedia: poolMaxAgeMedia,
		warmFor:     poolWarmFor,
		dial:        dial,
		ctx:         ctx,
		cancel:      cancel,
		entries:     map[poolKey]*poolEntry{},
	}
	p.wg.Add(1)
	go p.run()
	return p
}

func (p *pool) run() {
	defer p.wg.Done()
	tick := time.NewTicker(poolTick)
	defer tick.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-tick.C:
			p.sweep()
		}
	}
}

// take отдаёт готовое соединение, если оно свежее и сервер его не закрыл.
func (p *pool) take(k poolKey) upstream {
	for {
		p.mu.Lock()
		e := p.entries[k]
		if e == nil || len(e.idle) == 0 {
			p.mu.Unlock()
			return nil
		}
		c := e.idle[len(e.idle)-1]
		e.idle = e.idle[:len(e.idle)-1]
		p.mu.Unlock()

		if p.fresh(k, c) && alive(c.up) {
			return c.up
		}
		c.up.close()
	}
}

func (p *pool) fresh(k poolKey, c idleConn) bool {
	limit := p.maxAge
	if k.media {
		limit = p.maxAgeMedia
	}
	return time.Since(c.at) < limit
}

func alive(up upstream) bool {
	a, ok := up.(interface{ alive() bool })
	return !ok || a.alive()
}

// use отмечает обращение Telegram к дата-центру и пополняет его пул в фоне.
func (p *pool) use(k poolKey) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	e := p.entries[k]
	if e == nil {
		e = &poolEntry{}
		p.entries[k] = e
	}
	e.used = time.Now()
	p.fillLocked(k, e)
}

func (p *pool) fillLocked(k poolKey, e *poolEntry) {
	if e.filling || len(e.idle) >= p.size {
		return
	}
	e.filling = true
	p.wg.Add(1)
	go p.fill(k, e)
}

// fill открывает соединения, пока пул не наполнится. После ошибки ждёт следующего обхода:
// неудачные адреса и так на паузе, а долбить недоступный сервер незачем.
func (p *pool) fill(k poolKey, e *poolEntry) {
	defer p.wg.Done()
	for {
		p.mu.Lock()
		if p.closed || p.entries[k] != e || len(e.idle) >= p.size {
			e.filling = false
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

		up, err := p.dial(p.ctx, k)

		p.mu.Lock()
		if err != nil || p.closed || p.entries[k] != e {
			e.filling = false
			p.mu.Unlock()
			if err == nil {
				up.close()
			}
			return
		}
		e.idle = append(e.idle, idleConn{up: up, at: time.Now()})
		p.mu.Unlock()
	}
}

// sweep меняет устаревшие и закрытые сервером соединения, забывает дата-центры,
// к которым давно не обращались, и пополняет остальные.
func (p *pool) sweep() {
	p.mu.Lock()
	checking := map[poolKey][]idleConn{}
	var drop []upstream
	for k, e := range p.entries {
		if time.Since(e.used) >= p.warmFor {
			for _, c := range e.idle {
				drop = append(drop, c.up)
			}
			delete(p.entries, k)
			continue
		}
		checking[k], e.idle = e.idle, nil
	}
	p.mu.Unlock()

	// Проверка чтением идёт без блокировки: пока она длится, take этих соединений не видит.
	keep := map[poolKey][]idleConn{}
	for k, conns := range checking {
		for _, c := range conns {
			if p.fresh(k, c) && alive(c.up) {
				keep[k] = append(keep[k], c)
			} else {
				drop = append(drop, c.up)
			}
		}
	}

	p.mu.Lock()
	for k := range checking {
		e := p.entries[k]
		if e == nil || p.closed {
			for _, c := range keep[k] {
				drop = append(drop, c.up)
			}
			continue
		}
		e.idle = append(keep[k], e.idle...)
		if extra := len(e.idle) - p.size; extra > 0 { // пока шла проверка, fill успел дооткрыть
			for _, c := range e.idle[:extra] {
				drop = append(drop, c.up)
			}
			e.idle = e.idle[extra:]
		}
		p.fillLocked(k, e)
	}
	p.mu.Unlock()

	for _, up := range drop {
		up.close()
	}
}

// ready — сколько готовых соединений сейчас в пуле.
func (p *pool) ready() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, e := range p.entries {
		n += len(e.idle)
	}
	return n
}

// close закрывает пул и все готовые соединения.
func (p *pool) close() {
	p.mu.Lock()
	p.closed = true
	var idle []upstream
	for k, e := range p.entries {
		for _, c := range e.idle {
			idle = append(idle, c.up)
		}
		delete(p.entries, k)
	}
	p.mu.Unlock()

	p.cancel()
	p.wg.Wait()
	for _, up := range idle {
		up.close()
	}
}
