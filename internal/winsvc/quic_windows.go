package winsvc

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

// browserPolicyKeys — политики браузеров на Chromium: в каждом свой ключ, значение QuicAllowed общее.
var browserPolicyKeys = []string{
	`SOFTWARE\Policies\Google\Chrome`,
	`SOFTWARE\Policies\Microsoft\Edge`,
	`SOFTWARE\Policies\BraveSoftware\Brave`,
	`SOFTWARE\Policies\YandexBrowser`,
	`SOFTWARE\Policies\Chromium`,
}

// SetBrowserQUIC разрешает или запрещает браузерам QUIC (HTTP/3). Без QUIC YouTube и другие
// сайты Google идут по TCP, который обход держит надёжнее. Разрешая, FI убирает только свой
// запрет (QuicAllowed=0), чужие настройки не трогает.
func SetBrowserQUIC(allowed bool) error {
	var errs []error
	for _, path := range browserPolicyKeys {
		if allowed {
			k, err := registry.OpenKey(registry.LOCAL_MACHINE, path, registry.QUERY_VALUE|registry.SET_VALUE)
			if err != nil {
				continue // ключа нет — и запрета нет
			}
			if v, _, err := k.GetIntegerValue("QuicAllowed"); err == nil && v == 0 {
				errs = append(errs, k.DeleteValue("QuicAllowed"))
			}
			k.Close()
			continue
		}
		k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, path, registry.SET_VALUE)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, k.SetDWordValue("QuicAllowed", 0))
		k.Close()
	}
	return errors.Join(errs...)
}
