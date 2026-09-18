//go:build live && windows

// Диагностика этого компьютера — посмотреть глазами, что увидит служба:
//
//	go test -tags live -run LiveDiagnose -v ./internal/diag
package diag

import "testing"

func TestLiveDiagnose(t *testing.T) {
	snap, err := Collect()
	if err != nil {
		t.Logf("собрано не всё: %v", err)
	}
	t.Logf("служб: %d, процессов: %d, адаптеров: %d, интернет через адаптер %d", len(snap.Services), len(snap.Processes), len(snap.Adapters), snap.InternetIf)
	findings := append(Evaluate(snap), EvaluateUser(CollectUser())...)
	for _, f := range findings {
		t.Logf("[%s] %s — %s %s", f.Level, f.Title, f.Detail, f.Fix)
	}
	if len(snap.Services) == 0 {
		t.Fatal("список служб пуст")
	}
}
