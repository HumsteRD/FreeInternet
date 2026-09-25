package engine

import "strings"

// RunError — winws не запустился или завершился сам. Error() — коротко, для окна;
// Output — весь вывод winws, для журнала.
type RunError struct {
	AtStart bool   // упал при запуске, ещё не начав перехват
	Exit    error  // код завершения
	Output  string // вывод winws
}

func (e *RunError) Error() string {
	if e.AtStart {
		return "winws не запустился: " + e.Reason()
	}
	return "winws завершился: " + e.Reason()
}

// Reason — главное из вывода: последняя непустая строка, в ней winws пишет, что случилось.
func (e *RunError) Reason() string {
	lines := strings.Split(strings.TrimSpace(e.Output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	if e.Exit != nil {
		return e.Exit.Error()
	}
	return "причина неизвестна"
}

// WinDivert — не открылся драйвер WinDivert. Чаще всего он завис после прошлого запуска
// («The object is referenced by other objects so cannot be deleted») и лечится выгрузкой.
func (e *RunError) WinDivert() bool {
	return strings.Contains(strings.ToLower(e.Output), "windivert:")
}
