// Package winsvc — службы и процессы Windows для установщика и службы FI. Другие обходы
// здесь останавливаются одинаково: служба переводится на ручной запуск, а прежний режим
// запоминается в записи удаления FI, чтобы при удалении его можно было вернуть.
package winsvc

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const (
	// UninstallKey — запись FI в «Установленных приложениях».
	UninstallKey = `SOFTWARE\Microsoft\Windows\CurrentVersion\Uninstall\FI`
	// RestorePrefix — значение в записи удаления: служба другого обхода и её прежний режим запуска.
	RestorePrefix = "RestoreService_"
)

// Open открывает службу; закрывать — Close.
func Open(name string) (*mgr.Mgr, *mgr.Service, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, nil, err
	}
	s, err := m.OpenService(name)
	if err != nil {
		m.Disconnect()
		return nil, nil, err
	}
	return m, s, nil
}

func Close(m *mgr.Mgr, s *mgr.Service) {
	s.Close()
	m.Disconnect()
}

// StartType — режим запуска службы; false — службы нет.
func StartType(name string) (uint32, bool) {
	m, s, err := Open(name)
	if err != nil {
		return 0, false
	}
	defer Close(m, s)
	cfg, err := s.Config()
	return cfg.StartType, err == nil
}

func Running(name string) bool {
	m, s, err := Open(name)
	if err != nil {
		return false
	}
	defer Close(m, s)
	st, err := s.Query()
	return err == nil && st.State != svc.Stopped
}

// Stop останавливает службу и ждёт, пока она действительно остановится.
func Stop(s *mgr.Service) error {
	st, err := s.Query()
	if err != nil || st.State == svc.Stopped {
		return err
	}
	if st.State != svc.StopPending {
		if _, err := s.Control(svc.Stop); err != nil {
			return err
		}
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); time.Sleep(300 * time.Millisecond) {
		if st, err := s.Query(); err == nil && st.State == svc.Stopped {
			return nil
		}
	}
	return errors.New("служба не остановилась за 30 секунд")
}

// StopByName останавливает службу, если она есть.
func StopByName(name string) {
	if m, s, err := Open(name); err == nil {
		Stop(s)
		Close(m, s)
	}
}

// Disable останавливает службу и переводит её на ручной запуск.
func Disable(name string) error {
	m, s, err := Open(name)
	if err != nil {
		return err
	}
	defer Close(m, s)
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	if cfg.StartType != mgr.StartManual && cfg.StartType != mgr.StartDisabled {
		cfg.StartType = mgr.StartManual
		if err := s.UpdateConfig(cfg); err != nil {
			return err
		}
	}
	return Stop(s)
}

// Enable возвращает службе прежний режим запуска и запускает её, если она была автоматической.
func Enable(name string, start uint32) error {
	m, s, err := Open(name)
	if err != nil {
		return err
	}
	defer Close(m, s)
	cfg, err := s.Config()
	if err != nil {
		return err
	}
	cfg.StartType = start
	if err := s.UpdateConfig(cfg); err != nil {
		return err
	}
	if start == mgr.StartAutomatic {
		return s.Start()
	}
	return nil
}

// Delete останавливает и удаляет службу; если службы нет — ничего не делает.
func Delete(name string) error {
	m, s, err := Open(name)
	if err != nil {
		return nil
	}
	defer Close(m, s)
	if err := Stop(s); err != nil {
		return err
	}
	return s.Delete()
}

// StopDriver останавливает и удаляет службу драйвера, например WinDivert: пока драйвер загружен,
// его файл нельзя удалить. Нет такой службы — делать нечего. winws ставит драйвер заново сам.
func StopDriver(name string) error {
	m, s, err := Open(name)
	if err != nil {
		return nil
	}
	defer Close(m, s)
	if err := Stop(s); err != nil {
		return fmt.Errorf("%s не остановлен: %w", name, err)
	}
	if err := s.Delete(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_MARKED_FOR_DELETE) {
		return fmt.Errorf("%s не удалён: %w", name, err)
	}
	return nil
}

// RemoveLater помечает файл или папку к удалению при следующей перезагрузке — так убирается то,
// что занято загруженным драйвером. Нужны права администратора.
func RemoveLater(path string) error {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(p, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
}

// RemoveTree удаляет папку целиком. Занятое помечается к удалению при перезагрузке;
// pending — что-то осталось до неё.
func RemoveTree(dir string) (pending bool, err error) {
	if err := os.RemoveAll(dir); err == nil {
		return false, nil
	}
	var files, dirs []string
	filepath.WalkDir(dir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if e.IsDir() {
			dirs = append(dirs, path)
		} else {
			files = append(files, path)
		}
		return nil
	})
	slices.Reverse(dirs) // вложенные папки удаляются раньше внешних
	var errs []error
	for _, path := range append(files, dirs...) {
		if os.Remove(path) == nil {
			continue
		}
		if err := RemoveLater(path); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path, err))
		} else {
			pending = true
		}
	}
	return pending, errors.Join(errs...)
}

// Remember запоминает прежний режим запуска службы в записи удаления FI. Уже запомненный
// режим не перезаписывается: он исходный. Если FI не установлен установщиком, записи нет
// и возвращается ошибка — автозапуск тогда возвращают вручную.
func Remember(name string, start uint32) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, UninstallKey, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if _, _, err := k.GetIntegerValue(RestorePrefix + name); err == nil {
		return nil
	}
	return k.SetDWordValue(RestorePrefix+name, start)
}

// RestoreEntries — отключённые службы других обходов и их прежний режим.
func RestoreEntries() map[string]uint32 {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, UninstallKey, registry.QUERY_VALUE)
	if err != nil {
		return nil
	}
	defer k.Close()
	names, err := k.ReadValueNames(0)
	if err != nil {
		return nil
	}
	entries := map[string]uint32{}
	for _, n := range names {
		if service, ok := strings.CutPrefix(n, RestorePrefix); ok {
			if v, _, err := k.GetIntegerValue(n); err == nil {
				entries[service] = uint32(v)
			}
		}
	}
	return entries
}

// KillProcesses закрывает процессы с таким именем файла, кроме процесса except, и возвращает,
// сколько закрыл.
func KillProcesses(name string, except uint32) int {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(snap)
	killed := 0
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		if e.ProcessID == except || !strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), name) {
			continue
		}
		h, err := windows.OpenProcess(windows.PROCESS_TERMINATE|windows.SYNCHRONIZE, false, e.ProcessID)
		if err != nil {
			continue
		}
		if windows.TerminateProcess(h, 0) == nil {
			killed++
		}
		windows.WaitForSingleObject(h, 3000)
		windows.CloseHandle(h)
	}
	return killed
}
