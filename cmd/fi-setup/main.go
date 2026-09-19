//go:build windows

// fi-setup — установщик FI: кладёт программы в Program Files, ставит службу,
// ярлык в «Пуске» и автозапуск окна. Своя копия uninstall.exe в папке программы удаляет FI.
//
//	fi-setup.exe           установить или обновить
//	uninstall.exe /uninstall    удалить
//	/quiet                      без вопросов
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"

	"fi/internal/winsvc"
)

var version = "dev" // задаётся при сборке: -ldflags "-X main.version=…"

const serviceName = "FI"

// programs — что установщик кладёт в папку программы.
var programs = []string{"fi-service.exe", "fi.exe", "uninstall.exe"}

// otherBypasses — службы других обходов: одновременно с FI они не работают.
var otherBypasses = []string{"zapret", "GoodbyeDPI"}

func main() {
	args := os.Args[1:]
	uninstall := isUninstaller || slices.Contains(args, "/uninstall")
	update := slices.Contains(args, "/update") // запускает служба FI: без окон и вопросов
	quiet := update || slices.Contains(args, "/quiet")

	if slices.Contains(args, "/dialog") { // посмотреть окно установки, ничего не устанавливая
		showDialogPreview()
		return
	}
	if !windows.GetCurrentProcessToken().IsElevated() {
		if err := relaunchElevated(args); err != nil && !errors.Is(err, windows.ERROR_CANCELLED) {
			inform("Не удалось запросить права администратора: "+err.Error(), iconError)
		}
		return
	}

	var err error
	if uninstall {
		err = runUninstall(quiet)
	} else {
		err = runInstall(quiet, update)
	}
	if err != nil {
		if update {
			logUpdate("не получилось: " + err.Error()) // окно, открытое службой, никто не увидит
		} else {
			inform("Не получилось: "+err.Error(), iconError)
		}
		os.Exit(1)
	}
}

func installDir() string { return filepath.Join(os.Getenv("ProgramFiles"), "FI") }
func dataDir() string    { return filepath.Join(os.Getenv("ProgramData"), "FI") }
func shortcutPath() string {
	return filepath.Join(os.Getenv("ProgramData"), `Microsoft\Windows\Start Menu\Programs\FI.lnk`)
}

// desktopPath — ярлык на рабочем столе для всех пользователей.
func desktopPath() string { return filepath.Join(os.Getenv("PUBLIC"), "Desktop", "FI.lnk") }

// installedDir — где FI стоит сейчас; пусто, если ещё не установлен.
func installedDir() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, winsvc.UninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	dir, _, _ := k.GetStringValue("InstallLocation")
	return dir
}

// payloadSize — сколько места займут программы FI.
func payloadSize() int64 {
	var total int64
	for _, name := range programs {
		if info, err := fs.Stat(payload, "payload/"+name); err == nil {
			total += info.Size()
		}
	}
	return total
}

