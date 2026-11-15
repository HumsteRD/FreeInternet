package main

import (
	"log"
	"time"

	"fi/internal/winsvc"
)

// syncSitesPAC держит системный PAC Windows в согласии со службой: пока включены сайты через
// воркер в браузерах, браузеры спрашивают у FI, куда идти. Когда включён VPN — с системным
// прокси или с туннелем, — браузеры ведёт он: служба сценарий не просит, и FI его убирает.
// VPN выключили — сценарий возвращается. Пропавший сценарий FI ставит снова (его стирают
// VPN-клиенты), а функцию выключает, только если сценарий убирают раз за разом.
func (t *tray) syncSitesPAC(base string) {
	s := loadTraySettings()
	cur, manual := winsvc.CurrentPAC()
	switch t.pac.Decide(time.Now(), base, cur, manual, s.SitesPACSet) {
	case winsvc.PACWait:
		return
	case winsvc.PACRestore:
		log.Print("сценарий прокси FI пропал из параметров Windows (его стирают VPN-клиенты) — возвращён")
	case winsvc.PACGiveUp:
		s.SitesPACSet = false
		saveTraySettings(s)
		log.Print("сценарий прокси FI убирают раз за разом — сайты через воркер в браузерах выключены")
		go t.command("set_via_worker_browsers", map[string]bool{"on": false})
		t.toast("Сайты через воркер в браузерах выключены", "Сценарий прокси FI в параметрах Windows несколько раз убирали — FI его больше не ставит. Включить снова: Настройки → Cloudflare.")
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
	// Помним, стоит ли PAC FI на самом деле: пока работает VPN, его нет, и пропажей это не считается.
	pac, _ := winsvc.CurrentPAC()
	if set := winsvc.IsOurSitesPAC(pac); set != s.SitesPACSet {
		s.SitesPACSet = set
		saveTraySettings(s)
	}
}
