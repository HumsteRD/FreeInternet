package main

import (
	"log"

	"fi/internal/winsvc"
)

// syncSitesPAC держит системный PAC Windows в согласии со службой: пока есть сайты через
// воркер, браузеры спрашивают у FI, куда идти; заодно подхватывает прокси, который включил VPN.
func (t *tray) syncSitesPAC(base string) {
	msg := ""
	if err := winsvc.SyncSystemPAC(base); err != nil {
		msg = err.Error()
	}
	if msg != t.pacErr && msg != "" {
		log.Printf("системный PAC: %s", msg)
	}
	t.pacErr = msg
}
