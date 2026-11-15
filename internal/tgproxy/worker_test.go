package tgproxy

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNormalizeWorker(t *testing.T) {
	cases := map[string]string{
		"":                             "",
		"  ":                           "",
		"tg-1234.user.workers.dev":     "tg-1234.user.workers.dev",
		"https://tg.example.com/":      "tg.example.com",
		"http://TG.Example.com/apiws":  "tg.example.com",
		"tg.example.com?dc=2":          "tg.example.com",
		"https://tg.example.com/#путь": "tg.example.com",
	}
	for in, want := range cases {
		got, err := NormalizeWorker(in)
		if err != nil || got != want {
			t.Errorf("NormalizeWorker(%q) = %q, %v; ждали %q", in, got, err, want)
		}
	}
	for _, bad := range []string{"localhost", "не домен", "tg.example.com:8443", "user@tg.example.com"} {
		if got, err := NormalizeWorker(bad); err == nil {
			t.Errorf("NormalizeWorker(%q) = %q, ждали ошибку", bad, got)
		}
	}
}

// workerServer — поддельный Cloudflare Worker: принимает WebSocket и складывает пришедшие байты
// подряд, как это делал бы TCP до дата-центра.
func workerServer(t *testing.T, stream chan<- []byte, dc chan<- string) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			w.WriteHeader(http.StatusOK)
			return
		}
		select {
		case dc <- r.URL.Query().Get("dc"):
		default:
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
		rw.Flush()
		peer := &wsConn{conn: conn, br: rw.Reader}
		for {
			_, op, payload, err := peer.readFrame()
			if err != nil || op == opClose {
				return
			}
			if len(payload) > 0 {
				stream <- payload
			}
		}
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool
}

// TestSilentWorkerIsBypassed: воркер принимает WebSocket и сразу закрывает его, ничего не
// передав (так было со сломанным кодом воркера). Следующее соединение идёт в обход него.
func TestSilentWorkerIsBypassed(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
		rw.Write([]byte{0x88, 0x00}) // кадр закрытия
		rw.Flush()
	}))
	defer srv.Close()
	certs := x509.NewCertPool()
	certs.AddCert(srv.Certificate())
	rootCAs = certs
	defer func() { rootCAs = nil }()
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	oldPort := workerPort
	workerPort = port
	defer func() { workerPort = oldPort }()

	fallback := make(chan poolKey, 4)
	secret := bytes.Repeat([]byte{9}, 16)
	proxy := &Server{
		Secret:   secret,
		Worker:   "example.com",
		PoolSize: -1,
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
		wsUpstream: func(_ context.Context, k poolKey) (upstream, error) {
			fallback <- k
			return &fakeUp{}, nil
		},
	}
	connectOnce := func() {
		client, proxySide := net.Pipe()
		defer client.Close()
		done := make(chan struct{})
		go func() { proxy.handle(context.Background(), proxySide); close(done) }()
		init, _, _ := clientHandshake(secret, protoIntermediate, 2)
		client.SetDeadline(time.Now().Add(5 * time.Second))
		client.Write(init)
		buf := make([]byte, 64)
		client.Read(buf) // ждём, пока прокси закроет соединение
		<-done
	}

	connectOnce()
	if !proxy.workerPaused() {
		t.Fatal("молчащий воркер не отложен")
	}
	select {
	case <-fallback:
		t.Fatal("первое соединение должно было пойти через воркер")
	default:
	}
	connectOnce()
	select {
	case <-fallback:
	case <-time.After(time.Second):
		t.Fatal("после молчания воркера соединение не пошло в обход него")
	}
}

// TestWorkerRoute: прокси идёт в Telegram через воркер, отдаёт ему поток байтов без разбивки
// по кадрам, и на том конце получается ровно то, что ждёт сервер Telegram.
func TestWorkerRoute(t *testing.T) {
	stream, dc := make(chan []byte, 32), make(chan string, 1)
	srv, certs := workerServer(t, stream, dc)
	rootCAs = certs
	defer func() { rootCAs = nil }()

	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	oldPort := workerPort
	workerPort = port
	defer func() { workerPort = oldPort }()

	secret := bytes.Repeat([]byte{9}, 16)
	proxy := &Server{
		Secret:   secret,
		Worker:   "example.com",
		PoolSize: -1, // пул здесь только мешал бы считать соединения
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}

	client, proxySide := net.Pipe()
	defer client.Close()
	go proxy.handle(context.Background(), proxySide)

	init, enc, _ := clientHandshake(secret, protoIntermediate, 2)
	payload := []byte("привет из теста")
	packet := binary.LittleEndian.AppendUint32(nil, uint32(len(payload)))
	packet = append(packet, payload...)
	encrypted := make([]byte, len(packet))
	enc.XORKeyStream(encrypted, packet)
	client.SetDeadline(time.Now().Add(5 * time.Second))
	go client.Write(append(init, encrypted...))

	// Собираем байты, пока не наберётся заголовок для Telegram и сам пакет.
	var got []byte
	deadline := time.After(5 * time.Second)
	for len(got) < initLen+len(packet) {
		select {
		case chunk := <-stream:
			got = append(got, chunk...)
		case <-deadline:
			t.Fatalf("воркер получил только %d байт из %d", len(got), initLen+len(packet))
		}
	}
	if n := <-dc; n != "2" {
		t.Fatalf("воркер открыл дата-центр %q", n)
	}

	proto, idx, tgDec, _ := telegramSide(t, got[:initLen])
	if proto != protoIntermediate || idx != 2 {
		t.Fatalf("заголовок к Telegram: proto=%x dc=%d", proto, idx)
	}
	body := got[initLen:]
	tgDec.XORKeyStream(body, body)
	if !bytes.Equal(body, packet) {
		t.Fatalf("пакет искажён: %x", body)
	}
	if st := proxy.Stats(); st.Worker != 1 || st.WebSocket != 1 {
		t.Fatalf("статистика %+v", st)
	}
}

