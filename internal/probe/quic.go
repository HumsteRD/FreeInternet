package probe

import (
	"context"
	"crypto/tls"
	"net"

	"github.com/quic-go/quic-go"
)

// checkQUIC проверяет рукопожатие QUIC. YouTube и Google в браузере по умолчанию
// ходят по QUIC, и его блокируют отдельно от TCP.
func (p *Prober) checkQUIC(ctx context.Context, t Target, r *Result) {
	ctx, cancel := context.WithTimeout(ctx, p.ConnectTimeout)
	defer cancel()

	conn, err := quic.DialAddr(ctx, net.JoinHostPort(r.IP, p.portFor(t, 443)),
		&tls.Config{ServerName: t.Host, NextProtos: []string{"h3"}, RootCAs: p.rootCAs},
		&quic.Config{HandshakeIdleTimeout: p.ConnectTimeout})
	if err != nil {
		if isCertError(err) {
			r.Status, r.Detail = TLSCert, err.Error()
		} else {
			r.Status, r.Detail = QUICFail, err.Error()
		}
		return
	}
	r.TLSVersion = "1.3"
	r.Status = OK
	conn.CloseWithError(0, "")
}
