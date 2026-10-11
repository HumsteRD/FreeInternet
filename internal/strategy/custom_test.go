package strategy

import (
	"slices"
	"strings"
	"testing"
)

func TestParseCustomBat(t *testing.T) {
	bat := "@echo off\r\nset \"BIN=%~dp0bin\\\"\r\n" +
		"start \"zapret\" /min \"%BIN%winws.exe\" --wf-tcp=80,443,%GameFilterTCP% --wf-udp=443 ^\r\n" +
		"--filter-tcp=443 --hostlist=\"%LISTS%list-general.txt\" --dpi-desync=fake,multisplit --dpi-desync-fake-tls=^! " +
		"--dpi-desync-split-seqovl-pattern=\"%BIN%tls_clienthello_www_google_com.bin\" --dpi-desync-fake-quic=0x0A0B\r\n"
	s, err := ParseCustom("Мой ALT", bat)
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "свой: Мой ALT" || !slices.Contains(s.Args, "--hostlist={lists}list-general.txt") || !slices.Contains(s.Args, "--wf-tcp=80,443,{game_tcp}") {
		t.Fatalf("%+v", s)
	}
	if got := s.CustomFiles(); !slices.Equal(got, []string{"lists/list-general.txt", "bin/tls_clienthello_www_google_com.bin"}) {
		t.Fatalf("файлы: %v", got)
	}
}

func TestParseCustomArgsText(t *testing.T) {
	s, err := ParseCustom("simple", "# моя стратегия\n--wf-tcp=443\n--filter-tcp=443 --dpi-desync=multisplit\n--dpi-desync-split-pos=1\n")
	if err != nil || len(s.Args) != 4 {
		t.Fatalf("%+v %v", s, err)
	}
}

func TestCustomRejectsDangerous(t *testing.T) {
	bad := map[string]string{
		"запись файла":        "--wf-tcp=443 --debug=@C:\\Windows\\System32\\evil.dll",
		"автосписок":          "--wf-tcp=443 --hostlist-auto=C:\\x.txt",
		"чужой файл":          "--wf-tcp=443 --hostlist=C:\\Users\\a\\list.txt",
		"выход из папки":      "--wf-tcp=443 --hostlist={lists}..\\..\\config.json",
		"подпапка":            "--wf-tcp=443 --dpi-desync-fake-tls={bin}sub\\x.bin",
		"сырой фильтр":        "--wf-raw=@filter.txt",
		"интерфейс":           "--wf-tcp=443 --wf-iface=1",
		"без фильтра":         "--dpi-desync=fake",
		"не параметр":         "--wf-tcp=443 calc.exe",
		"собачка в значении":  "--wf-tcp=443 --dpi-desync-fake-tls-mod=sni=@x",
		"hex вместо hostlist": "--wf-tcp=443 --hostlist=0x00",
	}
	for why, args := range bad {
		if _, err := ParseCustom("x", args); err == nil {
			t.Errorf("%s: принято %q", why, args)
		}
	}
	if _, err := ParseCustom("../../evil", "--wf-tcp=443"); err == nil || !strings.Contains(err.Error(), "название") {
		t.Errorf("название с путём принято: %v", err)
	}
}
