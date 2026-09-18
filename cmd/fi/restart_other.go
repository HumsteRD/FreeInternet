//go:build !windows

package main

import "time"

func waitForProcess(uint32, time.Duration) {}
