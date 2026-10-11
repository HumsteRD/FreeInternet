//go:build live

// Подписанный манифест из build\ проходит ту же проверку, что в установленном FI:
//
//	$env:FI_UPDATE_PUBLIC_KEY="…"; go test -tags live -run LiveSigned -v ./internal/appupdate
package appupdate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestLiveSignedManifest(t *testing.T) {
	pub := os.Getenv("FI_UPDATE_PUBLIC_KEY")
	if pub == "" {
		t.Skip("FI_UPDATE_PUBLIC_KEY не задан")
	}
	srv := httptest.NewServer(http.FileServer(http.Dir("../../build")))
	defer srv.Close()
	m, err := check(context.Background(), srv.Client(), srv.URL+"/update.json", pub)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("подпись верна: версия %s, %d байт, %s", m.Version, m.Size, m.URL)
}