func runInstall(quiet, update bool) error {
	dir := installDir()
	for _, name := range programs {
		if _, err := fs.Stat(payload, "payload/"+name); err != nil {
			return errors.New("установщик собран без программ — соберите его скриптом scripts\\build.ps1")
		}
	}
	// Другие обходы: одновременно с FI они не работают, поэтому предлагаем их остановить.
	var found []string
	for _, name := range otherBypasses {
		start, ok := winsvc.StartType(name)
		if !ok || start == mgr.StartDisabled || (start == mgr.StartManual && !winsvc.Running(name)) {
			continue
		}
		found = append(found, name)
	}

	choice := setupChoice{dir: dir, desktop: true, startMenu: true, autostart: true, stopOther: len(found) > 0}
	if prev := installedDir(); prev != "" {
		choice.dir = prev // переустановка: по умолчанию туда же, где FI стоит сейчас
	}
	if !quiet {
		var ok bool
		if choice, ok = askSetup(choice, found, payloadSize()); !ok {
			return nil
		}
	}
	dir = choice.dir

	disabled := map[string]uint32{}
	if choice.stopOther {
		for _, name := range found {
			start, _ := winsvc.StartType(name)
			if err := winsvc.Disable(name); err != nil {
				return fmt.Errorf("служба %s: %w", name, err)
			}
			disabled[name] = start
		}
	}

	// Обновление: работающие программы держат свои файлы. Когда обновляет служба, окно в трее
	// не закрываем: его файл отодвигается в сторону, и окно само перезапустится на новую версию.
	winsvc.StopByName(serviceName)
	if !update {
		winsvc.KillProcesses("fi.exe", 0)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range programs {
		os.Remove(filepath.Join(dir, name+".old")) // прошлое обновление отодвигало занятый файл
		data, err := payload.ReadFile("payload/" + name)
		if err != nil {
			return err
		}
		if err := writeFile(filepath.Join(dir, name), data); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	uninstaller := filepath.Join(dir, "uninstall.exe")

	tray := filepath.Join(dir, "fi.exe")
	type step struct {
		what string
		do   func() error
	}
	steps := []step{
		{"служба", func() error { return installService(filepath.Join(dir, "fi-service.exe")) }},
		{"запись в «Установленных приложениях»", func() error { return registerUninstall(dir, uninstaller, disabled) }},
	}
	if !update { // при обновлении из службы ярлыки и автозапуск не трогаем: это настройки пользователя
		if choice.autostart {
			steps = append(steps, step{"автозапуск окна", func() error { return setAutostart(tray) }})
		} else {
			removeAutostart()
		}
		if choice.startMenu {
			steps = append(steps, step{"ярлык в «Пуске»", func() error { return createShortcut(shortcutPath(), tray) }})
		} else {
			os.Remove(shortcutPath())
		}
		if choice.desktop {
			steps = append(steps, step{"ярлык на рабочем столе", func() error { return createShortcut(desktopPath(), tray) }})
		} else {
			os.Remove(desktopPath())
		}
	}
	for _, s := range steps {
		if err := s.do(); err != nil {
			return fmt.Errorf("%s: %w", s.what, err)
		}
	}

	if update {
		logUpdate("установлена версия " + version)
		return nil
	}
	launchUnelevated(tray)
	if !quiet {
		inform("FI установлен и запущен.\n\nЗначок появится в трее рядом с часами. Windows 11 прячет новые значки: если его не видно, нажмите стрелку слева от часов и перетащите значок на панель.\n\nПри первом запуске окно предложит скачать набор стратегий и подобрать лучшую.", iconInfo)
	}
	return nil
}

// userFiles — файлы в папке данных, которые принадлежат человеку и переживают удаление FI.
var userFiles = []string{"config.json"}

// removeData удаляет папку данных FI; keep — оставить настройки человека.
func removeData(keep bool) (pending bool, err error) {
	if !keep {
		return winsvc.RemoveTree(dataDir())
	}
	entries, err := os.ReadDir(dataDir())
	if err != nil {
		return false, nil
	}
	var errs []error
	for _, e := range entries {
		if slices.Contains(userFiles, e.Name()) {
			continue
		}
		p, err := winsvc.RemoveTree(filepath.Join(dataDir(), e.Name()))
		pending = pending || p
		errs = append(errs, err)
	}
	return pending, errors.Join(errs...)
}

func runUninstall(quiet bool) error {
	if !quiet && !ask("Удалить FI? Служба обхода блокировок будет остановлена и удалена.") {
		return nil
	}
	if err := winsvc.Delete(serviceName); err != nil {
		return fmt.Errorf("служба: %w", err)
	}
	winsvc.KillProcesses("fi.exe", 0)
	removeAutostart()
	os.Remove(shortcutPath())
	os.Remove(desktopPath())

	for name, start := range winsvc.RestoreEntries() {
		if quiet || ask(fmt.Sprintf("FI отключал службу %s (другой обход). Включить её обратно?", name)) {
			if err := winsvc.Enable(name, start); err != nil {
				inform(fmt.Sprintf("Службу %s включить не удалось: %v", name, err), iconWarning)
			}
		}
	}
	registry.DeleteKey(registry.LOCAL_MACHINE, winsvc.UninstallKey)

	if !quiet {
		// Скачанное и временное удаляется всегда, а настройки человека — список сайтов, Telegram,
		// игры — только по его прямому согласию: после повторной установки FI они вернутся.
		keep := !askNo("Удалить и ваши настройки FI: список сайтов, настройки Telegram и игр?\n\n" +
			"Нажмите «Нет», чтобы оставить их: после повторной установки FI всё вернётся.")
		// Файл драйвера WinDivert занят, пока драйвер загружен, поэтому сначала выгружаем его.
		winsvc.KillProcesses("winws.exe", 0)
		winsvc.StopDriver("WinDivert")
		winsvc.StopDriver("WinDivert14")
		switch pending, err := removeData(keep); {
		case err != nil:
			inform("Часть файлов FI удалить не удалось: "+err.Error(), iconWarning)
		case pending:
			inform("Несколько файлов FI были заняты системой — они исчезнут после перезагрузки.", iconInfo)
		}
	}
	if err := removeLater(installDir()); err != nil {
		return fmt.Errorf("папка программы: %w", err)
	}
	if !quiet {
		inform("FI удалён.", iconInfo)
	}
	return nil
}
