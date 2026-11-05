//go:build !windows

package main

import "unsafe"

// roundCorners: скругление углов окна без рамки задаёт оконный менеджер.
func roundCorners(unsafe.Pointer) {}

// placeAtTray: окно ставит оконный менеджер.
func placeAtTray(unsafe.Pointer) bool { return false }
