//go:build live

// Живая проверка через настоящие серверы Telegram:
//
//	go test -tags live -run Live -v ./internal/tgproxy
package tgproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

const (
	reqPQMulti = 0xbe7e8ef1
	resPQ      = 0x05162463
)

// askPQ отправляет через прокси первый запрос MTProto (req_pq_multi) и ждёт от дата-центра
// resPQ с тем же nonce — значит, вся цепочка шифрования и канал работают.
func askPQ(srv *Server, dc int16) error {
	client, proxySide := net.Pipe()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	go srv.handle(ctx, proxySide)
	defer client.Close()
	client.SetDeadline(time.Now().Add(20 * time.Second))

	init, enc, dec := clientHandshake(srv.Secret, protoIntermediate, dc)
	nonce := make([]byte, 16)
	rand.Read(nonce)

	msg := make([]byte, 40)
	binary.LittleEndian.PutUint64(msg[8:], uint64(time.Now().Unix())<<32) // message_id
	binary.LittleEndian.PutUint32(msg[16:], 20)                           // длина тела
	binary.LittleEndian.PutUint32(msg[20:], reqPQMulti)
	copy(msg[24:], nonce)
	packet := binary.LittleEndian.AppendUint32(nil, uint32(len(msg)))
	packet = append(packet, msg...)
	enc.XORKeyStream(packet, packet)
	go client.Write(append(init, packet...))

	var head [4]byte
	if _, err := io.ReadFull(client, head[:]); err != nil {
		return fmt.Errorf("нет ответа: %w (%+v)", err, srv.Stats())
	}
	dec.XORKeyStream(head[:], head[:])
	n := binary.LittleEndian.Uint32(head[:])
	if n > 1<<16 {
		return fmt.Errorf("странная длина ответа %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(client, body); err != nil {
		return err
	}
	dec.XORKeyStream(body, body)
	if n == 4 {
		return fmt.Errorf("Telegram вернул код ошибки %d", int32(binary.LittleEndian.Uint32(body)))
	}
	if n < 40 || binary.LittleEndian.Uint32(body[20:]) != resPQ || !bytes.Equal(body[24:40], nonce) {
		return errors.New("ответ не resPQ")
	}
	return nil
}

// TestLiveCF проверяет путь через общие домены Cloudflare (tg-ws-proxy): для каждого дата-центра
// resPQ должен прийти именно этим путём.
func TestLiveCF(t *testing.T) {
	domains, err := FetchCFDomains(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("общих доменов: %d", len(domains))
	for _, c := range []struct {
		dc    int16
		label string
	}{{1, "DC1"}, {2, "DC2"}, {-2, "DC2 файлы"}, {3, "DC3"}, {4, "DC4"}, {5, "DC5"}} {
		t.Run(c.label, func(t *testing.T) {
			secret := make([]byte, 16)
			rand.Read(secret)
			srv := &Server{Secret: secret}
			srv.SetCFDomains(domains)
			srv.wsUpstream = srv.dialCF // только Cloudflare, без прямого пути
			start := time.Now()
			if err := askPQ(srv, c.dc); err != nil {
				t.Fatal(err)
			}
			t.Logf("resPQ через Cloudflare за %v", time.Since(start).Round(time.Millisecond))
		})
	}
}

// TestLiveTelegram проверяет каждый дата-центр дважды: первый раз соединение открывается
// на месте, второй — берётся готовым из пула.
func TestLiveTelegram(t *testing.T) {
	cases := []struct {
		dc    int16
		label string
	}{
		{1, "DC1"}, {2, "DC2"}, {-2, "DC2 файлы"}, {3, "DC3"}, {4, "DC4"}, {5, "DC5"},
	}
	for _, c := range cases {
		t.Run(c.label, func(t *testing.T) {
			secret := make([]byte, 16)
			rand.Read(secret)
			srv := &Server{Secret: secret}
			srv.pool = newPool(defaultPoolSize, srv.dialWebSocket)
			defer srv.pool.close()

			start := time.Now()
			if err := askPQ(srv, c.dc); err != nil {
				t.Fatal(err)
			}
			cold := time.Since(start)

			deadline := time.Now().Add(15 * time.Second)
			for srv.pool.ready() < defaultPoolSize && time.Now().Before(deadline) {
				time.Sleep(50 * time.Millisecond)
			}
			start = time.Now()
			if err := askPQ(srv, c.dc); err != nil {
				t.Fatal("из пула:", err)
			}
			warm := time.Since(start)

			st := srv.Stats()
			if st.FromPool != 1 {
				t.Fatalf("второй запрос не взял соединение из пула: %+v", st)
			}
			t.Logf("resPQ: без пула %v, из пула %v (websocket=%d напрямую=%d)", cold.Round(time.Millisecond), warm.Round(time.Millisecond), st.WebSocket, st.Direct)
		})
	}
}
