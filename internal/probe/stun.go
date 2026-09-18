package probe

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"net"
	"time"
)

const (
	stunBindingRequest = 0x0001
	stunBindingSuccess = 0x0101
	stunMagicCookie    = 0x2112A442
	stunHeaderLen      = 20
)

// checkSTUN отправляет STUN Binding Request. С него начинаются звонки Telegram и WhatsApp,
// WebRTC и голос Discord — и именно этот шаг провайдеры режут, когда блокируют звонки.
// Внешний адрес из ответа не сохраняется: для вердикта он не нужен.
func (p *Prober) checkSTUN(ctx context.Context, t Target, r *Result) {
	d := net.Dialer{Timeout: p.ConnectTimeout}
	conn, err := d.DialContext(ctx, "udp4", net.JoinHostPort(r.IP, p.portFor(t, 3478)))
	if err != nil {
		r.Status, r.Detail = UDPFail, err.Error()
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	req := make([]byte, stunHeaderLen)
	binary.BigEndian.PutUint16(req[0:], stunBindingRequest)
	binary.BigEndian.PutUint32(req[4:], stunMagicCookie)
	rand.Read(req[8:])
	txID := req[8:]

	deadline := time.Now().Add(p.ConnectTimeout)
	buf := make([]byte, 1500)
	// UDP теряет пакеты, поэтому запрос повторяется с растущим интервалом, как в RFC 8489.
	for interval := 250 * time.Millisecond; ; interval *= 2 {
		if _, err := conn.Write(req); err != nil {
			r.Status, r.Detail = UDPFail, err.Error()
			return
		}
		conn.SetReadDeadline(earliest(deadline, time.Now().Add(interval)))
		for {
			n, err := conn.Read(buf)
			if err != nil {
				if !isTimeout(err) {
					r.Status, r.Detail = UDPFail, err.Error()
					return
				}
				break
			}
			if isSTUNSuccess(buf[:n], txID) {
				r.Status = OK
				return
			}
		}
		if !time.Now().Before(deadline) {
			r.Status, r.Detail = UDPFail, fmt.Sprintf("нет ответа за %s", p.ConnectTimeout)
			return
		}
	}
}

func isSTUNSuccess(b, txID []byte) bool {
	return len(b) >= stunHeaderLen &&
		binary.BigEndian.Uint16(b[0:]) == stunBindingSuccess &&
		binary.BigEndian.Uint32(b[4:]) == stunMagicCookie &&
		bytes.Equal(b[8:stunHeaderLen], txID)
}

func earliest(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
