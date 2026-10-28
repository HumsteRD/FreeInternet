package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOldCheckIntervalMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"check_every":"15m0s"}`), 0o644)
	if c, err := Load(path); err != nil || time.Duration(c.CheckEvery) != DefaultCheckEvery {
		t.Fatalf("прежние 15 минут должны стать %s: %v %v", DefaultCheckEvery, time.Duration(c.CheckEvery), err)
	}
	os.WriteFile(path, []byte(`{"check_every":"7m0s"}`), 0o644)
	if c, _ := Load(path); time.Duration(c.CheckEvery) != 7*time.Minute {
		t.Fatal("заданный вручную интервал не трогаем")
	}
}

func TestYouTubeLeavesViaWorker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(path, []byte(`{"via_worker":["soundcloud.com","youtube.com","music.youtube.com","sndcdn.com"]}`), 0o644)
	c, err := Load(path)
	if err != nil || len(c.ViaWorker) != 2 || c.ViaWorker[0] != "soundcloud.com" || c.ViaWorker[1] != "sndcdn.com" {
		t.Fatalf("YouTube через воркером ломает YouTube Music — его надо убрать из списка: %v %v", c.ViaWorker, err)
	}
	if NotViaWorker("notyoutube.com") || !NotViaWorker("WWW.YouTube.com.") {
		t.Fatal("NotViaWorker: домен и поддомены — да, похожие имена — нет")
	}
}

func TestLoadCorruptKeepsCopy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	broken := []byte(`{"sites":[{"host":"rutracker.org"}`)
	if err := os.WriteFile(path, broken, 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err == nil || !c.Enabled {
		t.Fatalf("повреждённый файл: %+v, %v", c, err)
	}
	if saved, _ := os.ReadFile(path + ".bad"); string(saved) != string(broken) {
		t.Fatalf("копия повреждённых настроек: %q", saved)
	}
}

func TestLoadSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := Load(path)
	if err != nil || c.CheckEvery != Default().CheckEvery || !c.Enabled {
		t.Fatalf("без файла: %+v, %v", c, err)
	}

	c.Strategy = "general (ALT2)"
	now := time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC)
	if !c.AddSite(Site{Host: "x.com", Added: now, Note: "DPI"}) || c.AddSite(Site{Host: "x.com", Added: now}) {
		t.Fatal("AddSite должен отклонять повтор")
	}
	c.AddSite(Site{Host: "twimg.com", Added: now, Parent: "x.com"})
	if main := c.MainSites(); len(main) != 1 || main[0] != "x.com" || len(c.SiteHosts()) != 2 {
		t.Fatalf("основные сайты %v, все %v", main, c.SiteHosts())
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
		t.Error("RemoveSite должен убрать сайт вместе со связанными доменами")
	}
}
