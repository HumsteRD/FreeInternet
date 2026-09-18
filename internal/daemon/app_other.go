//go:build !windows

package daemon

import "errors"

func launchInstaller(string) error {
	return errors.New("обновление пока есть только для Windows")
}
