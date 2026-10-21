//go:build live

// Живая проверка QUIC до YouTube с обычного адаптера, мимо VPN: рукопожатие идёт
// с адреса FI_BIND (адрес Ethernet), адреса берутся из DoH.
//
//	$env:FI_BIND="192.168.1.2"; go test -tags live -run LiveQUICBind -v ./internal/probe
package probe

import (
	"context"
	"crypto/tls"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestLiveQUICBind(t *testing.T) {
	bind := os.Getenv("FI_BIND")
	if bind == "" {
		t.Skip("FI_BIND не задан")
	}
	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(bind)})
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	defer tr.Close()

	hosts := []string{"www.youtube.com", "music.youtube.com", "i.ytimg.com", "yt3.ggpht.com", "www.google.com"}
	if h := os.Getenv("FI_QUIC_HOSTS"); h != "" { // «хост» или «хост=адрес», через запятую
		hosts = strings.Split(h, ",")
	}
	for _, host := range hosts {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		host, ip, fixed := strings.Cut(host, "=")
		var err error
		if !fixed {
			ip, err = LookupDoH(ctx, host)
		}
		if err != nil {
			t.Logf("%s: DoH: %v", host, err)
			cancel()
			continue
		}
		start := time.Now()
		conn, err := tr.Dial(ctx, &net.UDPAddr{IP: net.ParseIP(ip), Port: 443},
			&tls.Config{ServerName: host, NextProtos: []string{"h3"}},
			&quic.Config{HandshakeIdleTimeout: 6 * time.Second})
		if err != nil {
			t.Logf("%-42s %-15s QUIC НЕ ПРОШЁЛ (%v): %v", host, ip, time.Since(start).Round(time.Millisecond), err)
		} else {
			t.Logf("%-42s %-15s QUIC ok за %v", host, ip, time.Since(start).Round(time.Millisecond))
			conn.CloseWithError(0, "")
		}
		cancel()
	}
}
