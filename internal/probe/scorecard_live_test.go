//go:build live

// Оценка работающей стратегии с обычного адаптера, мимо VPN: сайты, Discord и YouTube по TCP
// с настоящей загрузкой данных и QUIC. Адреса — из DoH, тоже мимо VPN.
//
//	$env:FI_BIND="192.168.1.2"; $env:FI_NODE="rr1---sn-….googlevideo.com"; $env:FI_SITES="a.com,b.org"; go test -tags live -run LiveScorecard -v ./internal/probe
package probe

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/quic-go/quic-go"
)

func TestLiveScorecard(t *testing.T) {
	bind := net.ParseIP(os.Getenv("FI_BIND"))
	if bind == nil {
		t.Skip("FI_BIND не задан")
	}
	d := &net.Dialer{LocalAddr: &net.TCPAddr{IP: bind}, Timeout: 5 * time.Second}
	http.DefaultClient = &http.Client{Timeout: 8 * time.Second, Transport: &http.Transport{DialContext: d.DialContext}}

	p := New()
	p.localIP = bind
	p.lookup = func(ctx context.Context, host string) (string, Status, string) {
		ip, err := lookupDoH(ctx, host)
		if err != nil {
			return "", DNSFail, err.Error()
		}
		return ip, OK, ""
	}

	targets := WithYouTubeNode(DefaultTargets(), os.Getenv("FI_NODE"))
	targets = slices.DeleteFunc(targets, func(x Target) bool { return x.Kind == KindQUIC || x.Kind == KindSTUN })
	for host := range strings.SplitSeq(os.Getenv("FI_SITES"), ",") {
		if host != "" {
			targets = append(targets, Target{Name: host, Group: "user", Kind: KindPage, Host: host, Weight: 1})
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	start := time.Now()
	results := p.Run(ctx, targets)
	passed := 0
	var total time.Duration
	var fails []string
	for _, r := range results {
		if r.Status == OK || r.Status == Slow {
			passed++
			total += r.Duration
		} else {
			fails = append(fails, fmt.Sprintf("%s=%s", r.Target.Name, r.Status))
		}
	}

	udp, err := net.ListenUDP("udp4", &net.UDPAddr{IP: bind})
	if err != nil {
		t.Fatal(err)
	}
	tr := &quic.Transport{Conn: udp}
	defer tr.Close()
	quicHosts := []string{"www.youtube.com", "i.ytimg.com", "yt3.ggpht.com", "discord.com", "cdn.discordapp.com"}
	quicOK := 0
	for _, host := range quicHosts {
		qctx, qcancel := context.WithTimeout(ctx, 5*time.Second)
		ip, err := lookupDoH(qctx, host)
		if err == nil {
			var conn *quic.Conn
			conn, err = tr.Dial(qctx, &net.UDPAddr{IP: net.ParseIP(ip), Port: 443},
				&tls.Config{ServerName: host, NextProtos: []string{"h3"}}, &quic.Config{HandshakeIdleTimeout: 4 * time.Second})
			if err == nil {
				conn.CloseWithError(0, "")
				quicOK++
			}
		}
		if err != nil {
			fails = append(fails, "QUIC "+host)
		}
		qcancel()
	}
	slices.Sort(fails)
	t.Logf("ИТОГ tcp=%d/%d quic=%d/%d быстрота=%v за=%v сбои: %s", passed, len(targets), quicOK, len(quicHosts),
		(total / time.Duration(max(passed, 1))).Round(time.Millisecond), time.Since(start).Round(time.Second), strings.Join(fails, "; "))
}
