//go:build !windows

package main

import "unsafe"

// roundCorners: скругление углов окна без рамки задаёт оконный менеджер.
func roundCorners(unsafe.Pointer) {}
