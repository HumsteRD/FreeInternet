//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"

	"fi/internal/winsvc"
)

// ─── Диалоги ──────────────────────────────────────────────────────────────

const (
	mbOK          = 0x0
	mbYesNo       = 0x4
	iconError     = 0x10
	iconQuestion  = 0x20
	iconWarning   = 0x30
	iconInfo      = 0x40
	mbSetForegrnd = 0x10000
	idYes         = 6
)

func messageBox(text string, flags uint32) int32 {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("FI")
	r, _ := windows.MessageBox(0, t, c, flags|mbSetForegrnd)
	return r
}

func ask(text string) bool { return messageBox(text, mbYesNo|iconQuestion) == idYes }

func inform(text string, icon uint32) { messageBox(text, mbOK|icon) }

// relaunchElevated перезапускает установщик с запросом прав администратора.
func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(args, " "))
	cwd, _ := windows.UTF16PtrFromString(filepath.Dir(exe))
	return windows.ShellExecute(0, verb, file, params, cwd, windows.SW_SHOWNORMAL)
}

// ─── Служба FI ───────────────────────────────────────────────────────

// installService ставит службу FI или обновляет путь к ней и запускает.
func installService(exe string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()

	s, err := m.OpenService(serviceName)
	if err == nil {
		defer s.Close()
		if err := winsvc.Stop(s); err != nil {
			return err
		}
		cfg, err := s.Config()
		if err != nil {
			return err
		}
		cfg.BinaryPathName = `"` + exe + `"`
		cfg.StartType = mgr.StartAutomatic
		if err := s.UpdateConfig(cfg); err != nil {
			return err
		}
		return s.Start()
	}

	s, err = m.CreateService(serviceName, exe, mgr.Config{
		DisplayName: "FI",
		Description: "Обход блокировок и замедлений: держит стратегию обхода и проверяет сервисы.",
		StartType:   mgr.StartAutomatic,
	})
	if err != nil {
		return err
	}
	defer s.Close()
	actions := []mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 2 * time.Minute},
	}
	if err := s.SetRecoveryActions(actions, uint32((24 * time.Hour).Seconds())); err != nil {
		return err
	}
	return s.Start()
}

// launchUnelevated запускает окно FI от имени пользователя, а не администратора:
// через Проводник процесс наследует его обычные права.
func launchUnelevated(exe string) {
	exec.Command("explorer.exe", exe).Start()
}

// ─── Файлы ────────────────────────────────────────────────────────────────

// writeFile пишет во временный файл и подменяет им целевой. Запущенный .exe (окно в трее при
// обновлении из службы) перезаписать нельзя, но можно переименовать — его отодвигаем в .old.
func writeFile(path string, data []byte) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, data, 0o755); err != nil {
		return err
	}
	err := os.Rename(tmp, path)
	if err == nil {
		return nil
	}
	old := path + ".old"
	os.Remove(old)
	if rerr := os.Rename(path, old); rerr != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Rename(old, path)
		os.Remove(tmp)
		return err
	}
	return nil
}

// logUpdate дописывает строку в журнал обновлений: установщик, запущенный службой, окон не показывает.
func logUpdate(line string) {
	f, err := os.OpenFile(filepath.Join(dataDir(), "update.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), line)
}

func dirSizeKB(dir string) uint32 {
	var total int64
	filepath.WalkDir(dir, func(_ string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			if info, err := e.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return uint32(total / 1024)
}

// removeLater удаляет папку программы после выхода установщика: uninstall.exe лежит в ней же.
func removeLater(dir string) error {
	cmd := exec.Command("cmd.exe")
	cmd.Dir = os.TempDir()
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
		CmdLine:       fmt.Sprintf(`cmd.exe /c ping -n 3 127.0.0.1 >nul & rmdir /s /q "%s"`, dir),
	}
	return cmd.Start()
}

// ─── Реестр и ярлык ───────────────────────────────────────────────────────

func registerUninstall(dir, uninstaller string, disabled map[string]uint32) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, winsvc.UninstallKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	strs := map[string]string{
		"DisplayName":          "FI",
		"DisplayVersion":       version,
		"Publisher":            "FI",
		"InstallLocation":      dir,
		"DisplayIcon":          filepath.Join(dir, "fi.exe"),
		"UninstallString":      `"` + uninstaller + `" /uninstall`,
		"QuietUninstallString": `"` + uninstaller + `" /uninstall /quiet`,
	}
	for name, value := range strs {
		if err := k.SetStringValue(name, value); err != nil {
			return err
		}
	}
	dwords := map[string]uint32{"NoModify": 1, "NoRepair": 1, "EstimatedSize": dirSizeKB(dir)}
	for name, start := range disabled {
		dwords[winsvc.RestorePrefix+name] = start
	}
	for name, value := range dwords {
		if err := k.SetDWordValue(name, value); err != nil {
			return err
		}
	}
	return nil
}

// runValue — имя записи автозапуска; так же её называет переключатель в настройках окна.
const (
	runKey   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValue = "fi"
)

func setAutostart(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(runValue, `"`+exe+`" --hidden`)
}

func removeAutostart() {
	if k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE); err == nil {
		k.DeleteValue(runValue)
		k.Close()
	}
}

// createShortcut создаёт ярлык через COM-объект WScript.Shell.
func createShortcut(lnk, target string) error {
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
	script := fmt.Sprintf(`$s = (New-Object -ComObject WScript.Shell).CreateShortcut(%s); $s.TargetPath = %s; $s.WorkingDirectory = %s; $s.Description = %s; $s.Save()`,
		quote(lnk), quote(target), quote(filepath.Dir(target)), quote("FI — обход блокировок"))
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
