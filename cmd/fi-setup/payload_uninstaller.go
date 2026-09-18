//go:build windows && uninstaller

package main

import "embed"

// Программа удаления собирается без вшитых программ и весит несколько мегабайт.
var payload embed.FS

const isUninstaller = true
