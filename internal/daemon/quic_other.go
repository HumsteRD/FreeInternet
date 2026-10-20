//go:build !windows

package daemon

var setBrowserQUIC = func(bool) error { return nil }
