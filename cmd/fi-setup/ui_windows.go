//go:build windows

// Окно установки: папка, ярлыки, автозапуск и место на диске. Сделано на обычных окнах Windows,
// без WebView2 — установщик должен открываться на любой машине и мгновенно.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// setupChoice — что человек выбрал в окне установки.
type setupChoice struct {
	dir       string
	desktop   bool // ярлык на рабочем столе
	startMenu bool // ярлык в меню «Пуск»
	autostart bool // запускать окно вместе с Windows
	stopOther bool // остановить другие обходы и убрать их из автозапуска
}

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")
	comctl32 = windows.NewLazySystemDLL("comctl32.dll")

	procRegisterClassEx     = user32.NewProc("RegisterClassExW")
	procCreateWindowEx      = user32.NewProc("CreateWindowExW")
	procDefWindowProc       = user32.NewProc("DefWindowProcW")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procUpdateWindow        = user32.NewProc("UpdateWindow")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procIsDialogMessage     = user32.NewProc("IsDialogMessageW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	procSendMessage         = user32.NewProc("SendMessageW")
	procSetWindowText       = user32.NewProc("SetWindowTextW")
	procGetWindowText       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procAdjustWindowRect    = user32.NewProc("AdjustWindowRect")
	procSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	procGetDpiForSystem     = user32.NewProc("GetDpiForSystem")
	procCreateFont          = gdi32.NewProc("CreateFontW")
	procDeleteObject        = gdi32.NewProc("DeleteObject")
	procBrowseForFolder     = shell32.NewProc("SHBrowseForFolderW")
	procPathFromIDList      = shell32.NewProc("SHGetPathFromIDListW")
	procDiskFreeSpaceEx     = kernel32.NewProc("GetDiskFreeSpaceExW")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
	procInitCommonControls  = comctl32.NewProc("InitCommonControlsEx")
	procCoTaskMemFree       = windows.NewLazySystemDLL("ole32.dll").NewProc("CoTaskMemFree")
)

