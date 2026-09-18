package lists

import (
	"os"
	"path/filepath"
	"testing"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("%s = %q, want %q", filepath.Base(path), got, want)
	}
}

func TestPrepare(t *testing.T) {
	base := t.TempDir()
	mustWrite(t, filepath.Join(base, "list-general.txt"), "discord.com\n")
	mustWrite(t, filepath.Join(base, "list-general-user.txt"), "own.example\n")
	mustWrite(t, filepath.Join(base, "ipset-all.txt"), NoneMarker+"\n")
	mustWrite(t, filepath.Join(base, "ipset-all.txt.backup"), "1.2.3.0/24\n")

	dst := filepath.Join(t.TempDir(), "lists")
	if err := Prepare(dst, base, []string{"x.com"}, IpsetLoaded); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dst, "list-general.txt"), "discord.com\n")
	assertFile(t, filepath.Join(dst, userHostsFile), placeholderHost+"\nx.com\n")
	assertFile(t, filepath.Join(dst, "ipset-all.txt"), "1.2.3.0/24\n")

	if err := WriteUserHosts(dst, nil); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dst, userHostsFile), placeholderHost+"\n")

	if err := Prepare(dst, base, nil, IpsetAny); err != nil {
		t.Fatal(err)
	}
	assertFile(t, filepath.Join(dst, "ipset-all.txt"), "")
}

func TestDomainSet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "list.txt")
	mustWrite(t, path, "# comment\ndiscord.com\n^dns.google\n\n")
	set, err := LoadDomains(path, filepath.Join(t.TempDir(), "missing.txt"))
	if err != nil {
		t.Fatal(err)
	}

	cases := map[string]bool{
		"discord.com":         true,
		"gateway.Discord.com": true,
		"notdiscord.com":      false,
		"dns.google":          true,
		"a.dns.google":        false, // ^ — без поддоменов
		"example.org":         false,
	}
	for host, want := range cases {
		if got := set.Matches(host); got != want {
			t.Errorf("Matches(%q) = %v, want %v", host, got, want)
		}
	}
}
