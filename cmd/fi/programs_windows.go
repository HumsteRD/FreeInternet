package main

import (
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// runningCopies — номера запущенных процессов этой программы. У программ на Chromium их
// несколько: главный и по одному на окно и службу.
func runningCopies(exe string) []uint32 {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	name := strings.ToLower(filepath.Base(exe))
	var pids []uint32
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if strings.ToLower(windows.UTF16ToString(e.ExeFile[:])) != name {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, e.ProcessID)
		if err != nil {
			continue
		}
		buf := make([]uint16, windows.MAX_LONG_PATH)
		size := uint32(len(buf))
		if windows.QueryFullProcessImageName(h, 0, &buf[0], &size) == nil && strings.EqualFold(windows.UTF16ToString(buf[:size]), exe) {
			pids = append(pids, e.ProcessID)
		}
		windows.CloseHandle(h)
	}
	return pids
}

// closeCopies завершает процессы программы и ждёт до трёх секунд, пока они исчезнут: иначе
// новый запуск передал бы управление прежнему экземпляру, и прокси программа бы не узнала.
func closeCopies(pids []uint32) {
	var handles []windows.Handle
	for _, pid := range pids {
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, pid)
		if err != nil {
			continue
		}
		windows.TerminateProcess(h, 0)
		handles = append(handles, h)
	}
	deadline := time.Now().Add(3 * time.Second)
	for _, h := range handles {
		wait := time.Until(deadline)
		if wait < 0 {
			wait = 0
		}
		windows.WaitForSingleObject(h, uint32(wait.Milliseconds()))
		windows.CloseHandle(h)
	}
}
