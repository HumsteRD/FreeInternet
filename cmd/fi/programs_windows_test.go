package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// helperOut — куда запущенная тестом «программа» пишет, что она получила. Задан — значит, это
// не прогон тестов, а сама «программа»: TestMain отвечает и ждёт, пока её закроют.
const helperOut = "FI_TEST_PROGRAM_OUT"

func TestMain(m *testing.M) {
	if path := os.Getenv(helperOut); path != "" {
		os.WriteFile(path, []byte(os.Getenv("HTTPS_PROXY")+"\n"+os.Getenv("NO_PROXY")+"\n"+strings.Join(os.Args[1:], " ")), 0o644)
		time.Sleep(30 * time.Second)
		return
	}
	os.Exit(m.Run())
}

// shortPath — путь в коротком виде 8.3; тот же путь, если короткие имена на диске выключены.
func shortPath(p string) string {
	from, err := windows.UTF16PtrFromString(p)
	if err != nil {
		return p
	}
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n, err := windows.GetShortPathName(from, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		return p
	}
	return windows.UTF16ToString(buf[:n])
}

// TestRunViaProxy запускает настоящую программу (сам тестовый файл) так, как это делает кнопка
// «Запустить»: программа получает прокси FI, FI видит, что она работает, и умеет её закрыть —
// без этого перезапуск «через Cloudflare» оставил бы прежний экземпляр без прокси.
func TestRunViaProxy(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(runningCopies(exe)); n != 1 { // сам прогон тестов
		t.Fatalf("до запуска работает копий: %d, ждали одну — сам тест", n)
	}
	// Тот же файл под коротким именем Windows («C:\Users\RUNNER~1\…») — та же программа.
	if short := shortPath(exe); !strings.EqualFold(short, longPath(exe)) {
		if n := len(runningCopies(short)); n != 1 {
			t.Fatalf("по короткому пути %s работающих копий: %d, ждали одну", short, n)
		}
	}
	out := t.TempDir() + `\program.txt`
	t.Setenv(helperOut, out)
	chromium, err := runViaProxy(exe, "127.0.0.1:1455")
	if err != nil || chromium {
		t.Fatalf("запуск: chromium=%v, %v", chromium, err)
	}
	var got []byte
	for range 100 { // программа стартует не мгновенно
		if got, _ = os.ReadFile(out); len(got) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if want := "http://127.0.0.1:1455\nlocalhost,127.0.0.1,::1\n"; string(got) != want {
		t.Fatalf("программа получила %q, ждали %q", got, want)
	}

	pids := runningCopies(exe)
	if len(pids) != 2 {
		t.Fatalf("работает копий: %d, ждали две — тест и запущенную программу", len(pids))
	}
	// Закрываем только запущенную программу, не сам тест.
	self := uint32(os.Getpid())
	var others []uint32
	for _, pid := range pids {
		if pid != self {
			others = append(others, pid)
		}
	}
	start := time.Now()
	closeCopies(others)
	if n := len(runningCopies(exe)); n != 1 {
		t.Fatalf("после закрытия работает копий: %d", n)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("закрытие заняло %v", took)
	}
}
