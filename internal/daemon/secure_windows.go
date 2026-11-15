package daemon

import (
	"golang.org/x/sys/windows"

	"fi/internal/engine"
)

// dataDirSDDL: система и администраторы — полный доступ, пользователи — только чтение.
// Наследование от ProgramData отключено, иначе пользователь мог бы подложить файлы,
// которые служба запустит от имени системы.
const dataDirSDDL = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FRFX;;;BU)"

// secureDir закрывает папку данных от записи обычными пользователями.
// Без прав администратора ничего не делает: так работают тесты и отладка.
func secureDir(dir string) error {
	if !engine.IsElevated() {
		return nil
	}
	sd, err := windows.SecurityDescriptorFromString(dataDirSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}

// secretsSDDL: только система и администраторы. В папке данных пользователи могут читать всё,
// а токен Cloudflare читать им незачем: с ним можно менять воркеры в чужом аккаунте.
const secretsSDDL = "D:PAI(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)"

// secureSecrets закрывает папку с секретами от всех, кроме системы и администраторов.
func secureSecrets(dir string) error {
	if !engine.IsElevated() {
		return nil
	}
	sd, err := windows.SecurityDescriptorFromString(secretsSDDL)
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(dir, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil)
}
