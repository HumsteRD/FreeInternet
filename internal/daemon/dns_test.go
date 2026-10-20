package daemon

import (
	"testing"

	"fi/internal/probe"
)

func TestDNSBroken(t *testing.T) {
	results := make([]probe.Result, 10)
	if dnsBroken(results) {
		t.Fatal("системный DNS отвечал — всё в порядке")
	}
	results[0].SysDNSFailed, results[1].SysDNSFailed = true, true
	if dnsBroken(results) {
		t.Fatal("два случайных таймаута — ещё не сломанный DNS")
	}
	results[2].SysDNSFailed = true
	if !dnsBroken(results) {
		t.Fatal("DNS не ответил для трёх сайтов — окно должно предупредить")
	}
}
