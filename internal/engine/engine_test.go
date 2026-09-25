package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestRunErrorIsShort(t *testing.T) {
	out := "github version v72.9\r\n\r\nLoading hostlist C:/lists/list-google.txt\r\nLoaded 22 hosts\r\n" +
		"windivert: error opening filter: The object is referenced by other objects so cannot be deleted.\r\n"
	e := &RunError{AtStart: true, Exit: errors.New("exit status 10"), Output: out}
	want := "winws не запустился: windivert: error opening filter: The object is referenced by other objects so cannot be deleted."
	if e.Error() != want || strings.Contains(e.Error(), "Loading hostlist") {
		t.Fatalf("Error() = %q", e.Error())
	}
	if !e.WinDivert() {
		t.Error("ошибка WinDivert не распознана")
	}
	if (&RunError{Exit: errors.New("exit status 1"), Output: "  \n"}).Reason() != "exit status 1" {
		t.Error("без вывода причина — код завершения")
	}
}
