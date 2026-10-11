package winsvc

import (
	"errors"
	"slices"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// webView2ArgsKey — политика WebView2: дополнительные аргументы встроенного браузера для
// программы с этим именем файла. Аргументы дописываются к тем, что задаёт сама программа.
const webView2ArgsKey = `SOFTWARE\Policies\Microsoft\Edge\WebView2\AdditionalBrowserArguments`

// TelegramExes — клиенты Telegram Desktop, у которых FI настраивает окна мини-приложений.
var TelegramExes = []string{"Telegram.exe", "AyuGram.exe", "Kotatogram.exe", "64Gram.exe", "materialgram.exe", "iMe.exe"}

// pacFlag — аргумент, которым FI отдаёт окнам мини-приложений свой файл PAC.
const pacFlag = "--proxy-pac-url="

// SetTelegramPAC задаёт окнам мини-приложений Telegram файл PAC; пустой адрес убирает его.
// Чужие аргументы в тех же значениях сохраняются.
func SetTelegramPAC(pacURL string) error {
	k, _, err := registry.CreateKey(registry.LOCAL_MACHINE, webView2ArgsKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		if pacURL == "" && errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()
	var errs []error
	for _, exe := range TelegramExes {
		old, _, err := k.GetStringValue(exe)
		if err != nil && !errors.Is(err, registry.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		value := withPAC(old, pacURL)
		switch {
		case value == old:
		case value == "":
			errs = append(errs, k.DeleteValue(exe))
		default:
			errs = append(errs, k.SetStringValue(exe, value))
		}
	}
	return errors.Join(errs...)
}

// withPAC убирает из аргументов прежний PAC от FI и добавляет новый, если он задан.
// Чужой --proxy-pac-url (не на 127.0.0.1) не трогается, и тогда свой не добавляется.
func withPAC(args, pacURL string) string {
	fields := strings.Fields(args)
	fields = slices.DeleteFunc(fields, isOurPAC)
	foreign := slices.ContainsFunc(fields, func(f string) bool { return strings.HasPrefix(f, pacFlag) })
	if pacURL != "" && !foreign {
		fields = append(fields, pacFlag+pacURL)
	}
	return strings.Join(fields, " ")
}

func isOurPAC(f string) bool {
	return strings.HasPrefix(f, pacFlag+"http://127.0.0.1:") && strings.HasSuffix(f, "/telegram.pac")
}
