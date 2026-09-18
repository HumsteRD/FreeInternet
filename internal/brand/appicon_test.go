package brand

import (
	"image/color"
	"testing"
)

func near(t *testing.T, label string, got, want color.NRGBA) {
	t.Helper()
	d := func(a, b uint8) int {
		if a > b {
			return int(a - b)
		}
		return int(b - a)
	}
	if d(got.R, want.R) > 24 || d(got.G, want.G) > 24 || d(got.B, want.B) > 24 || d(got.A, want.A) > 24 {
		t.Errorf("%s: %v, want ≈%v", label, got, want)
	}
}

func TestAppIcon(t *testing.T) {
	images := AppIconImages()
	if len(images) != len(IconSizes) {
		t.Fatalf("кадров %d", len(images))
	}
	for i, img := range images {
		if img.Bounds().Dx() != IconSizes[i] || img.Bounds().Dy() != IconSizes[i] {
			t.Errorf("кадр %d: размер %v", IconSizes[i], img.Bounds())
		}
	}

	big := AppIcon(256)
	near(t, "угол прозрачный", big.NRGBAAt(2, 2), color.NRGBA{})
	near(t, "буква F светлая", big.NRGBAAt(70, 150), color.NRGBA{0xF1, 0xEE, 0xF3, 0xFF})
	// Середина стойки: там градиент проходит через чистый цвет программы.
	near(t, "стойка I красная", big.NRGBAAt(186, 114), color.NRGBA{0xE5, 0x48, 0x4D, 0xFF})
	if violetI := AppIconFor(AccentViolet, 256).NRGBAAt(186, 114); violetI.B < violetI.R {
		t.Errorf("у службы стойка не фиолетовая: %v", violetI)
	}
	if a := big.NRGBAAt(128, 30).A; a != 0xFF {
		t.Errorf("плитка над корпусом не сплошная: alpha %d", a)
	}

	// Мелкие кадры: по середине корпуса идут только чистые цвета — плитка, корпус, просвет.
	// Полупрозрачная смесь означает, что грань попала между пикселями и размыта.
	pure := []color.NRGBA{{0x0E, 0x0C, 0x11, 0xFF}, {0xF1, 0xEE, 0xF3, 0xFF}, {0xE5, 0x48, 0x4D, 0xFF}}
	for _, size := range []int{20, 24, 32, 40, 48} {
		img := AppIcon(size)
		y := size * 2 / 3
		reds := 0
		for x := 2; x < size-2; x++ {
			c := img.NRGBAAt(x, y)
			ok := false
			for _, p := range pure {
				if c == p {
					ok = true
				}
			}
			if !ok {
				t.Errorf("%dpx: пиксель (%d,%d) размыт: %v", size, x, y, c)
			}
			if c == pure[2] {
				reds++
			}
		}
		if reds < 2 {
			t.Errorf("%dpx: ширина стойки I всего %d px", size, reds)
		}
	}

	small := AppIcon(16)
	near(t, "16px: буква F", small.NRGBAAt(4, 8), color.NRGBA{0xF1, 0xEE, 0xF3, 0xFF})
	near(t, "16px: стойка I", small.NRGBAAt(12, 10), color.NRGBA{0xE5, 0x48, 0x4D, 0xFF})
	near(t, "16px: плитка", small.NRGBAAt(1, 8), color.NRGBA{0x0E, 0x0C, 0x11, 0xFF})
}
