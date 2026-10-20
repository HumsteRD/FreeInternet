//go:build live && windows

// Вызвать метод работающей службы, как это делает окно:
//
//	$env:FI_CALL="remove_via_worker"; $env:FI_PARAMS='{"host":"soundcloud.com"}'; go test -tags live -run LiveCall -v ./internal/ipc
package ipc

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestLiveCall(t *testing.T) {
	method := os.Getenv("FI_CALL")
	if method == "" {
		t.Skip("FI_CALL не задан")
	}
	var params any
	if p := os.Getenv("FI_PARAMS"); p != "" {
		params = json.RawMessage(p)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := Dial(ctx)
	if err != nil {
		t.Fatalf("служба не отвечает: %v", err)
	}
	var result map[string]json.RawMessage
	if err := Call(ctx, conn, method, params, &result); err != nil {
		t.Fatal(err)
	}
	t.Logf("via_worker после вызова: %s", result["via_worker"])
}
