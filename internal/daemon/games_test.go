package daemon

import (
	"strings"
	"testing"

	"fi/internal/strategy"
)

func TestQuickSet(t *testing.T) {
	all := []strategy.Strategy{{Name: "A"}, {Name: "B"}, {Name: "C"}, {Name: "D"}, {Name: "E"}}
	names := func(set []strategy.Strategy) string {
		var n []string
		for _, s := range set {
			n = append(n, s.Name)
		}
		return strings.Join(n, ",")
	}
	// Текущая первой, дальше три лидера прошлого подбора; пропавшие из набора пропускаются.
	if got := names(quickSet(all, []string{"B", "A", "X", "C", "D", "E"}, "A")); got != "A,B,C,D" {
		t.Errorf("quickSet = %s", got)
	}
	if got := names(quickSet(all, nil, "A")); got != "A" {
		t.Errorf("без лидеров: %s", got)
	}
	if got := names(quickSet(all, []string{"C", "B"}, "удалена из набора")); got != "C,B" {
		t.Errorf("текущей нет в наборе: %s", got)
	}
}
