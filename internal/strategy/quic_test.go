package strategy

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Правила как в general (ALT13): QUIC по list-general, TCP по list-google, QUIC по ipset.
const alt13Like = "--wf-tcp=80,443 --wf-udp=443,{game_udp} " +
	"--filter-udp=443 --hostlist={lists}list-general.txt --hostlist={lists}list-general-user.txt --dpi-desync=fake --new " +
	"--filter-tcp=443 --hostlist={lists}list-google.txt --dpi-desync=hostfakesplit --new " +
	"--filter-tcp=80,443 --hostlist={lists}list-general.txt --dpi-desync=fake --new " +
	"--filter-udp=443 --ipset={lists}ipset-all.txt --dpi-desync=fake"

func TestWithGoogleQUIC(t *testing.T) {
	args := strings.Fields(alt13Like)
	got := withGoogleQUIC(args)
	want := slices.Insert(slices.Clone(args), 4, googleList) // сразу за list-general первого правила QUIC
	if !slices.Equal(got, want) {
		t.Fatalf("list-google в QUIC:\n%v\nждали\n%v", got, want)
	}
	if again := withGoogleQUIC(got); !slices.Equal(again, got) {
		t.Fatal("list-google добавлен второй раз")
	}
	if !slices.Equal(args, strings.Fields(alt13Like)) {
		t.Fatal("исходные аргументы стратегии изменились")
	}
}

func TestExpandGoogleQUIC(t *testing.T) {
	s := Strategy{Args: strings.Fields(alt13Like)}
	dir := t.TempDir()
	if got := strings.Join(s.Expand(Vars{Lists: dir}), " "); strings.Count(got, "list-google.txt") != 1 {
		t.Fatal("без list-google.txt в наборе правило QUIC менять нельзя: winws не найдёт файл")
	}
	os.WriteFile(filepath.Join(dir, "list-google.txt"), []byte("youtube.com\n"), 0o644)
	got := strings.Join(s.Expand(Vars{Lists: dir}), " ")
	if strings.Count(got, "list-google.txt") != 2 {
		t.Fatalf("YouTube не попал в правило QUIC: %s", got)
	}
}
