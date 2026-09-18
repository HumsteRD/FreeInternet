package config

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := Load(path)
	if err != nil || c.CheckEvery != Default().CheckEvery || !c.Enabled {
		t.Fatalf("без файла: %+v, %v", c, err)
	}

	c.Strategy = "general (ALT2)"
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	if !c.AddSite("x.com", "DPI", now) || c.AddSite("x.com", "", now) {
		t.Fatal("AddSite должен отклонять повтор")
	}
	if err := Save(path, c); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := json.Marshal(c)
	have, _ := json.Marshal(got)
	if string(want) != string(have) {
		t.Fatalf("после сохранения:\n%s\nwant\n%s", have, want)
	}

	if !got.RemoveSite("x.com") || got.RemoveSite("x.com") || len(got.SiteHosts()) != 0 {
		t.Error("RemoveSite")
	}
}
