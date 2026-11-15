// Package cfdeploy разворачивает Cloudflare Worker FI в аккаунте пользователя через API Cloudflare.
//
// Зачем не руками: в панели Cloudflare нельзя выбрать, где запускать воркер, а запускается он
// в том дата-центре, куда провайдер привёл запрос, — у многих российских провайдеров это
// Гонконг, и сайты видят гонконгский IP. Через API воркер закрепляется в выбранном регионе
// (placement.region), и сайты видят, например, Германию. Токен используется один раз и не хранится.
package cfdeploy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"
)

// ScriptName — имя воркера FI в аккаунте.
const ScriptName = "fi-tg"

// Regions — где можно закрепить воркер: облачный регион → дата-центр Cloudflare рядом с ним.
var Regions = map[string]string{
	"de": "aws:eu-central-1", // Франкфурт
	"nl": "gcp:europe-west4", // Нидерланды
	"us": "aws:us-east-1",    // Вирджиния
}

// apiBase подменяется в тестах.
var apiBase = "https://api.cloudflare.com/client/v4"

// Страницы Cloudflare, на которые мастер настройки ведёт человека.
const (
	// SignUpURL — регистрация: почта и пароль, карта не нужна.
	SignUpURL = "https://dash.cloudflare.com/sign-up"
	// TokenURL — создание токена с уже заполненной формой: права «Workers Scripts: Edit» и
	// «Account Settings: Read», имя FI. Человеку остаётся нажать «Continue to summary» и
	// «Create Token». Если форма откроется пустой, годится шаблон «Edit Cloudflare Workers».
	TokenURL = "https://dash.cloudflare.com/profile/api-tokens?permissionGroupKeys=" +
		"%5B%7B%22key%22%3A%22workers_scripts%22%2C%22type%22%3A%22edit%22%7D%2C%7B%22key%22%3A%22account_settings%22%2C%22type%22%3A%22read%22%7D%5D" +
		"&name=FI&accountId=*&zoneId=all"
)

// Client — запросы к API Cloudflare от имени токена пользователя.
type Client struct {
	Token string
	HTTP  *http.Client
}

type apiResponse struct {
	Success bool            `json:"success"`
	Errors  []apiError      `json:"errors"`
	Result  json.RawMessage `json:"result"`
}

type apiError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// APIError — Cloudflare отказал; Code — его код ошибки.
type APIError struct {
	Status int
	Code   int
	Msg    string
}

// BadToken — Cloudflare отказал из-за самого токена: его нет смысла пробовать снова.
func (e *APIError) BadToken() bool {
	msg := strings.ToLower(e.Msg)
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden ||
		e.Code == 10000 || e.Code == 9109 || e.Code == 6003 || e.Code == 6111 ||
		strings.Contains(msg, "authorization header") || strings.Contains(msg, "invalid request headers")
}

// Error объясняет отказ словами, по которым человек поймёт, что делать: сообщения Cloudflare
// написаны для программистов и по-английски.
func (e *APIError) Error() string {
	msg := strings.ToLower(e.Msg)
	switch {
	case e.Code == 10034 || strings.Contains(msg, "email") && strings.Contains(msg, "verif"):
		return "Cloudflare просит подтвердить почту: откройте письмо от Cloudflare, нажмите в нём ссылку и повторите"
	case e.Code == 6003 || e.Code == 6111 || strings.Contains(msg, "authorization header") || strings.Contains(msg, "invalid request headers"):
		return "это не похоже на токен Cloudflare: скопируйте его кнопкой Copy сразу после создания — он показывается один раз"
	case e.BadToken():
		return "Cloudflare не принял токен: создайте новый кнопкой «Создать токен» — нужны права «Workers Scripts: Edit» (" + e.Msg + ")"
	}
	return fmt.Sprintf("Cloudflare: %s (код %d)", e.Msg, e.Code)
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Cloudflare не отвечает: %w", err)
	}
	defer resp.Body.Close()
	var r apiResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		return fmt.Errorf("Cloudflare ответил непонятно (HTTP %d): %w", resp.StatusCode, err)
	}
	if !r.Success {
		e := &APIError{Status: resp.StatusCode, Msg: "запрос отклонён"}
		if len(r.Errors) > 0 {
			e.Code, e.Msg = r.Errors[0].Code, r.Errors[0].Message
		}
		return e
	}
	if out != nil && len(r.Result) > 0 {
		return json.Unmarshal(r.Result, out)
	}
	return nil
}

