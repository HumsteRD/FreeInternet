package cfdeploy

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestAPIErrorText: отказы Cloudflare объясняются словами, по которым понятно, что делать.
func TestAPIErrorText(t *testing.T) {
	cases := []struct {
		err      APIError
		want     string
		badToken bool
	}{
		{APIError{Status: 400, Code: 6003, Msg: "Invalid request headers"}, "не похоже на токен", true},
		{APIError{Status: 403, Code: 10000, Msg: "Authentication error"}, "не принял токен", true},
		{APIError{Status: 403, Code: 10034, Msg: "You need to verify your email address to use Workers."}, "подтвердить почту", true},
		{APIError{Status: 400, Code: 10021, Msg: "Uncaught SyntaxError"}, "Uncaught SyntaxError (код 10021)", false},
	}
	for _, c := range cases {
		if got := c.err.Error(); !strings.Contains(got, c.want) || c.err.BadToken() != c.badToken {
			t.Errorf("%+v: %q (токен плохой: %v), ждали %q и %v", c.err, got, c.err.BadToken(), c.want, c.badToken)
		}
	}
}

func TestLooksLikeToken(t *testing.T) {
	for s, want := range map[string]bool{
		"aB3dE6gH9jK2mN5pQ8sT1vW4yZ7cF0hJ3lM6oR9u":       true, // так выглядит токен Cloudflare
		"aB3dE6gH9jK2mN5pQ8sT1vW4yZ7cF0hJ3lM6oR_-x":      true,
		"https://dash.cloudflare.com/profile/api-tokens": false,
		"мой пароль 123":                                 false,
		"короткий":                                       false,
		"":                                               false,
	} {
		if got := LooksLikeToken(s); got != want {
			t.Errorf("LooksLikeToken(%q) = %v", s, got)
		}
	}
}

// TestTokenURL: ссылка на создание токена несёт права, без которых воркер не загрузить.
func TestTokenURL(t *testing.T) {
	u, err := url.Parse(TokenURL)
	if err != nil {
		t.Fatal(err)
	}
	var groups []struct{ Key, Type string }
	if err := json.Unmarshal([]byte(u.Query().Get("permissionGroupKeys")), &groups); err != nil {
		t.Fatalf("права в ссылке не разобрать: %v", err)
	}
	have := map[string]string{}
	for _, g := range groups {
		have[g.Key] = g.Type
	}
	if have["workers_scripts"] != "edit" || have["account_settings"] != "read" || u.Query().Get("name") != "FI" {
		t.Fatalf("ссылка на токен: %v, имя %q", have, u.Query().Get("name"))
	}
}
