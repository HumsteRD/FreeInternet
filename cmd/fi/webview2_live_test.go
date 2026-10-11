//go:build live && windows

// Есть ли на этом компьютере WebView2, по мнению окна:
//
//	go test -tags live -run LiveWebView2 -v ./cmd/fi
package main

import "testing"

func TestLiveWebView2(t *testing.T) {
	t.Logf("WebView2 установлен: %v", webView2Installed())
}
