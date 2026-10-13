package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestSitesRoundTrip(t *testing.T) {
	sites := []siteEntry{
		{Host: "soundcloud.com"},
		{Host: "sndcdn.com", Parent: "soundcloud.com"},
		{Host: "x.com"},
		{Host: "soundcloud.cloud", Parent: "soundcloud.com"},
		{Host: "orphan.net", Parent: "gone.com"},
	}
	text := formatSites(sites)
	want := []siteEntry{
		{Host: "soundcloud.com"},
		{Host: "sndcdn.com", Parent: "soundcloud.com"},
		{Host: "soundcloud.cloud", Parent: "soundcloud.com"},
		{Host: "x.com"},
		{Host: "orphan.net"},
	}
	if got := parseSites(strings.NewReader(text)); !reflect.DeepEqual(got, want) {
		t.Fatalf("после сохранения и загрузки:\n%v\nwant\n%v\nтекст:\n%s", got, want, text)
	}
	// Список, набранный вручную в Блокноте: с BOM, комментариями и адресами целиком.
	got := parseSites(strings.NewReader(bom + "https://rutracker.org/forum # торренты\r\n\r\n  \r\ninstagram.com\r\n"))
	if len(got) != 2 || got[0].Host != "https://rutracker.org/forum" || got[1].Host != "instagram.com" {
		t.Fatalf("ручной список: %v", got)
	}
}

func TestAlertsOnlyConfirmedFailures(t *testing.T) {
	var a serviceAlerts
	check := func(state string) []alert {
		return a.update([]serviceInfo{{ID: "calls", Title: "Звонки и голос", State: state}})
	}
	steps := []struct {
		state string
		want  []alert
	}{
		{"ok", nil},      // первая проверка: только запоминаем
		{"partial", nil}, // одиночный сбой — ждём подтверждения
		{"ok", nil},      // сбой был случайным: ни «не работает», ни «снова работает»
		{"fail", nil},
		{"fail", []alert{{title: "Звонки и голос", broken: true}}}, // подтвердилось
		{"fail", nil}, // уже сообщили
		{"ok", []alert{{title: "Звонки и голос"}}},
	}
	for i, s := range steps {
		if got := check(s.state); !reflect.DeepEqual(got, s.want) {
			t.Fatalf("шаг %d (%s): %v, ждали %v", i, s.state, got, s.want)
		}
	}
}

func TestRedactHidesProxySecret(t *testing.T) {
	out := string(redact([]byte(`{"telegram":{"secret":"abc123","link":"tg://proxy?secret=abc123","worker_key":"k3yk3y","port":1453}}`)))
	if strings.Contains(out, "abc123") || strings.Contains(out, "k3yk3y") {
		t.Fatalf("секрет попал в отчёт: %s", out)
	}
	var v map[string]map[string]any
	if json.Unmarshal([]byte(out), &v) != nil || v["telegram"]["port"] != float64(1453) {
		t.Fatalf("остальное испорчено: %s", out)
	}
}
