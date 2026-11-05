package main

import "testing"

func strp(s string) *string { return &s }
func intp(n int) *int       { return &n }
func boolp(b bool) *bool    { return &b }

func TestLookChange(t *testing.T) {
	var s traySettings
	if err := (lookChange{Theme: strp("anime"), Accent: strp("#FF6FAE"), Font: strp("mono"), Radius: strp("round"), Scale: intp(110), Motion: boolp(false)}).apply(&s); err != nil {
		t.Fatal(err)
	}
	got := lookOf(s)
	if got != (look{Theme: "anime", Accent: "#FF6FAE", Font: "mono", Radius: "round", Scale: 110, Motion: false}) {
		t.Fatalf("оформление: %+v", got)
	}
	bad := []lookChange{
		{Theme: strp("matrix")},
		{Accent: strp("red; background:url(x)")},
		{Font: strp("Comic Sans")},
		{Radius: strp("huge")},
		{Scale: intp(300)},
	}
	for _, c := range bad {
		before := s
		if c.apply(&s) == nil || s != before {
			t.Errorf("принято неверное значение: %+v", c)
		}
	}
	// Сброс к оформлению темы.
	if err := (lookChange{Accent: strp(""), Font: strp(""), Radius: strp(""), Scale: intp(0), Motion: boolp(true)}).apply(&s); err != nil {
		t.Fatal(err)
	}
	if got := lookOf(s); got != (look{Theme: "anime", Motion: true}) {
		t.Fatalf("после сброса: %+v", got)
	}
	if lookOf(traySettings{Theme: "unknown"}).Theme != defaultTheme {
		t.Fatal("неизвестная тема — тёмная")
	}
}
