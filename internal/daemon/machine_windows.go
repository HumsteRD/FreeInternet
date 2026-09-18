package daemon

import "golang.org/x/sys/windows/registry"

// machineID — постоянный идентификатор установки Windows: переустановка FI его не меняет.
func machineID() string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\Cryptography`, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if err != nil {
		return ""
	}
	defer k.Close()
	id, _, err := k.GetStringValue("MachineGuid")
	if err != nil {
		return ""
	}
	return id
}
