package tgproxy

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeUp — соединение, которое тест может «закрыть со стороны сервера».
type fakeUp struct {
	dead   atomic.Bool
	closed atomic.Bool
}

func (f *fakeUp) framed() bool          { return true }
func (f *fakeUp) send([]byte) error     { return nil }
func (f *fakeUp) recv() ([]byte, error) { return nil, io.EOF }
func (f *fakeUp) close()                { f.closed.Store(true) }
func (f *fakeUp) alive() bool           { return !f.dead.Load() }

type fakeDialer struct {
	mu     sync.Mutex
	conns  []*fakeUp
	failed bool
}

func (d *fakeDialer) dial(context.Context, poolKey) (upstream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.failed {
		return nil, errors.New("нет сети")
	}
	u := &fakeUp{}
	d.conns = append(d.conns, u)
	return u, nil
}

func (d *fakeDialer) all() []*fakeUp {
	d.mu.Lock()
	defer d.mu.Unlock()
	return append([]*fakeUp(nil), d.conns...)
}

func (d *fakeDialer) setFailed(v bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.failed = v
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("не дождались: %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitClosed(t *testing.T, conns ...*fakeUp) {
	t.Helper()
	waitFor(t, "соединение закрыто", func() bool {
		for _, c := range conns {
			if !c.closed.Load() {
				return false
			}
		}
		return true
	})
}

func idleOf(p *pool, k poolKey) []*fakeUp {
	p.mu.Lock()
	defer p.mu.Unlock()
	var conns []*fakeUp
	if e := p.entries[k]; e != nil {
		for _, c := range e.idle {
			conns = append(conns, c.up.(*fakeUp))
		}
	}
	return conns
}

func TestPool(t *testing.T) {
	d := &fakeDialer{}
	p := newPool(2, d.dial)
	defer p.close()
	k := poolKey{dc: 2, media: true}
	full := func() bool { return p.ready() == 2 }

	if p.take(k) != nil {
		t.Fatal("пустой пул выдал соединение")
	}
	p.use(k)
	waitFor(t, "пул наполнился", full)

	if p.take(k) == nil {
		t.Fatal("готовое соединение не выдано")
	}
	if p.take(poolKey{dc: 4}) != nil {
		t.Fatal("выдано соединение к другому дата-центру")
	}
	p.use(k)
	waitFor(t, "пул пополнился после выдачи", full)
	if n := len(d.all()); n != 3 {
		t.Fatalf("открыто %d соединений, ждали 3", n)
	}

	// Закрытое сервером соединение не выдаётся, а закрывается.
	dead := idleOf(p, k)
	for _, c := range dead {
		c.dead.Store(true)
	}
	if p.take(k) != nil {
		t.Fatal("выдано соединение, закрытое сервером")
	}
	waitClosed(t, dead...)

	// Обход меняет устаревшие и закрытые сервером соединения на новые.
	p.use(k)
	waitFor(t, "пул наполнился", full)
	old := idleOf(p, k)
	p.mu.Lock()
	p.entries[k].idle[0].at = time.Now().Add(-p.maxAgeMedia)
	p.mu.Unlock()
	old[1].dead.Store(true)
	p.sweep()
	waitClosed(t, old...)
	waitFor(t, "пул снова полон", full)

	// При ошибке подключения пул не крутится впустую, а ждёт следующего обхода.
	d.setFailed(true)
	p.take(k)
	before := len(d.all())
	p.use(k)
	time.Sleep(50 * time.Millisecond)
	if p.ready() != 1 || len(d.all()) != before {
		t.Fatalf("после ошибки: готово %d, открыто %d (было %d)", p.ready(), len(d.all()), before)
	}

	// Дата-центр, к которому давно не обращались, забывается вместе с соединениями.
	last := idleOf(p, k)
	p.mu.Lock()
	p.entries[k].used = time.Now().Add(-p.warmFor)
	p.mu.Unlock()
	p.sweep()
	if p.ready() != 0 {
		t.Fatalf("после простоя осталось %d соединений", p.ready())
	}
	waitClosed(t, last...)
}

func TestPoolClose(t *testing.T) {
	d := &fakeDialer{}
	p := newPool(3, d.dial)
	k := poolKey{dc: 1}
	p.use(k)
	waitFor(t, "пул наполнился", func() bool { return p.ready() == 3 })
	conns := idleOf(p, k)
	p.close()
	waitClosed(t, conns...)
	p.use(k)
	if p.take(k) != nil || p.ready() != 0 {
		t.Fatal("закрытый пул продолжает работать")
	}
}

// TestProxyUsesPool: второе подключение к дата-центру берёт готовое соединение из пула,
// а остановка прокси закрывает всё.
func TestProxyUsesPool(t *testing.T) {
	d := &fakeDialer{}
	srv := &Server{Addr: "127.0.0.1:0", Secret: make([]byte, 16), wsUpstream: d.dial}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx) }()
	waitFor(t, "прокси запущен", func() bool { return srv.Stats().Listening })

	for i := range 2 {
		up, err := srv.connect(ctx, 2, false, false)
		if err != nil {
			t.Fatal(err)
		}
		up.close()
		if i == 0 {
			waitFor(t, "пул наполнился", func() bool { return srv.Stats().Ready == defaultPoolSize })
		}
	}
	if st := srv.Stats(); st.WebSocket != 2 || st.FromPool != 1 {
		t.Fatalf("статистика %+v", st)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if st := srv.Stats(); st.Listening || st.Ready != 0 {
		t.Fatalf("после остановки: %+v", st)
	}
	waitClosed(t, d.all()...)
}

// TestWSAlive: простаивающее соединение проходит проверку, а закрытое сервером — нет.
func TestWSAlive(t *testing.T) {
	serverSide, clientSide := net.Pipe()
	defer serverSide.Close()
	ws := &wsConn{conn: clientSide, br: bufio.NewReader(clientSide)}
	if !ws.alive() {
		t.Fatal("простаивающее соединение сочтено закрытым")
	}
	go serverSide.Write([]byte{0x88, 0}) // сервер прислал закрытие
	waitFor(t, "данные от сервера замечены", func() bool { return !ws.alive() })

	a, b := net.Pipe()
	ws = &wsConn{conn: a, br: bufio.NewReader(a)}
	b.Close()
	if ws.alive() {
		t.Fatal("закрытое соединение сочтено живым")
	}
}
