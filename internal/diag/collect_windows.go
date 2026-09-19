package diag

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/mgr"

	"fi/internal/winsvc"
)

// Collect собирает сведения о системе. Сбой одной части не мешает остальным:
// что не удалось узнать, остаётся пустым, а ошибка возвращается для журнала.
func Collect() (Snapshot, error) {
	var s Snapshot
	var errs []error
	var err error
	if s.Services, err = activeServices(); err != nil {
		errs = append(errs, fmt.Errorf("службы: %w", err))
	}
	if s.Processes, err = processes(); err != nil {
		errs = append(errs, fmt.Errorf("процессы: %w", err))
	}
	hosts := filepath.Join(os.Getenv("SystemRoot"), "System32", "drivers", "etc", "hosts")
	if data, err := os.ReadFile(hosts); err == nil {
		s.Hosts = string(data[:min(len(data), 1<<20)])
	} else if !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("hosts: %w", err))
	}
	s.DoH = dohConfigured(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\Dnscache\InterfaceSpecificParameters`, 0)
	s.Timestamps = timestampsEnabled()
	if s.Adapters, s.InternetIf, err = adapters(); err != nil {
		errs = append(errs, fmt.Errorf("адаптеры: %w", err))
	}
	return s, errors.Join(errs...)
}

func activeServices() ([]Service, error) {
	scm, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_ENUMERATE_SERVICE)
	if err != nil {
		return nil, err
	}
	defer windows.CloseServiceHandle(scm)

	size := uint32(64 << 10)
	for {
		buf := make([]byte, size)
		var needed, count, resume uint32
		err := windows.EnumServicesStatusEx(scm, windows.SC_ENUM_PROCESS_INFO, windows.SERVICE_WIN32|windows.SERVICE_DRIVER,
			windows.SERVICE_ACTIVE, &buf[0], size, &needed, &count, &resume, nil)
		if errors.Is(err, windows.ERROR_MORE_DATA) {
			size += needed
			continue
		}
		if err != nil {
			return nil, err
		}
		services := make([]Service, 0, count)
		if count > 0 {
			entries := unsafe.Slice((*windows.ENUM_SERVICE_STATUS_PROCESS)(unsafe.Pointer(&buf[0])), count)
			for _, e := range entries {
				services = append(services, Service{
					Name:    windows.UTF16PtrToString(e.ServiceName),
					Display: windows.UTF16PtrToString(e.DisplayName),
				})
			}
		}
		runtime.KeepAlive(buf) // строки записей указывают внутрь буфера
		return services, nil
	}
}

func processes() ([]string, error) {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snap)
	var names []string
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		names = append(names, strings.ToLower(windows.UTF16ToString(e.ExeFile[:])))
	}
	return names, nil
}

// dohConfigured ищет DohFlags у адресов DNS: так Windows 11 хранит DNS поверх HTTPS
// для каждого подключения (…\{адаптер}\DohInterfaceSettings\Doh\{адрес}).
func dohConfigured(parent registry.Key, path string, depth int) bool {
	k, err := registry.OpenKey(parent, path, registry.READ)
	if err != nil {
		return false
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("DohFlags"); err == nil && v > 0 {
		return true
	}
	if depth >= 5 {
		return false
	}
	names, err := k.ReadSubKeyNames(-1)
	if err != nil {
		return false
	}
	for _, name := range names {
		if dohConfigured(k, name, depth+1) {
			return true
		}
	}
	return false
}

// timestampsEnabled читает Tcp1323Opts (бит 2 — метки времени). Если значения нет, спрашивает netsh;
// не удалось узнать — считаем включёнными, чтобы не пугать зря.
func timestampsEnabled() bool {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`, registry.QUERY_VALUE)
	if err == nil {
		v, _, err := k.GetIntegerValue("Tcp1323Opts")
		k.Close()
		if err == nil {
			return v&2 != 0
		}
	}
	out, err := hiddenCommand("netsh", "interface", "tcp", "show", "global").Output()
	if err != nil {
		return true
	}
	return parseTimestamps(string(out))
}

// parseTimestamps ищет в выводе netsh строку про RFC 1323: подпись бывает переведена, номер — нет.
func parseTimestamps(out string) bool {
	for line := range strings.Lines(out) {
		if !strings.Contains(line, "1323") {
			continue
		}
		_, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "disabled", "отключено", "отключен", "выключено", "выключен":
			return false
		}
	}
	return true
}

