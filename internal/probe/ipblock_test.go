package probe

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
)

func checkNeutral(t *testing.T, p *Prober) Result {
	t.Helper()
	return p.Check(context.Background(), Target{Name: "test", Kind: KindTLS, Host: "video.example", Neutral: "neutral.example", Weight: 1})
}

func TestIPBlocked(t *testing.T) {
	addr := rawServer(t, func(c net.Conn) { io.Copy(io.Discard, c) }) // молчит на любое имя
	p := testProber(t, addr, nil)

	if r := checkNeutral(t, p); r.Status != IPBlocked {
		t.Fatalf("status=%s detail=%s, want %s", r.Status, r.Detail, IPBlocked)
	}
}

func TestBlockedByNameOnly(t *testing.T) {
	// Сервер молчит только на имя цели: на нейтральное отвечает (здесь — закрывает соединение).
	addr := rawServer(t, func(c net.Conn) {
		buf := make([]byte, 4096)
		n, _ := c.Read(buf)
		if bytes.Contains(buf[:n], []byte("neutral.example")) {
			c.Close()
			return
		}
		io.Copy(io.Discard, c)
	})
	p := testProber(t, addr, nil)

	if r := checkNeutral(t, p); r.Status != TLSTimeout {
		t.Fatalf("status=%s detail=%s, want %s: адрес открыт, блокируют имя — это лечит стратегия", r.Status, r.Detail, TLSTimeout)
	}
}
