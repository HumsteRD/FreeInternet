//go:build !windows

package daemon

var setAdapterDNS = func([]string) error { return nil }
