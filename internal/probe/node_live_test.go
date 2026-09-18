//go:build live

// Живая проверка узла YouTube:
//
//	go test -tags live -run LiveYouTubeNode -v ./internal/probe
package probe

import (
	"context"
	"net/http"
	"testing"
)

func TestLiveYouTubeNode(t *testing.T) {
	host, err := FindYouTubeNode(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("видеосервер сети: %s", host)
	for _, target := range WithYouTubeNode(DefaultTargets(), host) {
		if target.Host != host {
			continue
		}
		r := New().Check(context.Background(), target)
		if r.Status != OK {
			t.Fatalf("%s: %s %s", target.Name, r.Status, r.Detail)
		}
		t.Logf("%s: %s, HTTP %d, отправлено %d КБ за %v", target.Name, r.Status, r.HTTPCode, r.Bytes>>10, r.Duration)
	}
}
