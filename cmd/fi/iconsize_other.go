//go:build !windows

package main

// trayIconSize — типичный размер значка в панелях Linux.
func trayIconSize() int { return 22 }
