package probe

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Тесты эмулируют поведение DPI локальными серверами и проверяют классификацию.

func testProber(t *testing.T, addr string, pool *x509.CertPool) *Prober {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ConnectTimeout = time.Second
	p.StallTimeout = 300 * time.Millisecond
	p.SlowKBps = 0
	p.Retries = 0
	p.port = port
	p.rootCAs = pool
	p.lookup = func(context.Context, string) (string, Status, string) { return host, OK, "" }
	return p
}

// tlsServer поднимает HTTPS-сервер с сертификатом на example.com.
func tlsServer(t *testing.T, h http.HandlerFunc) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.StartTLS()
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool
}

// hang блокирует обработчик, пока клиент не отключится или тест не закончится.
// Одного r.Context() мало: пока тело POST не дочитано, сервер не замечает отключения
// клиента, и httptest.Server.Close ждал бы вечно.
func hang(t *testing.T, r *http.Request) {
	select {
	case <-r.Context().Done():
	case <-t.Context().Done():
	}
}

// rawServer принимает TCP и передаёт соединение обработчику — для сломанных рукопожатий.
func rawServer(t *testing.T, handle func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
			go handle(c)
		}
	}()
	return ln.Addr().String()
}

func check(t *testing.T, p *Prober, kind Kind) Result {
	t.Helper()
	return p.Check(context.Background(), Target{Name: "test", Kind: kind, Host: "example.com", Weight: 1})
}

func TestPageOK(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 300<<10)
	srv, pool := tlsServer(t, func(w http.ResponseWriter, r *http.Request) { w.Write(body) })
	p := testProber(t, srv.Listener.Addr().String(), pool)

	r := check(t, p, KindPage)
	if r.Status != OK || r.Bytes < p.MaxBytes || r.HTTPCode != http.StatusOK {
		t.Fatalf("status=%s bytes=%d code=%d detail=%s", r.Status, r.Bytes, r.HTTPCode, r.Detail)
	}
}

func TestPageFreeze16K(t *testing.T) {
	srv, pool := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(300<<10))
		w.Write(make([]byte, 16<<10))
		w.(http.Flusher).Flush()
		hang(t, r) // остальное «застряло» у DPI
	})
	p := testProber(t, srv.Listener.Addr().String(), pool)

	if r := check(t, p, KindPage); r.Status != Freeze16K {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, Freeze16K)
	}
}

func TestPageSlow(t *testing.T) {
	srv, pool := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(128<<10))
		for range 16 { // 16 × 8 КБ за ~800 мс ≈ 160 КБ/с
			w.Write(make([]byte, 8<<10))
			w.(http.Flusher).Flush()
			time.Sleep(50 * time.Millisecond)
		}
	})
	p := testProber(t, srv.Listener.Addr().String(), pool)
	p.SlowKBps = 1000

	if r := check(t, p, KindPage); r.Status != Slow {
		t.Fatalf("status=%s kbps=%.0f detail=%s, want %s", r.Status, r.KBps, r.Detail, Slow)
	}
}

func TestTLSReset(t *testing.T) {
	addr := rawServer(t, func(c net.Conn) {
		c.Read(make([]byte, 4096)) // получили ClientHello — и оборвали, как DPI по SNI
		c.Close()
	})
	p := testProber(t, addr, nil)

	if r := check(t, p, KindTLS); r.Status != TLSReset {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, TLSReset)
	}
}

func TestTLSTimeout(t *testing.T) {
	addr := rawServer(t, func(c net.Conn) { io.Copy(io.Discard, c) }) // молча глотаем ClientHello
	p := testProber(t, addr, nil)

	if r := check(t, p, KindTLS); r.Status != TLSTimeout {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, TLSTimeout)
	}
}

func TestTLSCert(t *testing.T) {
	srv, _ := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {})
	p := testProber(t, srv.Listener.Addr().String(), x509.NewCertPool()) // сертификат сервера не доверен

	if r := check(t, p, KindTLS); r.Status != TLSCert {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, TLSCert)
	}
}

func TestRetries(t *testing.T) {
	var accepted atomic.Int32
	addr := rawServer(t, func(c net.Conn) {
		accepted.Add(1)
		c.Read(make([]byte, 4096))
		c.Close()
	})
	p := testProber(t, addr, nil)
	p.Retries = 2

	if r := check(t, p, KindTLS); r.Status != TLSReset {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, TLSReset)
	}
	if n := accepted.Load(); n != 3 {
		t.Errorf("попыток %d, want 3", n)
	}
}

// udpServer отвечает на STUN Binding Request (respond=true) или молча глотает его.
func udpServer(t *testing.T, respond bool) (string, *atomic.Int32) {
	t.Helper()
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	var received atomic.Int32
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			received.Add(1)
			if !respond || n < stunHeaderLen {
				continue
			}
			resp := make([]byte, stunHeaderLen)
			binary.BigEndian.PutUint16(resp[0:], stunBindingSuccess)
			binary.BigEndian.PutUint32(resp[4:], stunMagicCookie)
			copy(resp[8:], buf[8:stunHeaderLen])
			pc.WriteTo(resp, from)
		}
	}()
	return pc.LocalAddr().String(), &received
}

func TestSTUNOK(t *testing.T) {
	addr, _ := udpServer(t, true)
	if r := check(t, testProber(t, addr, nil), KindSTUN); r.Status != OK {
		t.Fatalf("status=%s detail=%s", r.Status, r.Detail)
	}
}

func TestSTUNNoAnswer(t *testing.T) {
	addr, received := udpServer(t, false)
	p := testProber(t, addr, nil)
	p.ConnectTimeout = 800 * time.Millisecond

	if r := check(t, p, KindSTUN); r.Status != UDPFail {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, UDPFail)
	}
	if n := received.Load(); n < 2 {
		t.Errorf("запрос не повторялся: получено %d", n)
	}
}

func TestWSUpgrade(t *testing.T) {
	srv, pool := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/apiws" || r.Header.Get("Upgrade") != "websocket" {
			w.WriteHeader(http.StatusFound)
			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		rw.Flush()
	})
	p := testProber(t, srv.Listener.Addr().String(), pool)
	ctx := context.Background()

	if r := p.Check(ctx, Target{Kind: KindWS, Host: "example.com", Path: "/apiws"}); r.Status != OK || r.HTTPCode != http.StatusSwitchingProtocols {
		t.Fatalf("status=%s code=%d detail=%s", r.Status, r.HTTPCode, r.Detail)
	}
	if r := p.Check(ctx, Target{Kind: KindWS, Host: "example.com", Path: "/other"}); r.Status != Failed || r.HTTPCode != http.StatusFound {
		t.Fatalf("302 принят за WebSocket: status=%s code=%d", r.Status, r.HTTPCode)
	}
}

func TestUploadOK(t *testing.T) {
	srv, pool := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusMethodNotAllowed)
	})
	p := testProber(t, srv.Listener.Addr().String(), pool)

	if r := check(t, p, KindUpload); r.Status != OK || r.HTTPCode != http.StatusMethodNotAllowed {
		t.Fatalf("status=%s code=%d detail=%s", r.Status, r.HTTPCode, r.Detail)
	}
}

func TestUploadFreeze(t *testing.T) {
	srv, pool := tlsServer(t, func(w http.ResponseWriter, r *http.Request) {
		hang(t, r) // тело «не дошло» — ответа нет
	})
	p := testProber(t, srv.Listener.Addr().String(), pool)

	if r := check(t, p, KindUpload); r.Status != Freeze16K {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, Freeze16K)
	}
}
