package main

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// webView2Client — код WebView2 Runtime в EdgeUpdate.
const webView2Client = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`

// webView2DownloadURL — загрузчик WebView2 Runtime с сайта Microsoft.
const webView2DownloadURL = "https://go.microsoft.com/fwlink/p/?LinkId=2124703"

// webView2Installed — есть ли WebView2 Runtime, без которого окно не открывается. Одной записи
// в реестре мало: программы «очистки» Windows удаляют файлы, а запись оставляют.
func webView2Installed() bool {
	type place struct {
		root registry.Key
		key  string
		dir  string // где лежат версии
	}
	x86 := os.Getenv("ProgramFiles(x86)")
	places := []place{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + webView2Client, filepath.Join(x86, `Microsoft\EdgeWebView\Application`)},
		{registry.LOCAL_MACHINE, `SOFTWARE\` + webView2Client, filepath.Join(x86, `Microsoft\EdgeWebView\Application`)},
		{registry.CURRENT_USER, `Software\` + webView2Client, filepath.Join(os.Getenv("LOCALAPPDATA"), `Microsoft\EdgeWebView\Application`)},
	}
	for _, p := range places {
		k, err := registry.OpenKey(p.root, p.key, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		version, _, err := k.GetStringValue("pv")
		k.Close()
		if err != nil || version == "" || version == "0.0.0.0" {
			continue
		}
		// Во время обновления запись указывает на новую версию раньше, чем появится папка, —
		// годится любая установленная версия.
		if found, _ := filepath.Glob(filepath.Join(p.dir, "*", "msedgewebview2.exe")); len(found) > 0 {
			return true
		}
	}
	return false
}

// offerWebView2 объясняет, почему окна не будет, и предлагает скачать WebView2. true — человек
// согласился, ссылка открыта в браузере.
func offerWebView2() bool {
	text := "Окну FI нужен компонент Microsoft Edge WebView2 Runtime, а на этом компьютере его нет — " +
		"его часто удаляют программы «очистки» Windows.\n\n" +
		"Обход и прокси для Telegram работают и без окна.\n\n" +
		"Скачать WebView2 с сайта Microsoft? После установки запустите FI снова."
	title, _ := windows.UTF16PtrFromString("FI")
	body, _ := windows.UTF16PtrFromString(text)
	answer, _ := windows.MessageBox(0, body, title, windows.MB_YESNO|windows.MB_ICONWARNING|windows.MB_SETFOREGROUND)
	if answer != 6 { // IDYES
		return false
	}
	url, _ := windows.UTF16PtrFromString(webView2DownloadURL)
	open, _ := windows.UTF16PtrFromString("open")
	return windows.ShellExecute(0, open, url, nil, nil, windows.SW_SHOWNORMAL) == nil
}
