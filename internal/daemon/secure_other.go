//go:build !windows

package daemon

import "os"

// secureDir оставляет запись в папку данных только владельцу (службе от root).
func secureDir(dir string) error {
	return os.Chmod(dir, 0o755)
}
