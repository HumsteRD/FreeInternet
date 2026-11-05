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

var (
	user32               = windows.NewLazySystemDLL("user32.dll")
	procGetCursorPos     = user32.NewProc("GetCursorPos")
	procMonitorFromPoint = user32.NewProc("MonitorFromPoint")
	procGetMonitorInfoW  = user32.NewProc("GetMonitorInfoW")
	procGetWindowRect    = user32.NewProc("GetWindowRect")
	procSetWindowPos     = user32.NewProc("SetWindowPos")
	procGetDpiForWindow  = user32.NewProc("GetDpiForWindow")
)

type monitorInfo struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
}

// placeAtTray ставит окно в угол рабочей области у панели задач на том мониторе, где курсор:
// туда, где значок FI в трее. Всё — в физических пикселях Windows: расстановка Wails пересчитывает
// их с масштабом экрана, и при масштабе больше 100% окно уезжало ниже края экрана. Если окно выше
// рабочей области, оно укорачивается.
func placeAtTray(hwnd unsafe.Pointer) bool {
	if hwnd == nil || procGetMonitorInfoW.Find() != nil || procSetWindowPos.Find() != nil {
		return false
	}
	var pt struct{ X, Y int32 }
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	const monitorDefaultToNearest = 2
	mon, _, _ := procMonitorFromPoint.Call(uintptr(uint32(pt.X))|uintptr(uint32(pt.Y))<<32, monitorDefaultToNearest)
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		return false
	}
	var wr winRect
	if ok, _, _ := procGetWindowRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&wr))); ok == 0 {
		return false
	}
	dpi := uint32(96)
	if procGetDpiForWindow.Find() == nil {
		if v, _, _ := procGetDpiForWindow.Call(uintptr(hwnd)); v != 0 {
			dpi = uint32(v)
		}
	}
	margin := int32(12 * dpi / 96)
	x, y, w, h := trayPlacement(mi.Monitor, mi.Work, wr.Right-wr.Left, wr.Bottom-wr.Top, margin)
	const swpNoZOrder, swpNoActivate = 0x0004, 0x0010
	ok, _, _ := procSetWindowPos.Call(uintptr(hwnd), 0, uintptr(x), uintptr(y), uintptr(w), uintptr(h), swpNoZOrder|swpNoActivate)
	return ok != 0
}
