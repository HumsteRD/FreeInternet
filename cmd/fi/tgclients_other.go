//go:build !windows

package main

// findTelegramClients на других системах знает только выбранные вручную программы.
func findTelegramClients(extra ...string) []telegramClient { return collectClients(extra) }
