package tgproxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

// frame оформляет тело пакета заголовком протокола.
func frame(proto uint32, body []byte) []byte {
	if proto == protoAbridged {
		if n := len(body) / 4; n < 0x7F {
			return append([]byte{byte(n)}, body...)
		} else {
			return append([]byte{0x7F, byte(n), byte(n >> 8), byte(n >> 16)}, body...)
		}
	}
	return append(binary.LittleEndian.AppendUint32(nil, uint32(len(body))), body...)
}

func codePacket(proto uint32, code int32, pad int) []byte {
	body := binary.LittleEndian.AppendUint32(nil, uint32(code))
	return frame(proto, append(body, make([]byte, pad)...))
}

func TestWatcherFindsBadDC(t *testing.T) {
	for _, proto := range []uint32{protoAbridged, protoIntermediate, protoPadded} {
		big := frame(proto, bytes.Repeat([]byte{0xAB}, 600)) // обычный пакет, внутри встречается что угодно
		fake := frame(proto, append(binary.LittleEndian.AppendUint32(nil, uint32(0xFFFFFE44)), make([]byte, 20)...))
		stream := append(append(append([]byte{}, big...), fake...), codePacket(proto, -404, 0)...)
		prefix := len(stream)
		stream = append(stream, codePacket(proto, badDCCode, 4)...)
		stream = append(stream, big...)

		// По одному байту, кусками и целиком — граница пакета не зависит от нарезки.
		for _, chunk := range []int{1, 3, 7, 64, len(stream)} {
			w := &watcher{proto: proto}
			var sent []byte
			found := false
			for i := 0; i < len(stream) && !found; i += chunk {
				part := stream[i:min(i+chunk, len(stream))]
				out, bad := w.scan(part)
				sent = append(sent, out...)
				found = bad
			}
			// Клиент получает всё до кода -444 и, возможно, заголовок пакета с ним — но не сам код.
			header := 4
			if proto == protoAbridged {
				header = 1
			}
			if !found || len(sent) < prefix || len(sent) > prefix+header || !bytes.Equal(sent, stream[:len(sent)]) {
				t.Fatalf("proto %x, куски по %d: found=%v, отдано %d, код начинается после %d", proto, chunk, found, len(sent), prefix+header)
			}
		}
	}
}

func TestWatcherPassesNormalTraffic(t *testing.T) {
	stream := append(frame(protoPadded, bytes.Repeat([]byte{1}, 40)), codePacket(protoPadded, -429, 0)...)
	w := &watcher{proto: protoPadded}
	if out, bad := w.scan(stream); bad || !bytes.Equal(out, stream) {
		t.Fatalf("обычный поток изменён: %d байт из %d, bad=%v", len(out), len(stream), bad)
	}
}

// TestProxyHidesBadDC: Telegram отвечает -444 — клиент не получает код, соединение закрывается.
func TestProxyHidesBadDC(t *testing.T) {
	secret := bytes.Repeat([]byte{5}, 16)
	up := &pipeUpstream{toServer: make(chan []byte, 16), toProxy: make(chan []byte, 16), closed: make(chan struct{})}
	srv := &Server{
		Addr:       "127.0.0.1:0",
		Secret:     secret,
		wsUpstream: func(context.Context, poolKey) (upstream, error) { return up, nil },
	}
	client, proxySide := net.Pipe()
	go srv.handle(context.Background(), proxySide)
	defer client.Close()

	init, _, dec := clientHandshake(secret, protoPadded, 2)
	go client.Write(init)

	relayInit := <-up.toServer
	_, _, _, tgEnc := telegramSide(t, relayInit)
	ok := frame(protoPadded, bytes.Repeat([]byte{7}, 16))
	reply := append(append([]byte{}, ok...), codePacket(protoPadded, badDCCode, 0)...)
	tgEnc.XORKeyStream(reply, reply)
	up.toProxy <- reply

	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	got, err := io.ReadAll(client)
	if err != nil {
		t.Fatal(err)
	}
	dec.XORKeyStream(got, got)
	// Первый пакет целиком и, может быть, заголовок следующего — но не код -444.
	want := append(append([]byte{}, ok...), 4, 0, 0, 0)
	if !bytes.Equal(got, want) {
		t.Fatalf("клиент получил %x, ждали %x", got, want)
	}
	if st := srv.Stats(); st.BadDC != 1 {
		t.Fatalf("код -444 не учтён: %+v", st)
	}
}

func TestSetErrorRateLimited(t *testing.T) {
	var buf bytes.Buffer
	srv := &Server{Log: slog.New(slog.NewTextHandler(&buf, nil))}
	for range 50 {
		srv.setError(errBadInit)
	}
	if n := strings.Count(buf.String(), "\n"); n != 1 {
		t.Fatalf("одинаковая ошибка записана %d раз, ждали один:\n%s", n, buf.String())
	}
	if st := srv.Stats(); st.Failed != 50 {
		t.Fatalf("счётчик ошибок: %d", st.Failed)
	}
}
