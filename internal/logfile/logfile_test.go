package logfile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestRotates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fi.log")
	l, err := Open(path, 100)
	if err != nil {
		t.Fatal(err)
	}
	line := bytes.Repeat([]byte("x"), 39)
	line = append(line, '\n')
	for range 6 {
		if _, err := l.Write(line); err != nil {
			t.Fatal(err)
		}
	}
	l.Close()
	cur, _ := os.ReadFile(path)
	old, _ := os.ReadFile(path + ".old")
	if len(cur) == 0 || len(cur) > 100 || len(old) == 0 || len(old) > 100 {
		t.Fatalf("журнал %d байт, старый %d — ждали оба не больше 100", len(cur), len(old))
	}
}
