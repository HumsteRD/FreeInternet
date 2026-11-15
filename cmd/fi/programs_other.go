//go:build !windows

package main

// На других системах окно не знает, запущена ли программа.
func runningCopies(string) []uint32 { return nil }

func closeCopies([]uint32) {}
