package strategy

import (
	"slices"
	"strings"
	"testing"
)

func TestWithGameIpset(t *testing.T) {
	// Как в general (ALT13): обычное правило по ipset и два игровых.
	args := strings.Fields("--wf-tcp=443,{game_tcp} " +
		"--filter-tcp=80,443 --ipset={lists}ipset-all.txt --dpi-desync=fake --new " +
		"--filter-tcp={game_tcp} --ipset={lists}ipset-all.txt --ipset-exclude={lists}ipset-exclude.txt --dpi-desync=fake --new " +
		"--filter-udp={game_udp} --ipset={lists}ipset-all.txt --dpi-desync=fake")
	got := withGameIpset(args)
	if got[2] != "--ipset={lists}ipset-all.txt" {
		t.Fatal("правило для сайтов должно остаться на ipset-all: его выбирает человек")
	}
	if got[6] != gameIpset || got[len(got)-2] != gameIpset {
		t.Fatalf("игровые правила не переведены на свой список: %v", got)
	}
	if slices.Contains(args, gameIpset) {
		t.Fatal("исходные аргументы стратегии изменились")
	}
}

func TestMergePorts(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"443,80", "80"}, "80,443"},
		{[]string{"2099,5222-5223", "5223-5300,5301"}, "2099,5222-5301"},
		{[]string{"7000-8000", "7500-7600,8001"}, "7000-8001"},
		{[]string{"", " 12 ", "x,0,70000,5-1"}, "12"},
		{nil, ""},
	}
	for _, c := range cases {
		if got := MergePorts(c.in...); got != c.want {
			t.Errorf("MergePorts(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestGamePortsFor(t *testing.T) {
	tcp, udp := GamePortsFor("games", []string{"valorant", "lol"})
	if tcp != "2099,5222-5223,8088,8393-8400,8446" || udp != "5000-5500,7000-8000,8180-8181" {
		t.Errorf("Riot: tcp=%s udp=%s", tcp, udp)
	}
	if tcp, udp := GamePortsFor("games", []string{"roblox"}); tcp != GameOff || udp != "49152-65535" {
		t.Errorf("Roblox: tcp=%s udp=%s", tcp, udp)
	}
	if tcp, udp := GamePortsFor("games", nil); tcp != GameOff || udp != GameOff {
		t.Errorf("без игр фильтр должен быть выключен: tcp=%s udp=%s", tcp, udp)
	}
	if tcp, udp := GamePortsFor("all", []string{"steam"}); tcp != "1024-65535" || udp != "1024-65535" {
		t.Errorf("режим all: tcp=%s udp=%s", tcp, udp)
	}
	for _, g := range Games {
		if MergePorts(g.TCP) != g.TCP || MergePorts(g.UDP) != g.UDP {
			t.Errorf("%s: порты записаны не в каноническом виде", g.ID)
		}
	}
}
