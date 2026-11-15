package tgproxy

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Расход бесплатного тарифа Cloudflare. У бесплатного воркера два предела, и оба кончаются молча:
//
//   - 100 000 запросов в сутки (сутки по UTC). Каждое соединение FI с воркером — один запрос:
//     соединение Telegram, сайт через воркер, видеосервер YouTube. Исчерпан — Cloudflare отвечает
//     429 «error code: 1027», и до полуночи UTC воркер не работает.
//   - процессорное время. Воркер тратит его на каждый пересланный кусок данных; на долгой
//     быстрой загрузке (видео, файлы) Cloudflare обрывает соединение без объяснений — TCP просто
//     закрывается посреди передачи.
//
// FI считает запросы сам (точного счётчика без ключа доступа у Cloudflare не спросить) и
// замечает оба отказа: окно говорит человеку, что случилось и когда пройдёт, а Telegram на это
// время уходит на запасной путь — человеку не приходится гадать, почему всё встало.

const (
	// WorkerDailyLimit — запросов в сутки на бесплатном тарифе Cloudflare Workers.
	WorkerDailyLimit = 100_000
	// workerSaveShare — после этой доли суточного лимита Telegram идёт через общие домены
	// Cloudflare, если они есть: остаток запросов нужнее сайтам и видео, которым идти больше некуда.
	workerSaveShare = 0.8
	// Обрыв — соединение закрылось без кадра закрытия WebSocket посреди передачи: получено не
	// меньше cutMinBytes, последний кадр пришёл не раньше cutActive назад.
	cutMinBytes = 64 << 10
	cutActive   = 2 * time.Second
	cutWindow   = 10 * time.Minute
)

// errWorkerLimit — суточный лимит запросов воркера исчерпан: до полуночи UTC к нему не ходим.
var errWorkerLimit = errors.New("суточный лимит запросов Cloudflare исчерпан")

// WorkerUsage — расход воркера по счёту FI.
type WorkerUsage struct {
	Day          string    `json:"day"`                    // сутки UTC, за которые считаны запросы: 2026-11-15
	Requests     int       `json:"requests"`               // запросов к воркеру за эти сутки
	LimitedUntil time.Time `json:"limited_until,omitzero"` // лимит исчерпан: воркер заработает в это время
	Cuts         int       `json:"cuts"`                   // обрывов загрузки за последние десять минут
	LastCut      time.Time `json:"last_cut,omitzero"`      // когда был последний
	ResetAt      time.Time `json:"reset_at,omitzero"`      // когда счётчик запросов обнулится
}

type usageState struct {
	mu           sync.Mutex
	day          string
	requests     int
	limitedUntil time.Time
	cuts         []time.Time
}

// usageNow — часы; в тестах подменяются.
var usageNow = time.Now

func utcDay(t time.Time) string { return t.UTC().Format("2006-01-02") }

// nextUTCMidnight — когда Cloudflare обнулит суточный счётчик.
func nextUTCMidnight(t time.Time) time.Time {
	u := t.UTC()
	return time.Date(u.Year(), u.Month(), u.Day()+1, 0, 0, 0, 0, time.UTC)
}

// rollLocked обнуляет счётчик, когда по UTC начались новые сутки.
func (u *usageState) rollLocked(now time.Time) {
	if day := utcDay(now); day != u.day {
		u.day, u.requests = day, 0
	}
	if !u.limitedUntil.IsZero() && !now.Before(u.limitedUntil) {
		u.limitedUntil = time.Time{}
	}
}

// noteRequests считает запросы к воркеру: Cloudflare засчитывает каждый, на который ответил.
func (s *Server) noteRequests(n int) {
	u := &s.usage
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked(usageNow())
	u.requests += n
}

