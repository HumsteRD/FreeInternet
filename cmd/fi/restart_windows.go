package main

import (
	"time"

	"golang.org/x/sys/windows"
)

// waitForProcess ждёт завершения процесса, но не дольше timeout.
func waitForProcess(pid uint32, timeout time.Duration) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		return // процесса уже нет
	}
	defer windows.CloseHandle(h)
	windows.WaitForSingleObject(h, uint32(timeout.Milliseconds()))
}
