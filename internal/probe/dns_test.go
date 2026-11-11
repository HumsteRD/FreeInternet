package probe

import (
	"context"
	"errors"
	"net"
	"testing"
)

func TestClassifyDNS(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "example.com", IsNotFound: true}
	timeout := &net.DNSError{Err: "timeout", Name: "example.com", IsTimeout: true}
	stub := []net.IP{net.ParseIP("127.0.0.1")}

	cases := []struct {
		name   string
		sys    []net.IP
		sysErr error
		dohIP  string
		dohErr error
		wantIP string
		want   Status
	}{
		{"системный DNS не ответил вовремя — не подмена", nil, timeout, "1.2.3.4", nil, "1.2.3.4", OK},
		{"пустой ответ — не подмена", nil, nil, "1.2.3.4", nil, "1.2.3.4", OK},
		{"хоста нет, а DoH знает", nil, notFound, "1.2.3.4", nil, "1.2.3.4", DNSSpoof},
		{"адрес-заглушка", stub, nil, "1.2.3.4", nil, "1.2.3.4", DNSSpoof},
		{"заглушка, DoH недоступен", stub, nil, "", errors.New("нет"), "", DNSSpoof},
		{"хоста нет нигде", nil, notFound, "", errors.New("нет"), "", DNSFail},
	}
	for _, c := range cases {
		ip, st, _ := classifyDNS(c.sys, c.sysErr, c.dohIP, c.dohErr)
		if ip != c.wantIP || st != c.want {
			t.Errorf("%s: %q %s, want %q %s", c.name, ip, st, c.wantIP, c.want)
		}
	}
}

// TestDialContextFallsBackToDoH: системный DNS не знает сайт — соединение идёт по адресу из DoH.
// Другие ошибки (сервер недоступен) DoH не трогают: адрес был известен, дело не в DNS.
func TestDialContextFallsBackToDoH(t *testing.T) {
	oldDial, oldLookup := systemDial, dohLookup
	defer func() { systemDial, dohLookup = oldDial, oldLookup }()

	var dialed []string
	notFound := &net.OpError{Op: "dial", Err: &net.DNSError{Err: "no such host", Name: "www.youtube.com", IsNotFound: true}}
	systemDial = func(_ context.Context, _, addr string) (net.Conn, error) {
		dialed = append(dialed, addr)
		host, _, _ := net.SplitHostPort(addr)
		switch {
		case host == "www.youtube.com":
			return nil, notFound
		case host == "refused.example":
			return nil, errors.New("connection refused")
		}
		ours, theirs := net.Pipe()
		theirs.Close()
		return ours, nil
	}
	lookups := 0
	dohLookup = func(_ context.Context, host string) (string, error) {
		lookups++
		if host == "nowhere.example" {
			return "", errors.New("нет A-записей")
		}
		return "142.251.1.136", nil
	}

	conn, err := DialContext(context.Background(), "tcp", "www.youtube.com:443")
	if err != nil {
		t.Fatalf("сайт, которого не знает системный DNS: %v", err)
	}
	conn.Close()
	if len(dialed) != 2 || dialed[1] != "142.251.1.136:443" || lookups != 1 {
		t.Fatalf("подключения %v, запросов DoH %d", dialed, lookups)
	}

	if _, err := DialContext(context.Background(), "tcp", "refused.example:443"); err == nil || lookups != 1 {
		t.Fatalf("сервер недоступен — DoH не нужен: %v, запросов DoH %d", err, lookups)
	}
	if conn, err := DialContext(context.Background(), "tcp", "example.com:443"); err != nil || lookups != 1 {
		t.Fatalf("обычный сайт идёт через системный DNS: %v, запросов DoH %d", err, lookups)
	} else {
		conn.Close()
	}
}
