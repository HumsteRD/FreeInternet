package tgproxy

import (
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rand"
	"crypto/x509"
	"encoding/binary"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// clientHandshake делает то же, что Telegram Desktop при подключении к MTProto-прокси:
// возвращает заголовок и потоки шифра клиента.
func clientHandshake(secret []byte, proto uint32, dcIdx int16) (init []byte, enc, dec cipher.Stream) {
	plain := make([]byte, initLen)
	rand.Read(plain)
	binary.LittleEndian.PutUint32(plain[protoPos:], proto)
	binary.LittleEndian.PutUint16(plain[dcPos:], uint16(dcIdx))

	keyIV := plain[skipLen : skipLen+keyLen+ivLen]
	enc = newCTR(secretKey(keyIV[:keyLen], secret), keyIV[keyLen:])
	encrypted := make([]byte, initLen)
	enc.XORKeyStream(encrypted, plain)

	init = append(append([]byte(nil), plain[:protoPos]...), encrypted[protoPos:]...)
	back := reversed(keyIV)
	dec = newCTR(secretKey(back[:keyLen], secret), back[keyLen:])
	return init, enc, dec
}

// telegramSide разбирает заголовок прокси так, как это делает сервер Telegram.
func telegramSide(t *testing.T, relayInit []byte) (proto uint32, dcIdx int16, dec, enc cipher.Stream) {
	t.Helper()
	keyIV := relayInit[skipLen : skipLen+keyLen+ivLen]
	dec = newCTR(keyIV[:keyLen], keyIV[keyLen:])
	plain := make([]byte, initLen)
	dec.XORKeyStream(plain, relayInit)
	back := reversed(keyIV)
	enc = newCTR(back[:keyLen], back[keyLen:])
	return binary.LittleEndian.Uint32(plain[protoPos:]), int16(binary.LittleEndian.Uint16(plain[dcPos:])), dec, enc
}

func TestParseInit(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 16)
	init, _, _ := clientHandshake(secret, protoPadded, -4)

	ci, err := parseInit(init, secret)
	if err != nil || ci.dc != 4 || !ci.media || ci.proto != protoPadded {
		t.Fatalf("parseInit = %+v, %v", ci, err)
	}
	if _, err := parseInit(init, bytes.Repeat([]byte{8}, 16)); err == nil {
		t.Fatal("чужой секрет принят")
	}

	relay := newRelayInit(protoPadded, -4)
	proto, idx, _, _ := telegramSide(t, relay)
	if proto != protoPadded || idx != -4 {
		t.Fatalf("Telegram увидит proto=%x dc=%d", proto, idx)
	}
}

func TestSplitter(t *testing.T) {
	packet := func(n int) []byte {
		p := make([]byte, 4+n)
		binary.LittleEndian.PutUint32(p, uint32(n))
		return p
	}
	stream := append(append(packet(8), packet(1000)...), packet(12)...)

	s := &splitter{proto: protoIntermediate}
	var got [][]byte
	for i := 0; i < len(stream); i += 5 { // кусками по 5 байт
		got = append(got, s.push(stream[i:min(i+5, len(stream))])...)
	}
	if len(got) != 3 || len(got[0]) != 12 || len(got[1]) != 1004 || len(got[2]) != 16 {
		t.Fatalf("intermediate: %d пакетов", len(got))
	}

	a := &splitter{proto: protoAbridged}
	short := append([]byte{2}, make([]byte, 8)...)                         // 2×4 байта
	long := append([]byte{0x7F, 0x00, 0x01, 0x00}, make([]byte, 256*4)...) // длина в трёх байтах
	parts := a.push(append(short, long...))
	if len(parts) != 2 || len(parts[0]) != 9 || len(parts[1]) != 4+1024 {
		t.Fatalf("abridged: %d пакетов", len(parts))
	}
}

// pipeUpstream — поддельный сервер Telegram, принимающий кадры по одному.
type pipeUpstream struct {
	toServer chan []byte
	toProxy  chan []byte
	closed   chan struct{}
}

func (p *pipeUpstream) framed() bool { return true }

func (p *pipeUpstream) send(b []byte) error {
	select {
	case p.toServer <- append([]byte(nil), b...):
		return nil
	case <-p.closed:
		return io.EOF
	}
}

func (p *pipeUpstream) recv() ([]byte, error) {
	select {
	case b := <-p.toProxy:
		return b, nil
	case <-p.closed:
		return nil, io.EOF
	}
}

