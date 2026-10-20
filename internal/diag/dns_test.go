package diag

import (
	"slices"
	"testing"
)

func TestDNSCommands(t *testing.T) {
	got := dnsCommands(7, []string{"1.1.1.1", "77.88.8.8"})
	want := [][]string{
		{"interface", "ipv4", "set", "dnsservers", "name=7", "source=static", "address=1.1.1.1", "register=primary", "validate=no"},
		{"interface", "ipv4", "add", "dnsservers", "name=7", "address=77.88.8.8", "index=2", "validate=no"},
	}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("публичный DNS:\n%v\nждали\n%v", got, want)
	}
	back := dnsCommands(7, nil)
	if len(back) != 1 || !slices.Equal(back[0], []string{"interface", "ipv4", "set", "dnsservers", "name=7", "source=dhcp"}) {
		t.Fatalf("возврат к DNS роутера: %v", back)
	}
}
