//go:build !windows

package main

func webView2Installed() bool { return true }

func offerWebView2() bool { return false }
