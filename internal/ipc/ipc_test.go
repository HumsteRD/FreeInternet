package ipc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"testing"
)

func TestCall(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go Serve(ctx, l, func(_ context.Context, method string, params json.RawMessage) (any, error) {
		if method != "echo" {
			return nil, errors.New("нет такого метода")
		}
		var p map[string]string
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, err
		}
		return p, nil
	})
	dial := func() net.Conn {
		c, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		return c
	}

	var got map[string]string
	if err := Call(ctx, dial(), "echo", map[string]string{"a": "б"}, &got); err != nil || got["a"] != "б" {
		t.Fatalf("echo = %v, %v", got, err)
	}

	err = Call(ctx, dial(), "missing", nil, nil)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Message != "нет такого метода" {
		t.Fatalf("err = %v", err)
	}
}