func adapters() ([]Adapter, uint32, error) {
	size := uint32(16 << 10)
	var buf []byte
	for {
		buf = make([]byte, size)
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC,
			windows.GAA_FLAG_SKIP_ANYCAST|windows.GAA_FLAG_SKIP_MULTICAST|windows.GAA_FLAG_SKIP_DNS_SERVER,
			0, (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])), &size)
		if errors.Is(err, windows.ERROR_BUFFER_OVERFLOW) {
			continue
		}
		if err != nil {
			return nil, 0, err
		}
		break
	}
	var list []Adapter
	for a := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0])); a != nil; a = a.Next {
		if a.OperStatus != windows.IfOperStatusUp {
			continue
		}
		list = append(list, Adapter{
			Name:        windows.UTF16PtrToString(a.FriendlyName),
			Description: windows.UTF16PtrToString(a.Description),
			Index:       a.IfIndex,
		})
	}
	runtime.KeepAlive(buf)

	// Какой адаптер система выбрала бы для публичного адреса: пакеты при этом не отправляются.
	var index uint32
	if err := windows.GetBestInterfaceEx(&windows.SockaddrInet4{Addr: [4]byte{8, 8, 8, 8}}, &index); err != nil {
		index = 0
	}
	return list, index, nil
}

// CollectUser читает настройки прокси текущего пользователя.
func CollectUser() UserSnapshot {
	var u UserSnapshot
	k, err := registry.OpenKey(registry.CURRENT_USER, `Software\Microsoft\Windows\CurrentVersion\Internet Settings`, registry.QUERY_VALUE)
	if err != nil {
		return u
	}
	defer k.Close()
	if v, _, err := k.GetIntegerValue("ProxyEnable"); err == nil {
		u.ProxyEnabled = v == 1
	}
	u.ProxyServer, _, _ = k.GetStringValue("ProxyServer")
	u.AutoConfig, _, _ = k.GetStringValue("AutoConfigURL")
	return u
}

// EnableTimestamps включает метки времени TCP — так же, как это делает service.bat Flowseal.
func EnableTimestamps() error {
	if err := hiddenCommand("netsh", "interface", "tcp", "set", "global", "timestamps=enabled").Run(); err != nil {
		return fmt.Errorf("netsh не включил метки времени: %w", err)
	}
	return nil
}

// StartBFE запускает службу базовой фильтрации.
func StartBFE() error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService("BFE")
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return fmt.Errorf("служба BFE не запустилась: %w", err)
	}
	return nil
}

// UnloadWinDivert выгружает драйвер WinDivert, который оставил другой обход. Движок FI
// при запуске ставит драйвер заново, поэтому удалять его службу безопасно.
func UnloadWinDivert() error {
	names, err := processes()
	if err != nil {
		return err
	}
	for _, p := range []string{"winws.exe", "winws2.exe", "goodbyedpi.exe"} {
		if (Snapshot{Processes: names}).processCount(p) > 0 {
			return fmt.Errorf("драйвер занят: работает %s", p)
		}
	}
	var errs []error
	for _, name := range []string{"WinDivert", "WinDivert14"} {
		if err := winsvc.StopDriver(name); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// bypassServiceNames — службы других обходов так, как их называют установщики.
var bypassServiceNames = []string{"zapret", "winws1", "winws2", "GoodbyeDPI", "discordfix_zapret"}

// StopBypass останавливает другие обходы. Службы переводятся на ручной запуск, а прежний режим
// запоминается — при удалении FI его можно вернуть. Процессы закрываются, кроме движка
// FI (own). Возвращает, что остановлено.
func StopBypass(own uint32) ([]string, error) {
	var stopped []string
	var errs []error
	for _, name := range bypassServiceNames {
		start, ok := winsvc.StartType(name)
		if !ok || (!winsvc.Running(name) && start != mgr.StartAutomatic) {
			continue // службы нет, или она остановлена и сама не запустится
		}
		if err := winsvc.Disable(name); err != nil {
			errs = append(errs, fmt.Errorf("служба %s: %w", name, err))
			continue
		}
		if start == mgr.StartAutomatic {
			// Если FI запущен не из установщика, записи удаления нет — тогда автозапуск возвращают вручную.
			winsvc.Remember(name, start)
		}
		stopped = append(stopped, "служба "+name)
	}
	for _, p := range []string{"winws.exe", "winws2.exe", "goodbyedpi.exe"} {
		if winsvc.KillProcesses(p, own) > 0 {
			stopped = append(stopped, "процесс "+p)
		}
	}
	return stopped, errors.Join(errs...)
}

func hiddenCommand(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return cmd
}

// InternetVPN — VPN-адаптер, через который сейчас идёт интернет; пусто — напрямую или узнать не удалось.
func InternetVPN() string {
	list, index, err := adapters()
	if err != nil {
		return ""
	}
	if a := vpnAdapter(list, index); a != nil {
		return a.Name
	}
	return ""
}
