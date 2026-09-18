package strategy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

const sampleBat = "@echo off\r\n" +
	":: comment winws.exe\" --bogus\r\n" +
	"set \"BIN=%~dp0bin\\\"\r\n" +
	"start \"zapret: %~n0\" /min \"%BIN%winws.exe\" --wf-tcp=80,443,%GameFilterTCP% --wf-udp=443,%GameFilterUDP% ^\r\n" +
	"--filter-udp=443 --hostlist=\"%LISTS%list-general.txt\" --dpi-desync=fake --new ^\r\n" +
	"--filter-tcp=443 --dpi-desync-fake-tls=\"%BIN%tls clienthello.bin\" --dpi-desync-fake-tls=^! --dpi-desync-fake-tls-mod=rnd,sni=\"a^b\"\r\n" +
	"echo done\r\n"

func TestParseFlowsealBat(t *testing.T) {
	path := filepath.Join(t.TempDir(), "general (ALT2).bat")
	if err := os.WriteFile(path, []byte(sampleBat), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := ParseFlowsealBat(path)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "general (ALT2)" {
		t.Errorf("name = %q", s.Name)
	}
	want := []string{
		"--wf-tcp=80,443,{game_tcp}", "--wf-udp=443,{game_udp}",
		"--filter-udp=443", "--hostlist={lists}list-general.txt", "--dpi-desync=fake", "--new",
		"--filter-tcp=443", "--dpi-desync-fake-tls={bin}tls clienthello.bin",
		"--dpi-desync-fake-tls=!", "--dpi-desync-fake-tls-mod=rnd,sni=a^b",
	}
	if !reflect.DeepEqual(s.Args, want) {
		t.Errorf("args =\n%q\nwant\n%q", s.Args, want)
	}

	got := s.Expand(Vars{Bin: `C:\z\bin`, Lists: `C:\z\lists`})
	if got[0] != "--wf-tcp=80,443,12" || got[3] != `--hostlist=C:\z\lists\list-general.txt` {
		t.Errorf("expand = %q", got)
	}
}

func TestParseFlowsealBatUnknownVar(t *testing.T) {
	path := filepath.Join(t.TempDir(), "general.bat")
	bat := "start \"x\" /min \"%BIN%winws.exe\" --wf-tcp=%Unknown%\r\n"
	if err := os.WriteFile(path, []byte(bat), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseFlowsealBat(path); err == nil {
		t.Fatal("ожидалась ошибка о неизвестной переменной")
	}
}

func TestNaturalOrder(t *testing.T) {
	if !(naturalKey("general (ALT2).bat") < naturalKey("general (ALT10).bat")) {
		t.Error("ALT2 должен идти раньше ALT10")
	}
}
