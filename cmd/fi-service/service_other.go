//go:build !windows

package main

import "errors"

var errNoServiceManager = errors.New("установка службы пока есть только для Windows; в Linux запустите fi-service run через systemd")

func defaultDataDir() string { return "/var/lib/fi" }

func runningAsService() bool { return false }

func installService() error { return errNoServiceManager }

func uninstallService() error { return errNoServiceManager }

func serviceRunning() bool { return false }