func (c *Client) jsonBody(ctx context.Context, method, path string, in, out any) error {
	data, err := json.Marshal(in)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path, "application/json", bytes.NewReader(data), out)
}

// account — первый аккаунт, к которому у токена есть доступ.
func (c *Client) account(ctx context.Context) (string, error) {
	var accounts []struct {
		ID string `json:"id"`
	}
	if err := c.do(ctx, http.MethodGet, "/accounts?per_page=50", "", nil, &accounts); err != nil {
		return "", err
	}
	if len(accounts) == 0 {
		return "", errors.New("у токена нет доступа ни к одному аккаунту Cloudflare: создайте токен заново и в «Account Resources» оставьте «All accounts»")
	}
	return accounts[0].ID, nil
}

// subdomain — поддомен аккаунта на workers.dev; если его ещё нет, создаёт.
func (c *Client) subdomain(ctx context.Context, account string) (string, error) {
	var sub struct {
		Subdomain string `json:"subdomain"`
	}
	err := c.do(ctx, http.MethodGet, "/accounts/"+account+"/workers/subdomain", "", nil, &sub)
	if err == nil && sub.Subdomain != "" {
		return sub.Subdomain, nil
	}
	var ae *APIError
	if err != nil && !(errors.As(err, &ae) && ae.Status == http.StatusNotFound) {
		return "", err
	}
	b := make([]byte, 4)
	rand.Read(b)
	name := "fi-" + hex.EncodeToString(b)
	if err := c.jsonBody(ctx, http.MethodPut, "/accounts/"+account+"/workers/subdomain", map[string]string{"subdomain": name}, &sub); err != nil {
		return "", fmt.Errorf("поддомен workers.dev не создан: %w", err)
	}
	if sub.Subdomain == "" {
		sub.Subdomain = name
	}
	return sub.Subdomain, nil
}

// upload загружает код воркера с закреплением в регионе.
func (c *Client) upload(ctx context.Context, account, code, region string) error {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	meta, _ := json.Marshal(map[string]any{
		"main_module":        "worker.js",
		"compatibility_date": "2026-01-01",
		"placement":          map[string]string{"region": region},
	})
	if err := w.WriteField("metadata", string(meta)); err != nil {
		return err
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="worker.js"; filename="worker.js"`)
	h.Set("Content-Type", "application/javascript+module")
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	io.WriteString(part, code)
	w.Close()
	return c.do(ctx, http.MethodPut, "/accounts/"+account+"/workers/scripts/"+ScriptName, w.FormDataContentType(), &body, nil)
}

// LooksLikeToken — строка похожа на токен Cloudflare: 40 и больше букв, цифр, «_» и «-».
// Проверка грубая — чтобы сразу сказать человеку, что он вставил не то (адрес сайта, пароль).
func LooksLikeToken(s string) bool {
	if len(s) < 40 || len(s) > 200 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-') {
			return false
		}
	}
	return true
}

// Account проверяет токен и возвращает аккаунт, в который пойдёт воркер: первый, к которому
// у токена есть доступ.
func (c *Client) Account(ctx context.Context) (string, error) {
	if strings.TrimSpace(c.Token) == "" {
		return "", errors.New("вставьте токен Cloudflare")
	}
	return c.account(ctx)
}

// Deploy загружает воркер FI в аккаунт, закрепляет в регионе и включает адрес на workers.dev.
// Возвращает домен воркера: fi-tg.<поддомен>.workers.dev.
func (c *Client) Deploy(ctx context.Context, code, region string) (string, error) {
	account, err := c.Account(ctx)
	if err != nil {
		return "", err
	}
	return c.DeployTo(ctx, account, code, region)
}

// DeployTo — как Deploy, но в уже известный аккаунт.
func (c *Client) DeployTo(ctx context.Context, account, code, region string) (string, error) {
	sub, err := c.subdomain(ctx, account)
	if err != nil {
		return "", err
	}
	if err := c.upload(ctx, account, code, region); err != nil {
		return "", fmt.Errorf("воркер не загружен: %w", err)
	}
	enable := map[string]bool{"enabled": true, "previews_enabled": false}
	if err := c.jsonBody(ctx, http.MethodPost, "/accounts/"+account+"/workers/scripts/"+ScriptName+"/subdomain", enable, nil); err != nil {
		return "", fmt.Errorf("адрес воркера на workers.dev не включён: %w", err)
	}
	return ScriptName + "." + sub + ".workers.dev", nil
}