// noteWorkerDial разбирает итог обращения к воркеру: считает запрос и замечает исчерпанный лимит.
// true — лимит исчерпан.
func (s *Server) noteWorkerDial(err error) bool {
	var se *statusError
	if err != nil && !errors.As(err, &se) {
		return false // до Cloudflare запрос не дошёл: соединение не открылось
	}
	s.noteRequests(1)
	if se == nil || (se.code != http.StatusTooManyRequests && se.cfCode() != 1027) {
		return false
	}
	now := usageNow()
	u := &s.usage
	u.mu.Lock()
	first := u.limitedUntil.IsZero()
	u.limitedUntil = nextUTCMidnight(now).Add(time.Minute)
	until := u.limitedUntil
	u.mu.Unlock()
	if first {
		if p := s.currentPool(); p != nil {
			p.flush() // в пуле — соединения через воркер, новых до полуночи не будет
		}
		if s.Log != nil {
			s.Log.Warn("суточный лимит запросов Cloudflare исчерпан — до полуночи UTC воркер не работает, Telegram идёт запасным путём",
				"worker", s.Worker, "until", until.Local().Format("15:04"))
		}
	}
	return true
}

// workerLimited — лимит запросов исчерпан: к воркеру сейчас не ходим.
func (s *Server) workerLimited() bool {
	u := &s.usage
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked(usageNow())
	return !u.limitedUntil.IsZero()
}

// workerSaving — запросов осталось мало: Telegram стоит увести на общие домены Cloudflare.
func (s *Server) workerSaving() bool {
	u := &s.usage
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked(usageNow())
	return float64(u.requests) >= workerSaveShare*WorkerDailyLimit
}

// noteCut запоминает обрыв загрузки через воркер.
func (s *Server) noteCut() {
	now := usageNow()
	u := &s.usage
	u.mu.Lock()
	u.cuts = append(recentCuts(u.cuts, now), now)
	n := len(u.cuts)
	u.mu.Unlock()
	if s.Log != nil && (n == 3 || n%20 == 0) { // не каждый: при перегрузе их десятки в минуту
		s.Log.Warn("Cloudflare обрывает загрузки через воркер: на бесплатном тарифе ему не хватает процессорного времени на долгую быструю передачу",
			"worker", s.Worker, "обрывов_за_10_минут", n)
	}
}

func recentCuts(cuts []time.Time, now time.Time) []time.Time {
	i := 0
	for i < len(cuts) && now.Sub(cuts[i]) >= cutWindow {
		i++
	}
	return cuts[i:]
}

// Usage — расход воркера по счёту FI на сейчас.
func (s *Server) Usage() WorkerUsage {
	now := usageNow()
	u := &s.usage
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked(now)
	u.cuts = recentCuts(u.cuts, now)
	out := WorkerUsage{Day: u.day, Requests: u.requests, LimitedUntil: u.limitedUntil, Cuts: len(u.cuts), ResetAt: nextUTCMidnight(now)}
	if len(u.cuts) > 0 {
		out.LastCut = u.cuts[len(u.cuts)-1]
	}
	return out
}

// SeedUsage продолжает счёт после перезапуска прокси или службы: Cloudflare считает сутки
// по своим часам, а не от запуска FI.
func (s *Server) SeedUsage(prev WorkerUsage) {
	now := usageNow()
	u := &s.usage
	u.mu.Lock()
	defer u.mu.Unlock()
	u.rollLocked(now)
	if prev.Day == u.day && prev.Requests > u.requests {
		u.requests = prev.Requests
	}
	if prev.LimitedUntil.After(now) {
		u.limitedUntil = prev.LimitedUntil
	}
}

func (s *Server) currentPool() *pool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pool
}

// limitError — понятный текст про исчерпанный лимит, со временем, когда он обнулится.
func (s *Server) limitError() error {
	until := s.Usage().LimitedUntil
	if until.IsZero() {
		until = nextUTCMidnight(usageNow())
	}
	return fmt.Errorf("воркер %s: %w — он снова заработает в %s", s.Worker, errWorkerLimit, until.Local().Format("15:04"))
}
