//go:build live

// Живая проверка TLS до видеосерверов YouTube с обычного адаптера, мимо VPN:
// соединение идёт с адреса FI_BIND, по шагам — TCP, рукопожатие TLS, ответ.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_HOSTS="rr1---sn-n8v7kn76.googlevideo.com=173.194.176.147"; go test -tags live -run LiveTLSBind -v ./internal/probe
package probe

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	utls "github.com/refraction-networking/utls"
)

func TestLiveTLSBind(t *testing.T) {
	bind, hosts := os.Getenv("FI_BIND"), os.Getenv("FI_HOSTS")
	if bind == "" || hosts == "" {
		t.Skip("FI_BIND или FI_HOSTS не задан")
	}
	for _, pair := range strings.Split(hosts, ",") {
		host, ip, _ := strings.Cut(pair, "=")
		d := net.Dialer{LocalAddr: &net.TCPAddr{IP: net.ParseIP(bind)}, Timeout: 6 * time.Second}
		start := time.Now()
		raw, err := d.DialContext(context.Background(), "tcp4", net.JoinHostPort(ip, "443"))
		if err != nil {
			t.Logf("%s %s: TCP не соединился за %v: %v", host, ip, time.Since(start).Round(time.Millisecond), err)
			continue
		}
		tcp := time.Since(start)
		conn := utls.UClient(raw, &utls.Config{ServerName: host, NextProtos: []string{"http/1.1"}}, utls.HelloChrome_Auto)
		raw.SetDeadline(time.Now().Add(8 * time.Second))
		if err := conn.Handshake(); err != nil {
			t.Logf("%s %s: TCP за %v, TLS не прошёл за %v: %v", host, ip, tcp.Round(time.Millisecond), time.Since(start).Round(time.Millisecond), err)
			raw.Close()
			continue
		}
		tlsDone := time.Since(start)
		req, _ := http.NewRequest(http.MethodGet, "https://"+host+"/generate_204", nil)
		req.Write(conn)
		resp, err := http.ReadResponse(bufio.NewReader(conn), req)
		if err != nil {
			t.Logf("%s %s: TCP %v, TLS %v, ответа нет: %v", host, ip, tcp.Round(time.Millisecond), tlsDone.Round(time.Millisecond), err)
		} else {
			t.Logf("%s %s: TCP %v, TLS %v, HTTP %d", host, ip, tcp.Round(time.Millisecond), tlsDone.Round(time.Millisecond), resp.StatusCode)
		}
		conn.Close()
	}
}
