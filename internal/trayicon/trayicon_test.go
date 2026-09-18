package trayicon

import (
	"bytes"
	"image"
	"image/png"
	"testing"
)

func decode(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func rgba(img image.Image, x, y int) (r, g, b, a uint32) {
	r, g, b, a = img.At(x, y).RGBA()
	return r >> 8, g >> 8, b >> 8, a >> 8
}

func TestFrames(t *testing.T) {
	for _, size := range Sizes {
		f := frames[size]
		img := decode(t, PNG(size, Active, true))
		if img.Bounds().Dx() != size {
			t.Fatalf("размер %d, want %d", img.Bounds().Dx(), size)
		}
		if r, _, _, a := rgba(img, f.ix1, f.y2-1); r != 0xE5 || a != 0xFF {
			t.Errorf("%d px: просвет не красный: r=%x a=%x", size, r, a)
		}
		if r, _, _, a := rgba(img, f.x1, f.y1); r != 0xF1 || a != 0xFF {
			t.Errorf("%d px: корпус не светлый: r=%x a=%x", size, r, a)
		}
		if _, _, _, a := rgba(img, 0, size-1); a != 0 {
			t.Errorf("%d px: угол не прозрачный", size)
		}
	}
}

func TestStates(t *testing.T) {
	f := frames[16]
	off := decode(t, PNG(16, Off, false))
	if _, _, _, a := rgba(off, f.x1+1, f.y1+1); a != 0 {
		t.Error("выключено: внутри контура не прозрачно")
	}
	if _, _, _, a := rgba(off, f.x1, f.y1+1); a == 0 {
		t.Error("выключено: нет контура")
	}

	failure := decode(t, PNG(16, Failure, true))
	if r, g, _, a := rgba(failure, int(f.bx), int(f.by)); r != 0xE5 || g != 0x48 || a < 0xF0 {
		t.Errorf("не работает: точка не красная: %x %x a=%x", r, g, a)
	}

	if FrameSize(18) != 20 || FrameSize(40) != 32 {
		t.Error("FrameSize")
	}
}
