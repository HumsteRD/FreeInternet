package main

import "testing"

func TestTrayPlacement(t *testing.T) {
	monitor := winRect{0, 0, 1920, 1080}
	cases := []struct {
		name        string
		work        winRect
		w, h        int32
		x, y, wantH int32
	}{
		// Масштаб 150%: окно 600×960 физических пикселей, панель задач внизу высотой 72.
		{"панель внизу", winRect{0, 0, 1920, 1008}, 600, 960, 1308, 36, 960},
		{"окно выше рабочей области — укорачивается", winRect{0, 0, 1920, 1008}, 600, 1400, 1308, 12, 984},
		{"панель сверху", winRect{0, 48, 1920, 1080}, 400, 640, 1508, 60, 640},
		{"панель слева", winRect{60, 0, 1920, 1080}, 400, 640, 72, 428, 640},
		{"панель справа", winRect{0, 0, 1860, 1080}, 400, 640, 1448, 428, 640},
	}
	for _, c := range cases {
		x, y, w, h := trayPlacement(monitor, c.work, c.w, c.h, 12)
		if x != c.x || y != c.y || h != c.wantH || w != c.w {
			t.Errorf("%s: (%d,%d) %d×%d, ждали (%d,%d) %d×%d", c.name, x, y, w, h, c.x, c.y, c.w, c.wantH)
		}
		if y+h > c.work.Bottom || y < c.work.Top || x < c.work.Left || x+w > c.work.Right {
			t.Errorf("%s: окно вышло за рабочую область", c.name)
		}
	}
}
