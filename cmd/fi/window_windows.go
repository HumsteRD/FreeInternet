package main

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

var procDwmSetWindowAttribute = windows.NewLazySystemDLL("dwmapi.dll").NewProc("DwmSetWindowAttribute")

const (
	dwmwaWindowCornerPreference = 33
	dwmwcpRound                 = 2
)

// roundCorners просит Windows 11 скруглить углы окна без рамки, как у обычных окон системы.
// На Windows 10 атрибута нет — вызов просто ничего не меняет.
func roundCorners(hwnd unsafe.Pointer) {
	if hwnd == nil || procDwmSetWindowAttribute.Find() != nil {
		return
	}
	pref := uint32(dwmwcpRound)
	procDwmSetWindowAttribute.Call(uintptr(hwnd), dwmwaWindowCornerPreference, uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
}
