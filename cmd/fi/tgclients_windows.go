package main

import (
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// installNames — как файлы клиентов называются на диске: так путь показывается в окне красиво.
var installNames = []string{"Telegram.exe", "AyuGram.exe", "Kotatogram.exe", "64Gram.exe", "materialgram.exe", "iMe.exe"}

// findTelegramClients ищет клиентов Telegram: сначала запущенные, потом в обычных папках
// установки и на рабочем столе — в том числе перенесённом в OneDrive. extra — выбранные вручную.
func findTelegramClients(extra ...string) []telegramClient {
	paths := append([]string{}, extra...)
	paths = append(paths, runningClients()...)

	appData, local := os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA")
	dirs := []string{
		filepath.Join(appData, "Telegram Desktop"),
		filepath.Join(appData, "AyuGram Desktop"),
		filepath.Join(appData, "AyuGram"),
		filepath.Join(appData, "Kotatogram Desktop"),
		filepath.Join(appData, "64Gram Desktop"),
		filepath.Join(local, "Programs", "AyuGram"),
		filepath.Join(os.Getenv("ProgramFiles"), "Telegram Desktop"),
		filepath.Join(os.Getenv("ProgramFiles"), "AyuGram"),
	}
	for _, folder := range []*windows.KNOWNFOLDERID{windows.FOLDERID_Desktop, windows.FOLDERID_PublicDesktop} {
		if dir, err := windows.KnownFolderPath(folder, 0); err == nil {
			dirs = append(dirs, dir)
		}
	}
	for _, dir := range dirs {
		for _, exe := range installNames {
			paths = append(paths, filepath.Join(dir, exe))
		}
	}
	return collectClients(paths)
}

// runningClients — пути запущенных клиентов Telegram.
func runningClients() []string {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	var paths []string
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if _, ok := clientExes[strings.ToLower(windows.UTF16ToString(e.ExeFile[:]))]; !ok {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil {
			paths = append(paths, windows.UTF16ToString(buf[:size]))
		}
		windows.CloseHandle(h)
	}
	return paths
}