func (p *pipeUpstream) close() {
	select {
	case <-p.closed:
	default:
		close(p.closed)
	}
}

func TestProxyRoundTrip(t *testing.T) {
	secret := bytes.Repeat([]byte{3}, 16)
	up := &pipeUpstream{toServer: make(chan []byte, 16), toProxy: make(chan []byte, 16), closed: make(chan struct{})}
	var gotDC int
	srv := &Server{
		Addr:   "127.0.0.1:0",
		Secret: secret,
		wsUpstream: func(_ context.Context, k poolKey) (upstream, error) {
			gotDC = k.dc
			return up, nil
		},
	}

	client, proxySide := net.Pipe()
	go srv.handle(context.Background(), proxySide)
	defer client.Close()

	init, enc, dec := clientHandshake(secret, protoIntermediate, 2)
	payload := []byte("0123456789abcdef")
	packet := make([]byte, 4+len(payload))
	binary.LittleEndian.PutUint32(packet, uint32(len(payload)))
	copy(packet[4:], payload)
	encrypted := make([]byte, len(packet))
	enc.XORKeyStream(encrypted, packet)
	go client.Write(append(init, encrypted...))

	// Сервер Telegram получает заголовок отдельным кадром, затем ровно один пакет.
	relayInit := <-up.toServer
	proto, idx, tgDec, tgEnc := telegramSide(t, relayInit)
	if proto != protoIntermediate || idx != 2 || gotDC != 2 {
		t.Fatalf("заголовок к Telegram: proto=%x dc=%d (connect dc=%d)", proto, idx, gotDC)
	}
	frame := <-up.toServer
	tgDec.XORKeyStream(frame, frame)
	if !bytes.Equal(frame, packet) {
		t.Fatalf("пакет к Telegram искажён: %x", frame)
	}

	// Ответ Telegram доходит до клиента расшифрованным его ключом.
	reply := []byte("pong from telegram")
	encReply := make([]byte, len(reply))
	tgEnc.XORKeyStream(encReply, reply)
	up.toProxy <- encReply
	got := make([]byte, len(reply))
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(client, got); err != nil {
		t.Fatal(err)
	}
	dec.XORKeyStream(got, got)
	if !bytes.Equal(got, reply) {
		t.Fatalf("ответ клиенту искажён: %q", got)
	}
}

// wsEchoServer — HTTPS-сервер, который принимает WebSocket и возвращает сообщения обратно.
func wsEchoServer(t *testing.T) (*httptest.Server, *x509.CertPool) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || r.URL.Path != "/apiws" {
			w.WriteHeader(http.StatusFound)
			return
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
			// Сервер не маскирует кадры.
			header := []byte{0x80 | opBinary, 127, 0, 0, 0, 0, 0, 0, 0, 0}
			binary.BigEndian.PutUint64(header[2:], uint64(len(payload)))
			conn.Write(append(header, payload...))
		}
	}))
	t.Cleanup(srv.Close)
	pool := x509.NewCertPool()
	pool.AddCert(srv.Certificate())
	return srv, pool
}

func TestWebSocketEcho(t *testing.T) {
	srv, pool := wsEchoServer(t)
	rootCAs = pool
	defer func() { rootCAs = nil }()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	addr := srv.Listener.Addr().String()

	ws, err := dialWS(ctx, addr, "example.com", "/apiws")
	if err != nil {
		t.Fatal(err)
	}
	defer ws.close()
	for _, size := range []int{10, 300, 70000} { // три формата длины кадра
		msg := make([]byte, size)
		rand.Read(msg)
		if err := ws.send(msg); err != nil {
			t.Fatal(err)
		}
		got, err := ws.recv()
		if err != nil || !bytes.Equal(got, msg) {
			t.Fatalf("эхо %d байт: %d, %v", size, len(got), err)
		}
	}

	if _, err := dialWS(ctx, addr, "example.com", "/other"); err == nil {
		t.Fatal("ответ 302 принят за WebSocket")
	}
	if _, err := dialWS(ctx, addr, "wrong.example", "/apiws"); err == nil {
		t.Fatal("чужой сертификат принят")
	}
}

func TestLink(t *testing.T) {
	got := Link("127.0.0.1:1453", bytes.Repeat([]byte{0xAB}, 16))
	want := "tg://proxy?server=127.0.0.1&port=1453&secret=ddabababababababababababababababab"
	if got != want {
		t.Fatalf("Link = %s", got)
	}
}
