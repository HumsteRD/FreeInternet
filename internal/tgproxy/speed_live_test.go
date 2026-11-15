//go:build live

// Скорость загрузки через воркер: сколько мегабит в секунду проходит по пути, которым идут
// свежие ролики YouTube, когда сервер Google закрыт провайдером.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_WORKER="…"; $env:FI_WORKER_KEY="…"; $env:FI_LIVE_EDGES="1"
//	$env:FI_SPEED_URL="https://fsn1-speed.hetzner.com/100MB.bin"
//	go test -tags live -run LiveWorkerSpeed -v ./internal/tgproxy
package tgproxy

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveWorkerSpeed(t *testing.T) {
	bind, key, target := net.ParseIP(os.Getenv("FI_BIND")), os.Getenv("FI_WORKER_KEY"), os.Getenv("FI_SPEED_URL")
	if bind == nil || key == "" || target == "" || os.Getenv("FI_LIVE_EDGES") == "" {
		t.Skip("FI_BIND, FI_WORKER_KEY, FI_SPEED_URL или FI_LIVE_EDGES не задан")
	}
	wsLocalAddr = &net.TCPAddr{IP: bind}
	defer func() { wsLocalAddr = nil }()
	worker := liveWorker(t)
	u, err := url.Parse(target)
	if err != nil {
		t.Fatal(err)
	}

	for _, fast := range []bool{false, true} {
		srv := &Server{Worker: worker, WorkerKey: key}
		srv.edge.host, srv.edge.at = worker, time.Now() // без быстрых входов: только DNS
		// FI_SPEED_PLAIN — простая пересылка в воркере (p=0), как в коде до восьмой версии: так
		// видно, виноват ли в сбое способ пересылки или сам сайт.
		srv.plainPump.Store(os.Getenv("FI_SPEED_PLAIN") != "")
		name := "адреса из DNS"
		if fast {
			srv.edge.list, _ = pickEdges(context.Background(), worker, edgeCandidates)
			name = "быстрые входы"
			t.Logf("входы: %+v", srv.edge.list)
		}
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := l.Addr().String()
		l.Close()
		p := &WebProxy{Addr: addr, Server: srv, ViaWorker: func() []string { return []string{u.Hostname()} }}
		if os.Getenv("FI_SPEED_TRACE") != "" {
			// Счётчики на соединении с воркером: в какую сторону и когда перестали идти данные.
			p.dialWorker = func(ctx context.Context, host string, port int) (net.Conn, error) {
				conn, err := srv.dialWorkerTCP(ctx, host, port)
				if err != nil {
					return nil, err
				}
				return &tracedConn{Conn: conn, t: t, start: time.Now()}, nil
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		go p.ListenAndServe(ctx)
		for !p.Listening() {
			time.Sleep(10 * time.Millisecond)
		}
		proxyURL, _ := url.Parse("http://" + addr)
		client := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
		start := time.Now()
		resp, err := client.Get(target)
		if err != nil {
			cancel()
			t.Fatalf("%s: %v", name, errText(err))
		}
		first := time.Since(start)
		// FI_SPEED_SECONDS секунд или весь файл — что раньше; FI_SPEED_RATE — потолок в МБ/с, чтобы
		// отличить обрыв по объёму от обрыва по времени.
		secs, _ := strconv.Atoi(os.Getenv("FI_SPEED_SECONDS"))
		rate, _ := strconv.ParseFloat(os.Getenv("FI_SPEED_RATE"), 64)
		deadline := time.AfterFunc(time.Duration(max(secs, 4))*time.Second, func() { resp.Body.Close() })
		var n int64
		var copyErr error
		buf := make([]byte, 256<<10)
		lastLog, lastN := time.Now(), int64(0)
		for {
			m, err := resp.Body.Read(buf)
			n += int64(m)
			if os.Getenv("FI_SPEED_TRACE") != "" && time.Since(lastLog) >= time.Second {
				t.Logf("  %5.1f с: %6.1f МБ (+%.1f)", (time.Since(start) - first).Seconds(), float64(n)/1e6, float64(n-lastN)/1e6)
				lastLog, lastN = time.Now(), n
			}
			if err != nil {
				if err != io.EOF {
					copyErr = err
				}
				break
			}
			if rate > 0 {
				if ahead := time.Duration(float64(n)/(rate*1e6)*float64(time.Second)) - (time.Since(start) - first); ahead > 0 {
					time.Sleep(ahead)
				}
			}
		}
		deadline.Stop()
		resp.Body.Close()
		took := time.Since(start) - first
		cancel()
		t.Logf("%s: HTTP %d %s, длина %d, первый байт через %v, %.1f МБ за %v — %.1f Мбит/с, конец: %v",
			name, resp.StatusCode, resp.Proto, resp.ContentLength, first.Round(time.Millisecond), float64(n)/1e6, took.Round(100*time.Millisecond), float64(n)*8/1e6/took.Seconds(), errText(copyErr))
		if os.Getenv("FI_SPEED_ONCE") != "" {
			return
		}
	}
}

// tracedConn считает байты в обе стороны и пишет, чем и когда закончилось чтение и запись.
type tracedConn struct {
	net.Conn
	t          *testing.T
	start      time.Time
	in, out    atomic.Int64
	reads      atomic.Int64
	lastIn     atomic.Int64 // мс от начала
	lastOut    atomic.Int64
	loggedRead atomic.Bool
}

func (c *tracedConn) since() int64 { return time.Since(c.start).Milliseconds() }

func (c *tracedConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.in.Add(int64(n))
	c.reads.Add(1)
	if n > 0 {
		c.lastIn.Store(c.since())
	}
	if err != nil && c.loggedRead.CompareAndSwap(false, true) {
		code, reason := uint16(0), ""
		if ws, ok := c.Conn.(*wsStream); ok {
			code, reason = ws.ws.closeCode, ws.ws.closeReason
		}
		c.t.Logf("  воркер→FI: конец на %d мс: %v (код закрытия %d %q); получено %d байт за %d чтений (последнее на %d мс), отправлено %d байт (последнее на %d мс)",
			c.since(), errText(err), code, reason, c.in.Load(), c.reads.Load(), c.lastIn.Load(), c.out.Load(), c.lastOut.Load())
	}
	return n, err
}

func (c *tracedConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.out.Add(int64(n))
	c.lastOut.Store(c.since())
	if err != nil {
		c.t.Logf("  FI→воркер: ошибка записи на %d мс: %v", c.since(), errText(err))
	}
	return n, err
}

func (c *tracedConn) Close() error {
	c.t.Logf("  соединение с воркером закрыто на %d мс: получено %d, отправлено %d", c.since(), c.in.Load(), c.out.Load())
	return c.Conn.Close()
}
