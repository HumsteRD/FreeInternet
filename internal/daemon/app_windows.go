package daemon

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// launchInstaller запускает установщик обновления отдельно от службы: он её остановит и заменит.
func launchInstaller(path string) error {
	cmd := exec.Command(path, "/update")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
