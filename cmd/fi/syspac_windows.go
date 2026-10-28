package main

import (
	"log"

	"fi/internal/winsvc"
)

// pacGoneChecks — сколько опросов подряд PAC FI должен отсутствовать без прокси VPN, чтобы
// считать, что его убрал человек: VPN-клиент при подключении сначала стирает PAC и лишь
// через мгновение включает свой прокси.
const pacGoneChecks = 2

// syncSitesPAC держит системный PAC Windows в согласии со службой: пока включены сайты через
// воркер в браузерах, браузеры спрашивают у FI, куда идти. Когда VPN-клиент включает свой
// системный прокси, браузеры ведёт он, а FI ждёт, пока VPN выключат. Если же человек сам убрал
// сценарий в параметрах Windows, FI его не возвращает, а выключает функцию: решает человек.
func (t *tray) syncSitesPAC(base string) {
	s := loadTraySettings()
	if base != "" && s.SitesPACSet && winsvc.UserRemovedPAC(winsvc.CurrentPAC()) {
		if t.pacGone++; t.pacGone < pacGoneChecks {
			return
		}
		t.pacGone = 0
		s.SitesPACSet = false
		saveTraySettings(s)
		log.Print("сценарий прокси FI убран в параметрах Windows — сайты через воркер в браузерах выключены")
		go t.command("set_via_worker_browsers", map[string]bool{"on": false})
		t.toast("Сайты через воркер в браузерах выключены", "Вы убрали сценарий прокси FI в параметрах Windows — FI его больше не ставит. Включить снова: Настройки → «Сайты через воркер».")
		return
	}
	t.pacGone = 0
	msg := ""
	if err := winsvc.SyncSystemPAC(base); err != nil {
		msg = err.Error()
	}
	if msg != t.pacErr && msg != "" {
		log.Printf("системный PAC: %s", msg)
	}
	t.pacErr = msg
	// Помним, стоит ли PAC FI на самом деле: пока работает VPN, его нет, и пропажей это не считается.
	pac, _ := winsvc.CurrentPAC()
	if set := winsvc.IsOurSitesPAC(pac); set != s.SitesPACSet {
		s.SitesPACSet = set
		saveTraySettings(s)
	}
}
