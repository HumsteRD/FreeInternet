//go:build !windows

package daemon

import (
	"os"
	"strings"
)

// machineID — постоянный идентификатор системы: переустановка FI его не меняет.
func machineID() string {
	data, err := os.ReadFile("/etc/machine-id")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
