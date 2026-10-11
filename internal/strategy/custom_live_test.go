//go:build live

// Все стратегии настоящего набора Flowseal должны проходить проверку своих стратегий:
//
//	$env:FI_FLOWSEAL="D:\zapret-discord-youtube-1.10.3"; go test -tags live -run LiveCustom -v ./internal/strategy
package strategy

import (
	"os"
	"testing"
)

func TestLiveCustomAcceptsFlowseal(t *testing.T) {
	dir := os.Getenv("FI_FLOWSEAL")
	if dir == "" {
		t.Skip("FI_FLOWSEAL не задан")
	}
	list, err := ImportFlowsealDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if err := ValidateCustom(s.Args); err != nil {
			t.Errorf("%s: %v", s.Name, err)
		}
	}
	t.Logf("проверено стратегий: %d", len(list))
}
