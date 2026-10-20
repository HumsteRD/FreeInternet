package daemon

import (
	"fi/internal/probe"
)

// DNS роутера или провайдера. Проверки FI, не получив ответа от системного DNS, берут адрес
// через DoH и идут дальше — иначе случайная задержка DNS выглядела бы как блокировка. Но браузер
// спрашивает системный DNS и сайт не откроет: FI говорил «всё работает», а YouTube не грузился.
// Теперь, если системный DNS молчит для многих сайтов, окно говорит об этом и предлагает
// переключиться на публичный DNS.

// dnsBrokenMin — для скольких сайтов системный DNS должен промолчать, чтобы считать его сломанным,
// а не случайно задумавшимся.
const dnsBrokenMin = 3

// dnsBroken — системный DNS не ответил для многих сайтов из проверки.
func dnsBroken(results []probe.Result) bool {
	n := 0
	for _, r := range results {
		if r.SysDNSFailed {
			n++
		}
	}
	return n >= dnsBrokenMin
}

// noteDNS запоминает, отвечает ли системный DNS, и пишет в журнал, когда это меняется.
func (d *Daemon) noteDNS(results []probe.Result) {
	broken := dnsBroken(results)
	d.mu.Lock()
	changed := broken != d.dnsBroken
	d.dnsBroken = broken
	d.mu.Unlock()
	switch {
	case !changed:
	case broken:
		d.log.Warn("системный DNS (роутер или провайдер) не отвечает — браузер может не открывать сайты, хотя проверки FI проходят")
	default:
		d.log.Info("системный DNS снова отвечает")
	}
}

// publicDNS — DNS, на который FI переключает обычный сетевой адаптер по кнопке в окне.
var publicDNS = []string{"1.1.1.1", "77.88.8.8"}

// SetPublicDNS переключает обычный адаптер на публичный DNS (on) или возвращает DNS от роутера.
func (d *Daemon) SetPublicDNS(on bool) error {
	servers := publicDNS
	if !on {
		servers = nil // снова от роутера по DHCP
	}
	if err := setAdapterDNS(servers); err != nil {
		return err
	}
	d.mu.Lock()
	d.cfg.PublicDNS = on
	err := d.saveLocked()
	d.mu.Unlock()
	if on {
		d.log.Info("DNS обычного адаптера переключён на публичный", "servers", servers)
	} else {
		d.log.Info("DNS обычного адаптера снова от роутера")
	}
	d.recheckSoon()
	return err
}
