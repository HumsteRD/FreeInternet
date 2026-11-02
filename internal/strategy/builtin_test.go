package strategy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestBuiltin(t *testing.T) {
	entries, err := builtinFiles.ReadDir("builtin")
	if err != nil || len(entries) == 0 {
		t.Fatalf("конфигов FI нет: %v", err)
	}
	for _, e := range entries {
		if _, err := parseBuiltin(e.Name()); err != nil {
			t.Errorf("%s не пройдёт проверку своих стратегий: %v", e.Name(), err)
		}
	}
	list := Builtin()
	if len(list) != len(entries) {
		t.Fatalf("загружено %d конфигов из %d", len(list), len(entries))
	}

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "list-google.txt"), []byte("youtube.com\n"), 0o644)
	for _, s := range list {
		if strings.Contains(strings.Join(s.Args, " "), "%") {
			t.Errorf("%s: неподставленная переменная: %v", s.Name, s.Args)
		}
		// YouTube в правиле QUIC уже есть — служба не должна добавлять его второй раз.
		got := s.Expand(Vars{Bin: `C:\b`, Lists: dir})
		quic := quicProfile(got)
		if n := countSuffix(quic, "list-google.txt"); n != 1 {
			t.Errorf("%s: list-google в правиле QUIC %d раз: %v", s.Name, n, quic)
		}
	}
}

func TestBuiltinSocialHasBlockedSites(t *testing.T) {
	i := slices.IndexFunc(Builtin(), func(s Strategy) bool { return s.Name == "general (FI SOCIAL)" })
	if i < 0 {
		t.Fatal("нет FI SOCIAL")
	}
	args := Builtin()[i].Args
	domains := slices.IndexFunc(args, func(a string) bool {
		return strings.HasPrefix(a, "--hostlist-domains=") && strings.Contains(a, "rutracker.org")
	})
	if domains < 0 {
		t.Fatalf("соцсетей и RuTracker нет в стратегии: %v", args)
	}
}

// quicProfile — первое правило для UDP 443.
func quicProfile(args []string) []string {
	start := slices.Index(args, "--filter-udp=443")
	if start < 0 {
		return nil
	}
	end := slices.Index(args[start:], "--new")
	if end < 0 {
		return args[start:]
	}
	return args[start : start+end]
}

func countSuffix(args []string, suffix string) int {
	n := 0
	for _, a := range args {
		if strings.HasSuffix(a, suffix) {
			n++
		}
	}
	return n
}