const (
	wsOverlapped   = 0x00000000
	wsCaption      = 0x00C00000
	wsSysMenu      = 0x00080000
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsTabStop      = 0x00010000
	wsBorder       = 0x00800000
	wsExClientEdge = 0x00000200
	wsExControlPar = 0x00010000

	bsAutoCheckbox = 0x00000003
	bsDefPushBtn   = 0x00000001
	esAutoHScroll  = 0x00000080
	ssLeft         = 0x00000000

	wmDestroy  = 0x0002
	wmClose    = 0x0010
	wmCommand  = 0x0111
	wmSetFont  = 0x0030
	bmGetCheck = 0x00F0
	bmSetCheck = 0x00F1
	enChange   = 0x0300 // текст в поле ввода изменился

	idOK      = 1
	idCancel  = 2
	idBrowse  = 100
	idPath    = 101
	idSpace   = 102
	idDesktop = 103
	idStart   = 104
	idAuto    = 105
	idStop    = 106

	swShow      = 5
	smCXScreen  = 0
	smCYScreen  = 1
	colorWindow = 5
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type rect struct{ left, top, right, bottom int32 }

type winMsg struct {
	hwnd    windows.Handle
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      struct{ x, y int32 }
}

type browseInfo struct {
	owner       windows.Handle
	root        uintptr
	displayName *uint16
	title       *uint16
	flags       uint32
	callback    uintptr
	lParam      uintptr
	image       int32
}

func utf16(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// askSetup показывает окно установки. false — человек закрыл окно или нажал «Отмена».
// Если окно создать не удалось, спрашиваем по-старому одним вопросом.
func askSetup(choice setupChoice, otherBypasses []string, needBytes int64) (setupChoice, bool) {
	s := &setupWindow{choice: choice, others: otherBypasses, need: needBytes}
	if err := s.run(); err != nil {
		text := fmt.Sprintf("Установить FI %s в %s?\n\nБудут установлены служба обхода блокировок и значок в трее.", version, choice.dir)
		return choice, ask(text)
	}
	return s.choice, s.ok
}

// showDialogPreview показывает окно установки и записывает выбор в файл, ничего не устанавливая:
// fi-setup.exe /dialog. Нужно, чтобы смотреть и править вид окна.
func showDialogPreview() {
	choice, ok := askSetup(
		setupChoice{dir: installDir(), desktop: true, startMenu: true, autostart: true, stopOther: true},
		[]string{"zapret"}, payloadSize())
	line := fmt.Sprintf("установка=%v папка=%q рабочий стол=%v пуск=%v автозапуск=%v другой обход=%v\n",
		ok, choice.dir, choice.desktop, choice.startMenu, choice.autostart, choice.stopOther)
	os.WriteFile(filepath.Join(os.TempDir(), "fi-dialog.txt"), []byte(line), 0o644)
}

type setupWindow struct {
	choice setupChoice
	others []string
	need   int64

	hwnd     windows.Handle
	controls map[int]windows.Handle
	font     uintptr
	ok       bool
}

func (s *setupWindow) run() error {
	// Окно и его сообщения должны жить на одном потоке.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	module, _, err := procGetModuleHandle.Call(0)
	if module == 0 {
		return err
	}
	inst := windows.Handle(module)
	var icc struct{ size, flags uint32 }
	icc.size, icc.flags = uint32(unsafe.Sizeof(icc)), 0x00004020 // стандартные классы + кнопки со стилями
	procInitCommonControls.Call(uintptr(unsafe.Pointer(&icc)))

	className := utf16("FISetupWindow")
	class := wndClassEx{
		size:       uint32(unsafe.Sizeof(wndClassEx{})),
		wndProc:    windows.NewCallback(s.wndProc),
		instance:   inst,
		cursor:     loadArrowCursor(),
		background: windows.Handle(colorWindow + 1),
		className:  className,
		icon:       loadAppIcon(inst),
		iconSm:     loadAppIcon(inst),
	}
	if atom, _, err := procRegisterClassEx.Call(uintptr(unsafe.Pointer(&class))); atom == 0 {
		return err
	}

	dpi := s.dpi()
	width, height := s.scale(516, dpi), s.scale(352, dpi)
	if len(s.others) == 0 {
		height = s.scale(326, dpi)
	}
	// Ширина и высота окна считаются вместе с рамкой и заголовком, а разметка — по содержимому.
	const style = wsOverlapped | wsCaption | wsSysMenu
	box := rect{0, 0, int32(width), int32(height)}
	procAdjustWindowRect.Call(uintptr(unsafe.Pointer(&box)), style, 0)
	width, height = int(box.right-box.left), int(box.bottom-box.top)

	screenW, _, _ := procGetSystemMetrics.Call(smCXScreen)
	screenH, _, _ := procGetSystemMetrics.Call(smCYScreen)
	hwnd, _, err := procCreateWindowEx.Call(wsExControlPar,
		uintptr(unsafe.Pointer(className)),
		uintptr(unsafe.Pointer(utf16("Установка FI "+version))),
		style,
		(screenW-uintptr(width))/2, (screenH-uintptr(height))/3,
		uintptr(width), uintptr(height),
		0, 0, uintptr(inst), 0)
	if hwnd == 0 {
		return err
	}
	s.hwnd = windows.Handle(hwnd)

	procShowWindow.Call(hwnd, swShow)
	procUpdateWindow.Call(hwnd)
	procSetForegroundWindow.Call(hwnd)

	var m winMsg
	for {
		r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		if ok, _, _ := procIsDialogMessage.Call(hwnd, uintptr(unsafe.Pointer(&m))); ok != 0 {
			continue
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&m)))
	}
	if s.font != 0 {
		procDeleteObject.Call(s.font)
	}
	return nil
}

func (s *setupWindow) dpi() int {
	dpi, _, _ := procGetDpiForSystem.Call()
	if dpi < 96 {
		return 96
	}
	return int(dpi)
}

func (s *setupWindow) scale(v, dpi int) int { return v * dpi / 96 }

