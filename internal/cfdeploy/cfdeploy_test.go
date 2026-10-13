package cfdeploy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeAPI — поддельный API Cloudflare: аккаунт без поддомена workers.dev.
func fakeAPI(t *testing.T, token string) (*httptest.Server, *map[string]string) {
	t.Helper()
	got := map[string]string{}
	ok := func(w http.ResponseWriter, result any) {
		data, _ := json.Marshal(result)
		w.Write([]byte(`{"success":true,"errors":[],"result":` + string(data) + `}`))
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/accounts":
			ok(w, []map[string]string{{"id": "acc1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/accounts/acc1/workers/subdomain":
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"success":false,"errors":[{"code":10007,"message":"This subdomain does not exist"}]}`))
		case r.Method == http.MethodPut && r.URL.Path == "/accounts/acc1/workers/subdomain":
			body, _ := io.ReadAll(r.Body)
			got["subdomain"] = string(body)
			var in map[string]string
			json.Unmarshal(body, &in)
			ok(w, in)
		case r.Method == http.MethodPut && r.URL.Path == "/accounts/acc1/workers/scripts/fi-tg":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				t.Errorf("multipart: %v", err)
			}
			got["metadata"] = r.FormValue("metadata")
			f, h, err := r.FormFile("worker.js")
			if err == nil {
				code, _ := io.ReadAll(f)
				got["code"], got["code_type"] = string(code), h.Header.Get("Content-Type")
			}
			ok(w, map[string]string{"id": "fi-tg"})
		case r.Method == http.MethodPost && r.URL.Path == "/accounts/acc1/workers/scripts/fi-tg/subdomain":
			body, _ := io.ReadAll(r.Body)
			got["enable"] = string(body)
			ok(w, map[string]bool{"enabled": true})
		default:
			t.Errorf("неожиданный запрос %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	old := apiBase
	apiBase = srv.URL
	t.Cleanup(func() { apiBase = old })
	return srv, &got
}

func TestDeploy(t *testing.T) {
	_, got := fakeAPI(t, "tok")
	domain, err := (&Client{Token: "tok"}).Deploy(context.Background(), "export default {}", Regions["de"])
	if err != nil {
		t.Fatal(err)
	}
	g := *got
	if !strings.HasPrefix(domain, "fi-tg.fi-") || !strings.HasSuffix(domain, ".workers.dev") {
		t.Fatalf("домен %q", domain)
	}
	if !strings.Contains(g["metadata"], `"placement":{"region":"aws:eu-central-1"}`) || !strings.Contains(g["metadata"], `"main_module":"worker.js"`) {
		t.Fatalf("metadata: %s", g["metadata"])
	}
	if g["code"] != "export default {}" || g["code_type"] != "application/javascript+module" {
		t.Fatalf("код: %q (%s)", g["code"], g["code_type"])
	}
	if !strings.Contains(g["enable"], `"enabled":true`) {
		t.Fatalf("workers.dev не включён: %s", g["enable"])
	}
}

func TestDeployBadToken(t *testing.T) {
	fakeAPI(t, "tok")
	_, err := (&Client{Token: "wrong"}).Deploy(context.Background(), "x", Regions["de"])
	if err == nil || !strings.Contains(err.Error(), "не принял токен") {
		t.Fatalf("ошибка: %v", err)
	}
}
