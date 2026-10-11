package tgproxy

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/binary"
	"net"
	"net/http"
	"net/http/httptest"
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
