package winsvc

import (
	"errors"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const internetSettingsKey = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

// ErrForeignPAC — у пользователя уже свой файл PAC (например, рабочий): FI его не заменяет.
var ErrForeignPAC = errors.New("в Windows уже задан другой файл автонастройки прокси — сайты через воркер в браузерах не включены")

// SyncSystemPAC ставит AutoConfigURL текущего пользователя на PAC службы base (пусто —
// убирает свой). Вызывается окном FI регулярно: следит и за прокси, который включает VPN.
func SyncSystemPAC(base string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	cur, _, _ := k.GetStringValue("AutoConfigURL")
	manual := ""
	if on, _, err := k.GetIntegerValue("ProxyEnable"); err == nil && on != 0 {
		server, _, _ := k.GetStringValue("ProxyServer")
		manual = parseProxyServer(server)
	}
	want, action := nextPAC(cur, base, manual)
	switch action {
	case pacForeign:
		return ErrForeignPAC
	case pacSet:
		err = k.SetStringValue("AutoConfigURL", want)
	case pacRemove:
		err = k.DeleteValue("AutoConfigURL")
	default:
		return nil
	}
	notifyProxyChanged()
	return err
}

// CurrentPAC — сценарий настройки прокси, заданный в Windows сейчас.
func CurrentPAC() string {
	k, err := registry.OpenKey(registry.CURRENT_USER, internetSettingsKey, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, _ := k.GetStringValue("AutoConfigURL")
	return v
}

// RemoveSystemPAC убирает PAC FI — при удалении программы.
func RemoveSystemPAC() error { return SyncSystemPAC("") }

var (
	wininet            = windows.NewLazySystemDLL("wininet.dll")
	internetSetOptionW = wininet.NewProc("InternetSetOptionW")
)

// notifyProxyChanged сообщает программам, что настройки прокси изменились.
func notifyProxyChanged() {
	const settingsChanged, refresh = 39, 37 // INTERNET_OPTION_SETTINGS_CHANGED, INTERNET_OPTION_REFRESH
	if internetSetOptionW.Find() != nil {
		return
	}
	internetSetOptionW.Call(0, settingsChanged, 0, 0)
	internetSetOptionW.Call(0, refresh, 0, 0)
}