// TestWorkerScript прогоняет код воркера на поддельных сокетах (testdata/worker_test.mjs):
// данные доходят целиком и по порядку, крупными блоками. Нужен Node.js.
func TestWorkerScript(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("нет Node.js — код воркера не проверен")
	}
	out, err := exec.Command(node, "testdata/worker_test.mjs", "cf-worker.js").CombinedOutput()
	if err != nil {
		t.Fatalf("код воркера: %v\n%s", err, out)
	}
	t.Log(strings.TrimSpace(string(out)))
}

// TestWorkerVersionMatchesCode: служба сверяет версию воркера с WorkerVersionCurrent — она должна
// совпадать с номером в самом коде, иначе окно вечно просило бы обновить воркер.
func TestWorkerVersionMatchesCode(t *testing.T) {
	m := regexp.MustCompile(`const VERSION = (\d+);`).FindStringSubmatch(workerSource)
	if m == nil {
		t.Fatal("в коде воркера нет номера версии")
	}
	if v, _ := strconv.Atoi(m[1]); v != WorkerVersionCurrent {
		t.Fatalf("версия в коде воркера %d, а WorkerVersionCurrent = %d", v, WorkerVersionCurrent)
	}
}

// pingWorker — поддельный воркер для сторожа соединений: принимает WebSocket; пока answer
// включён, отвечает на ping, а получив команду — присылает данные.
func pingWorker(t *testing.T, answer *atomic.Bool, send <-chan []byte) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n" +
			"Sec-WebSocket-Accept: " + acceptKey(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
		rw.Flush()
		peer := &wsConn{conn: conn, br: rw.Reader}
		go func() {
			for data := range send {
				peer.writeFrame(opBinary, data)
			}
		}()
		for {
			_, op, payload, err := peer.readFrame()
			if err != nil || op == opClose {
				return
			}
			if op == opPing && answer.Load() {
				peer.writeFrame(opPong, payload)
			}
		}
	}))
	t.Cleanup(srv.Close)
	certs := x509.NewCertPool()
	certs.AddCert(srv.Certificate())
	oldCAs, oldPort, oldPing := rootCAs, workerPort, wsPingEvery
	_, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	rootCAs, workerPort, wsPingEvery = certs, port, 60*time.Millisecond
	t.Cleanup(func() { rootCAs, workerPort, wsPingEvery = oldCAs, oldPort, oldPing })
}

// TestFrozenWorkerConnIsClosed: соединение с воркером замерло — ни данных, ни ответа на ping.
// Сторож закрывает его за несколько проверок, а не ждёт системного таймаута в двадцать секунд.
// Молчащее, но живое соединение (на ping отвечают) не трогает.
func TestFrozenWorkerConnIsClosed(t *testing.T) {
	var answer atomic.Bool
	answer.Store(true)
	send := make(chan []byte, 1)
	defer close(send)
	pingWorker(t, &answer, send)
	s := &Server{
		Worker:   "example.com",
		dnsCache: map[string]dnsEntry{"example.com": {ips: []string{"127.0.0.1"}, at: time.Now()}},
	}
	conn, err := s.dialWorkerTCP(context.Background(), "telegram.org", 443)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Пока чтение не ждёт данных, сторож соединение не трогает, даже если на ping не отвечают.
	answer.Store(false)
	time.Sleep(5 * wsPingEvery)
	if conn.(*wsStream).dead.Load() {
		t.Fatal("сторож закрыл соединение, из которого никто не читал")
	}
	answer.Store(true)

	// Живое соединение молчит десяток проверок — и остаётся открытым: на ping отвечают.
	type result struct {
		data string
		err  error
	}
	read := func() <-chan result {
		ch := make(chan result, 1)
		go func() {
			buf := make([]byte, 64)
			n, err := conn.Read(buf)
			ch <- result{string(buf[:n]), err}
		}()
		return ch
	}
	ch := read()
	select {
	case r := <-ch:
		t.Fatalf("живое молчащее соединение закрыто: %q %v", r.data, r.err)
	case <-time.After(10 * wsPingEvery):
	}
	send <- []byte("данные")
	if r := <-ch; r.err != nil || r.data != "данные" {
		t.Fatalf("после молчания: %q %v", r.data, r.err)
	}

	// Соединение замерло: на ping больше не отвечают.
	answer.Store(false)
	start := time.Now()
	select {
	case r := <-read():
		if r.err == nil || !strings.Contains(r.err.Error(), "замерло") {
			t.Fatalf("замершее соединение: %q %v", r.data, r.err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("замершее соединение не закрыто")
	}
	if took := time.Since(start); took > 6*wsPingEvery+500*time.Millisecond {
		t.Fatalf("сторож ждал %v", took)
	}
}