// build создаёт содержимое окна: пояснение, папку, место на диске и флажки.
func (s *setupWindow) build() {
	dpi := s.dpi()
	px := func(v int) int { return s.scale(v, dpi) }
	s.controls = map[int]windows.Handle{}
	s.font = createUIFont(px(9))

	text := func(id, x, y, w, h int, value string) {
		s.add("STATIC", value, ssLeft, 0, id, px(x), px(y), px(w), px(h))
	}
	text(0, 16, 14, 470, 18, "FI "+version+": обход блокировок и замедлений")
	text(0, 16, 36, 470, 34, "Будут установлены служба обхода и окно со значком в трее.\nСлужба запускается вместе с Windows и работает в фоне.")

	text(0, 16, 86, 470, 16, "Папка установки")
	s.add("EDIT", s.choice.dir, esAutoHScroll|wsTabStop|wsBorder, wsExClientEdge, idPath, px(16), px(106), px(370), px(24))
	s.add("BUTTON", "Обзор…", wsTabStop, 0, idBrowse, px(394), px(106), px(94), px(24))
	text(idSpace, 16, 138, 470, 16, s.spaceLine())

	y := 168
	s.add("BUTTON", "Ярлык на рабочем столе", bsAutoCheckbox|wsTabStop, 0, idDesktop, px(16), px(y), px(470), px(22))
	s.add("BUTTON", "Ярлык в меню «Пуск»", bsAutoCheckbox|wsTabStop, 0, idStart, px(16), px(y+26), px(470), px(22))
	s.add("BUTTON", "Запускать окно вместе с Windows", bsAutoCheckbox|wsTabStop, 0, idAuto, px(16), px(y+52), px(470), px(22))
	bottom := y + 78
	if len(s.others) > 0 {
		label := "Остановить другой обход (" + strings.Join(s.others, ", ") + ") и убрать из автозапуска"
		s.add("BUTTON", label, bsAutoCheckbox|wsTabStop, 0, idStop, px(16), px(bottom), px(470), px(22))
		bottom += 26
	}
	s.check(idDesktop, s.choice.desktop)
	s.check(idStart, s.choice.startMenu)
	s.check(idAuto, s.choice.autostart)
	s.check(idStop, s.choice.stopOther)

	s.add("BUTTON", "Установить", bsDefPushBtn|wsTabStop, 0, idOK, px(250), px(bottom+14), px(110), px(28))
	s.add("BUTTON", "Отмена", wsTabStop, 0, idCancel, px(374), px(bottom+14), px(110), px(28))
}

func (s *setupWindow) add(class, title string, style, exStyle uintptr, id, x, y, w, h int) {
	hwnd, _, _ := procCreateWindowEx.Call(exStyle,
		uintptr(unsafe.Pointer(utf16(class))),
		uintptr(unsafe.Pointer(utf16(title))),
		wsChild|wsVisible|style,
		uintptr(x), uintptr(y), uintptr(w), uintptr(h),
		uintptr(s.hwnd), uintptr(id), 0, 0)
	if hwnd == 0 {
		return
	}
	procSendMessage.Call(hwnd, wmSetFont, s.font, 1)
	if id != 0 {
		s.controls[id] = windows.Handle(hwnd)
	}
}

func (s *setupWindow) check(id int, on bool) {
	if h, ok := s.controls[id]; ok {
		state := uintptr(0)
		if on {
			state = 1
		}
		procSendMessage.Call(uintptr(h), bmSetCheck, state, 0)
	}
}

func (s *setupWindow) checked(id int) bool {
	h, ok := s.controls[id]
	if !ok {
		return false
	}
	r, _, _ := procSendMessage.Call(uintptr(h), bmGetCheck, 0, 0)
	return r == 1
}

func (s *setupWindow) pathValue() string {
	h, ok := s.controls[idPath]
	if !ok {
		return s.choice.dir
	}
	n, _, _ := procGetWindowTextLength.Call(uintptr(h))
	buf := make([]uint16, n+1)
	procGetWindowText.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if value := strings.TrimSpace(windows.UTF16ToString(buf)); value != "" {
		return value
	}
	return s.choice.dir
}

// spaceLine — сколько нужно места и сколько свободно на выбранном диске.
func (s *setupWindow) spaceLine() string {
	line := fmt.Sprintf("Нужно %s. Набор стратегий скачается отдельно, ещё около 5 МБ.", mb(s.need))
	if free, ok := freeSpace(s.choice.dir); ok {
		line = fmt.Sprintf("Нужно %s, свободно %s. Набор стратегий скачается отдельно, ещё около 5 МБ.", mb(s.need), mb(free))
	}
	return line
}

