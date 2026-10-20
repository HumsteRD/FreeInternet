package main

import (
	"log"

	"fi/internal/winsvc"
)

// syncSitesPAC держит системный PAC Windows в согласии со службой: пока включены сайты через
// воркер в браузерах, браузеры спрашивают у FI, куда идти; заодно подхватывает прокси, который
// включил VPN. Если человек сам убрал сценарий в параметрах Windows, FI его не возвращает,
// а выключает функцию: решает человек.
func (t *tray) syncSitesPAC(base string) {
	s := loadTraySettings()
	if base != "" && s.SitesPACSet && !winsvc.IsOurSitesPAC(winsvc.CurrentPAC()) {
		s.SitesPACSet = false
		saveTraySettings(s)
		log.Print("сценарий прокси FI убран в параметрах Windows — сайты через воркер в браузерах выключены")
		go t.command("set_via_worker_browsers", map[string]bool{"on": false})
		t.toast("Сайты через воркер в браузерах выключены", "Вы убрали сценарий прокси FI в параметрах Windows — FI его больше не ставит. Включить снова: Настройки → «Сайты через воркер».")
		return
	}
	msg := ""
	if err := winsvc.SyncSystemPAC(base); err != nil {
		msg = err.Error()
	}
	if msg != t.pacErr && msg != "" {
		log.Printf("системный PAC: %s", msg)
	}
	t.pacErr = msg
	if set := base != "" && msg == ""; set != s.SitesPACSet {
		s.SitesPACSet = set
		saveTraySettings(s)
	}
}
