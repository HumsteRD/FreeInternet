package tgproxy

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Программы через Cloudflare. Сайты через воркер FI ведёт по списку, а программе список не
// задашь: она ходит на свои адреса, и какие из них закрыты для России, заранее не известно.
// Поэтому программу FI запускает с указанием прокси, и всё, что она через него открывает, идёт
// через воркер — с не российского IP. Работает с программами, которые умеют ходить через прокси:
// браузеры и всё на Chromium и Electron (Discord, лаунчеры), утилиты, читающие HTTPS_PROXY.
// Игры и звонки ходят по UDP мимо прокси — их так не провести.

// AppProxy — локальный HTTP-прокси, который ведёт через воркер любые соединения.
type AppProxy struct {
	Addr   string
	Server *Server

	// dial подменяется в тестах.
	dial func(ctx context.Context, host string, port int) (net.Conn, error)

	mu        sync.Mutex
	listening bool
	stats     AppProxyStats
}

// AppProxyStats — что прошло через прокси программ с его запуска.
type AppProxyStats struct {
	Listening bool      `json:"listening"`
	Conns     int       `json:"conns"`                // открытых соединений всего
	Direct    int       `json:"direct,omitempty"`     // из них мимо воркера: сайт стоит за Cloudflare
	Failed    int       `json:"failed"`               // не открывшихся
	LastUsed  time.Time `json:"last_used,omitzero"`   // когда программа обращалась последний раз
	LastError string    `json:"last_error,omitempty"` // почему не открылось последнее
}

// Stats — снимок счётчиков.
func (p *AppProxy) Stats() AppProxyStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.stats
	st.Listening = p.listening
	return st
}

// ListenAndServe принимает соединения программ, пока не отменён ctx.
func (p *AppProxy) ListenAndServe(ctx context.Context) error {
	l, err := net.Listen("tcp", p.Addr)
	if err != nil {
		return fmt.Errorf("адрес %s занят: %w", p.Addr, err)
	}
	srv := &http.Server{Handler: p, ReadHeaderTimeout: 10 * time.Second}
	stop := context.AfterFunc(ctx, func() { srv.Close() })
	defer stop()
	p.setListening(true)
	defer p.setListening(false)
	if err := srv.Serve(l); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (p *AppProxy) setListening(on bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listening = on
}

func (p *AppProxy) note(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.stats.LastUsed = time.Now()
	if err != nil {
		p.stats.Failed++
		p.stats.LastError = err.Error()
		return
	}
	p.stats.Conns++
}

func (p *AppProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect:
		p.connect(w, r)
	case r.URL.IsAbs() && r.URL.Scheme == "http":
		p.forward(w, r)
	default:
		http.Error(w, "FI: это прокси для программ — нужен запрос CONNECT или адрес http://", http.StatusBadRequest)
	}
}

// connect открывает туннель до host:port — так программы ходят по HTTPS.
func (p *AppProxy) connect(w http.ResponseWriter, r *http.Request) {
	host, portText, err := net.SplitHostPort(r.Host)
	port, perr := strconv.Atoi(portText)
	if err != nil || perr != nil || port < 1 || port > 65535 {
		http.Error(w, "FI: непонятный адрес "+r.Host, http.StatusBadRequest)
		return
	}
	upstream, err := p.open(r.Context(), host, port)
	p.noteUnlessCanceled(r, err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	client, buf, err := hijack(w)
	if err != nil {
		return
	}
	defer client.Close()
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		return
	}
	relay(client, buf, upstream)
}

// forward пересылает обычный запрос http:// — так ходят проверки обновлений и старые программы.
// Ответив, соединение закрывается: следующий запрос программы может быть уже к другому сайту.
func (p *AppProxy) forward(w http.ResponseWriter, r *http.Request) {
	host, port := r.URL.Hostname(), 80
	if text := r.URL.Port(); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > 65535 {
			http.Error(w, "FI: непонятный адрес "+r.URL.Host, http.StatusBadRequest)
			return
		}
		port = n
	}
	upstream, err := p.open(r.Context(), host, port)
	p.noteUnlessCanceled(r, err)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer upstream.Close()
	for _, h := range []string{"Proxy-Connection", "Proxy-Authorization", "Proxy-Authenticate"} {
		r.Header.Del(h)
	}
	r.Header.Set("Connection", "close")
	r.Close = true
	if err := r.Write(upstream); err != nil { // пишет запрос в виде «GET /путь» с заголовком Host
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(upstream), r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for name, values := range resp.Header {
		for _, v := range values {
			w.Header().Add(name, v)
		}
	}
	w.Header().Set("Connection", "close")
	w.WriteHeader(resp.StatusCode)
	// Ответ отдаётся по мере прихода: это может быть долгая загрузка или поток событий.
	flusher := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}

func (p *AppProxy) noteUnlessCanceled(r *http.Request, err error) {
	if err != nil && r.Context().Err() != nil {
		return // программа сама закрыла соединение — это не сбой
	}
	p.note(err)
}

// open подключается к адресу: свой компьютер и домашняя сеть — напрямую (воркеру до них не
// достать), всё остальное — через воркер. Сайт, который сам стоит за Cloudflare, воркер открыть
// не может: к нему программа идёт напрямую, через обход, — иначе она бы просто не работала.
func (p *AppProxy) open(ctx context.Context, host string, port int) (net.Conn, error) {
	if localTarget(host) {
		d := net.Dialer{Timeout: dialTimeout}
		return d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	}
	if p.dial != nil {
		return p.dial(ctx, host, port)
	}
	if p.Server == nil || p.Server.Worker == "" {
		return nil, errNoWorker
	}
	if p.Server.BehindCloudflare(ctx, host) {
		return p.direct(ctx, host, port)
	}
	conn, err := p.Server.dialWorkerTCP(ctx, host, port)
	if errors.Is(err, ErrBehindCloudflare) {
		return p.direct(ctx, host, port)
	}
	return conn, err
}

// direct ведёт соединение программы мимо воркера и считает такие соединения.
func (p *AppProxy) direct(ctx context.Context, host string, port int) (net.Conn, error) {
	conn, err := p.Server.directTCP(ctx, host, port)
	if err == nil {
		p.mu.Lock()
		p.stats.Direct++
		p.mu.Unlock()
	}
	return conn, err
}

// localTarget — адрес этого компьютера или домашней сети.
func localTarget(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified())
}

func hijack(w http.ResponseWriter) (net.Conn, *bufio.Reader, error) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "нет перехвата соединения", http.StatusInternalServerError)
		return nil, nil, errors.New("нет перехвата соединения")
	}
	conn, buf, err := hj.Hijack()
	if err != nil {
		return nil, nil, err
	}
	return conn, buf.Reader, nil
}
