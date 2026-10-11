//go:build !windows

package daemon

var setTelegramPAC = func(string) error { return nil }