func (s *setupWindow) wndProc(hwnd windows.Handle, msg uint32, wParam, lParam uintptr) uintptr {
	switch msg {
	case 0x0001: // WM_CREATE
		s.hwnd = hwnd
		s.build()
		return 0
	case wmCommand:
		if int(wParam>>16) == enChange && int(wParam&0xFFFF) == idPath { // путь вписали руками
			s.choice.dir = s.pathValue()
			procSetWindowText.Call(uintptr(s.controls[idSpace]), uintptr(unsafe.Pointer(utf16(s.spaceLine()))))
			return 0
		}
		switch id := int(wParam & 0xFFFF); id {
		case idOK:
			s.choice.dir = s.pathValue()
			s.choice.desktop, s.choice.startMenu = s.checked(idDesktop), s.checked(idStart)
			s.choice.autostart, s.choice.stopOther = s.checked(idAuto), s.checked(idStop)
			s.ok = true
			procDestroyWindow.Call(uintptr(hwnd))
		case idCancel:
			procDestroyWindow.Call(uintptr(hwnd))
		case idBrowse:
			if dir, ok := browseFolder(hwnd, s.pathValue()); ok {
				s.choice.dir = filepath.Join(dir, "FI")
				procSetWindowText.Call(uintptr(s.controls[idPath]), uintptr(unsafe.Pointer(utf16(s.choice.dir))))
				procSetWindowText.Call(uintptr(s.controls[idSpace]), uintptr(unsafe.Pointer(utf16(s.spaceLine()))))
			}
		}
		return 0
	case wmClose:
		procDestroyWindow.Call(uintptr(hwnd))
		return 0
	case wmDestroy:
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProc.Call(uintptr(hwnd), uintptr(msg), wParam, lParam)
	return r
}

// browseFolder показывает выбор папки; возвращает папку, внутри которой создать FI.
func browseFolder(owner windows.Handle, current string) (string, bool) {
	windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED)
	buf := make([]uint16, windows.MAX_PATH)
	bi := browseInfo{
		owner:       owner,
		displayName: &buf[0],
		title:       utf16("Выберите папку, в которую установить FI"),
		flags:       0x00000040 | 0x00000001, // новый вид диалога, только папки
	}
	idl, _, _ := procBrowseForFolder.Call(uintptr(unsafe.Pointer(&bi)))
	if idl == 0 {
		return "", false
	}
	defer procCoTaskMemFree.Call(idl) // адрес списка приходит как uintptr, освобождаем им же
	path := make([]uint16, windows.MAX_PATH)
	if ok, _, _ := procPathFromIDList.Call(idl, uintptr(unsafe.Pointer(&path[0]))); ok == 0 {
		return "", false
	}
	return windows.UTF16ToString(path), true
}

// freeSpace — свободное место на диске папки (или ближайшей существующей родительской).
func freeSpace(dir string) (int64, bool) {
	for {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return 0, false
		}
		dir = parent
	}
	var free, total, totalFree uint64
	r, _, _ := procDiskFreeSpaceEx.Call(uintptr(unsafe.Pointer(utf16(dir))),
		uintptr(unsafe.Pointer(&free)), uintptr(unsafe.Pointer(&total)), uintptr(unsafe.Pointer(&totalFree)))
	if r == 0 {
		return 0, false
	}
	return int64(free), true
}

func mb(bytes int64) string {
	switch {
	case bytes >= 1<<30:
		return fmt.Sprintf("%.1f ГБ", float64(bytes)/(1<<30))
	default:
		return fmt.Sprintf("%d МБ", (bytes+(1<<20)-1)/(1<<20))
	}
}

func createUIFont(height int) uintptr {
	const defaultCharset, clearType, variablePitch = 1, 5, 2
	font, _, _ := procCreateFont.Call(uintptr(-height*4/3), 0, 0, 0, 400, 0, 0, 0,
		defaultCharset, 0, 0, clearType, variablePitch,
		uintptr(unsafe.Pointer(utf16("Segoe UI"))))
	return font
}

func loadArrowCursor() windows.Handle {
	const idcArrow = 32512
	h, _, _ := user32.NewProc("LoadCursorW").Call(0, idcArrow)
	return windows.Handle(h)
}

func loadAppIcon(inst windows.Handle) windows.Handle {
	h, _, _ := user32.NewProc("LoadIconW").Call(uintptr(inst), 3) // значок 3 — тот же, что у программ FI
	return windows.Handle(h)
}
