package daemon

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestWatchVPNIgnoresBlips(t *testing.T) {
	var mu sync.Mutex
	seq := []string{"", "", "happ-xray", "", "", "happ-xray", "happ-xray", "happ-xray"}
	i := 0
	oldRoute, oldPoll := netRoute, vpnPoll
	netRoute = func() (string, uint32) {
		mu.Lock()
		defer mu.Unlock()
		v := seq[min(i, len(seq)-1)]
		i++
		return v, 7
	}
	vpnPoll = 5 * time.Millisecond
	defer func() { netRoute, vpnPoll = oldRoute, oldPoll }()

	d := &Daemon{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	vpn := func() string {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.vpn
	}
	var seen []string
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { d.watchVPN(ctx); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if v := vpn(); len(seen) == 0 || seen[len(seen)-1] != v {
			seen = append(seen, v)
		}
		mu.Lock()
		finished := i > len(seq)+3
		mu.Unlock()
		if finished {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	<-done
	if got := vpn(); got != "happ-xray" {
		t.Fatalf("устойчивое включение VPN не замечено: %q", got)
	}
	if len(seen) != 2 {
		t.Fatalf("однократный всплеск VPN не должен засчитываться: %q", seen)
	}
}
