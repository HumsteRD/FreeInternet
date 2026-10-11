//go:build live && windows

// Состояние работающей службы — как его видит окно:
//
//	go test -tags live -run LiveStatus -v ./internal/ipc
package ipc

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLiveStatus(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := Dial(ctx)
	if err != nil {
		t.Skipf("служба не отвечает: %v", err)
	}
	var st map[string]json.RawMessage
	if err := Call(ctx, conn, "status", nil, &st); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"vpn", "checked_at", "task", "telegram", "services", "error"} {
		t.Logf("%s: %s", k, st[k])
	}
}
