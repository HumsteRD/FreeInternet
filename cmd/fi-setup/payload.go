//go:build windows && !uninstaller

package main

import "embed"

// payload заполняет scripts/build.ps1: fi-service.exe, fi.exe и uninstall.exe.
//
//go:embed payload
var payload embed.FS

// isUninstaller: этот файл — установщик.
const isUninstaller = false
