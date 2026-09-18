package probe

import (
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
