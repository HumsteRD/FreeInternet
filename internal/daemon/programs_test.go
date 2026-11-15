package daemon

import (
	"net"
	"strings"
	"testing"
	"time"
)

// TestPrograms: программа добавлена — служба открывает для неё прокси; убрана последняя — закрывает.
func TestPrograms(t *testing.T) {
	d := cloudDaemon(t, &cloud{token: goodToken})
	if st := d.Status().Programs; st.Apps == nil || len(st.Apps) != 0 || st.Proxy != "" {
		t.Fatalf("пустой список: %+v", st)
	}
	for _, bad := range []string{"", "notepad", `C:\Tools\script.bat`, "C:\a\"b.exe"} {
		if err := d.AddApp(bad); err == nil {
			t.Errorf("AddApp(%q) должен отказать", bad)
		}
	}
	const discord = `C:\Users\me\AppData\Local\Discord\app-1.0.9\Discord.exe`
	if err := d.AddApp(discord); err != nil {
		t.Fatal(err)
	}
	if err := d.AddApp(strings.ToUpper(discord)); err != nil { // тот же файл в другом регистре — не дубль
		t.Fatal(err)
	}
	st := d.Status().Programs
	if len(st.Apps) != 1 || st.Apps[0].Name != "Discord" || st.Apps[0].Path != discord {
		t.Fatalf("список: %+v", st.Apps)
	}
	// Воркера нет — программы через Cloudflare не пойдут, и окно говорит почему.
	if st.Proxy != "" || st.Error == "" {
		t.Fatalf("без воркера: %+v", st)
	}

	// Воркер задан, прокси Telegram работает — прокси программ слушает через порт от него.
	d.cfg.Telegram.Enabled, d.cfg.Telegram.Worker = true, "fi.example.workers.dev"
	d.startTelegram()
	addr := programsAddr(d.cfg.Telegram.Port)
	waitListening(t, addr, true)
	if st := d.Status().Programs; st.Proxy != addr || st.Error != "" {
		t.Fatalf("с воркером: %+v", st)
	}

	// Вторая программа добавляется без перезапуска прокси; убрали обе — прокси закрыт.
	if err := d.AddApp(`D:\Games\Launcher.exe`); err != nil {
		t.Fatal(err)
	}
	if err := d.RemoveApp(discord); err != nil {
		t.Fatal(err)
	}
	waitListening(t, addr, true)
	if err := d.RemoveApp(`D:\Games\Launcher.exe`); err != nil {
		t.Fatal(err)
	}
	waitListening(t, addr, false)
	if err := d.RemoveApp(discord); err == nil {
		t.Fatal("убрать то, чего нет в списке, — ошибка")
	}

	// Добавили снова при работающем прокси Telegram — прокси программ поднимается сам.
	if err := d.AddApp(discord); err != nil {
		t.Fatal(err)
	}
	waitListening(t, addr, true)
}

func waitListening(t *testing.T, addr string, want bool) {
	t.Helper()
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		conn, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			conn.Close()
		}
		if (err == nil) == want {
			return
		}
	}
	t.Fatalf("прокси программ на %s: ждали, что слушает = %v", addr, want)
}

func TestAppName(t *testing.T) {
	for path, want := range map[string]string{
		`C:\Games\Discord\Discord.exe`: "Discord",
		`D:/Tools/Some App.EXE`:        "Some App",
		`launcher.exe`:                 "launcher",
	} {
		if got := appName(path); got != want {
			t.Errorf("appName(%q) = %q, ждали %q", path, got, want)
		}
	}
}
