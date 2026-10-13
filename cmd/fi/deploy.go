package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"fi/internal/cfdeploy"
)

// handleWorkerDeploy разворачивает воркер FI в аккаунте Cloudflare по токену пользователя,
// закрепляет его в выбранной стране и вписывает его адрес в FI. Токен никуда не сохраняется.
func (t *tray) handleWorkerDeploy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token  string `json:"token"`
		Region string `json:"region"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	fail := func(err error) { writeJSON(w, http.StatusOK, map[string]string{"error": err.Error()}) }
	region, ok := cfdeploy.Regions[req.Region]
	if !ok {
		region = cfdeploy.Regions["de"]
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	var code struct {
		Code string `json:"code"`
	}
	if err := t.call(ctx, "worker_code", nil, &code); err != nil {
		fail(err)
		return
	}
	domain, err := (&cfdeploy.Client{Token: strings.TrimSpace(req.Token)}).Deploy(ctx, code.Code, region)
	if err != nil {
		fail(err)
		return
	}
	waitWorker(ctx, domain) // новый адрес на workers.dev оживает не сразу
	var status json.RawMessage
	if err := t.call(ctx, "set_telegram_worker", map[string]string{"host": domain}, &status); err != nil {
		fail(err)
		return
	}
	t.call(ctx, "recheck_worker", nil, &status)
	writeJSON(w, http.StatusOK, map[string]any{"domain": domain, "status": status})
}

// waitWorker ждёт, пока воркер ответит по новому адресу, — до 40 секунд.
func waitWorker(ctx context.Context, domain string) {
	client := &http.Client{Timeout: 5 * time.Second}
	for deadline := time.Now().Add(40 * time.Second); time.Now().Before(deadline); {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+domain+"/", nil)
		if resp, err := client.Do(req); err == nil {
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			resp.Body.Close()
			if strings.Contains(string(body), "FI worker") {
				return
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}
