package main

import "golang.org/x/sys/windows"

var procGetSystemMetrics = windows.NewLazySystemDLL("user32.dll").NewProc("GetSystemMetrics")

const smCXSmIcon = 49

// trayIconSize — размер маленького значка при текущем масштабе экрана: 16, 20, 24, 32…
func trayIconSize() int {
	n, _, _ := procGetSystemMetrics.Call(smCXSmIcon)
	if n == 0 {
		return 16
	}
	return int(n)
}
