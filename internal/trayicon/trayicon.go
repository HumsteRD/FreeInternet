// Package trayicon рисует значок трея по пиксельной геометрии из дизайна
// (design/build.mjs, TRAY): отдельный кадр под каждый размер, грани на целых пикселях.
package trayicon

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

type State int

const (
	Active    State = iota // обход работает
	Selecting              // идёт проверка или подбор стратегии
	Attention              // часть сервисов с перебоями
	Failure                // сервис не работает
	Paused                 // обход выключен
	Off                    // служба недоступна
)

// frame — знак «FI» для одного размера: буква F и стойка I, сквозь которую идёт свет.
type frame struct {
	x1, y1, y2     int     // левый край F, верх и низ знака
	stem           int     // толщина стоек и перекладин
	armTop, armMid int     // правые края верхней и средней перекладин
	midY           int     // верх средней перекладины
	ix1, ix2       int     // стойка I — она и есть просвет
	bx, by, br     float64 // точка состояния
}

var frames = map[int]frame{
	16: {3, 2, 12, 2, 9, 8, 6, 11, 13, 12.5, 12.5, 2.5},
	20: {4, 3, 16, 2, 11, 10, 8, 14, 16, 15.5, 15.5, 3},
	24: {5, 3, 19, 3, 14, 12, 9, 17, 20, 18.5, 18.5, 3.5},
	32: {7, 4, 26, 4, 19, 17, 13, 23, 27, 24.5, 24.5, 4.5},
}

// Sizes — размеры, для которых нарисованы кадры.
var Sizes = []int{16, 20, 24, 32}

// FrameSize подбирает ближайший кадр не меньше размера, который просит система.
func FrameSize(px int) int {
	for _, s := range Sizes {
		if px <= s {
			return s
		}
	}
	return Sizes[len(Sizes)-1]
}

var (
	red    = color.NRGBA{0xE5, 0x48, 0x4D, 0xFF}
	violet = color.NRGBA{0x9A, 0x6C, 0xF0, 0xFF}
	amber  = color.NRGBA{0xEB, 0xA9, 0x45, 0xFF}
)

// PNG рисует значок размера size. forDarkTaskbar — светлый знак для тёмной панели задач.
func PNG(size int, state State, forDarkTaskbar bool) []byte {
	f, ok := frames[size]
	if !ok {
		size = FrameSize(size)
		f = frames[size]
	}
	ink, dim := color.NRGBA{0x16, 0x13, 0x1A, 0xFF}, color.NRGBA{0xBD, 0xB6, 0xC4, 0xFF}
	if forDarkTaskbar {
		ink, dim = color.NRGBA{0xF1, 0xEE, 0xF3, 0xFF}, color.NRGBA{0x5A, 0x54, 0x60, 0xFF}
	}

	inGlyph := func(x, y int) bool { return inF(f, x, y) || inSlit(f, x, y) }

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			switch {
			case state == Off:
				// Только контур знака: пиксель, у которого есть сосед снаружи.
				if inGlyph(x, y) && (!inGlyph(x-1, y) || !inGlyph(x+1, y) || !inGlyph(x, y-1) || !inGlyph(x, y+1)) {
					img.SetNRGBA(x, y, withAlpha(ink, 0.7))
				}
			case inSlit(f, x, y):
				img.SetNRGBA(x, y, slitColor(state, dim))
			case inF(f, x, y) && state == Paused:
				img.SetNRGBA(x, y, withAlpha(ink, 0.5))
			case inF(f, x, y):
				img.SetNRGBA(x, y, ink)
			}
		}
	}
	switch state {
	case Attention:
		badge(img, f, amber)
	case Failure:
		badge(img, f, red)
	}

	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// inF — точка внутри буквы F: стойка, верхняя и средняя перекладины.
func inF(f frame, x, y int) bool {
	if y < f.y1 || y >= f.y2 || x < f.x1 {
		return false
	}
	switch {
	case x < f.x1+f.stem:
		return true
	case y < f.y1+f.stem:
		return x < f.armTop
	case y >= f.midY && y < f.midY+f.stem:
		return x < f.armMid
	}
	return false
}

// inSlit — стойка I: через неё показывается состояние обхода.
func inSlit(f frame, x, y int) bool {
	return x >= f.ix1 && x < f.ix2 && y >= f.y1 && y < f.y2
}

func slitColor(s State, dim color.NRGBA) color.NRGBA {
	switch s {
	case Selecting:
		return violet
	case Failure, Paused:
		return dim // свет не проходит
	default:
		return red
	}
}

// badge вырезает кольцо вокруг точки состояния, чтобы она читалась на любой панели, и рисует точку.
func badge(img *image.NRGBA, f frame, c color.NRGBA) {
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if ring := coverage(x, y, f.bx, f.by, f.br+1.25); ring > 0 {
				p := img.NRGBAAt(x, y)
				p.A = uint8(math.Round(float64(p.A) * (1 - ring)))
				img.SetNRGBA(x, y, p)
			}
			if dot := coverage(x, y, f.bx, f.by, f.br); dot > 0 {
				img.SetNRGBA(x, y, over(img.NRGBAAt(x, y), withAlpha(c, dot)))
			}
		}
	}
}

// coverage — доля пикселя (x, y) внутри круга, по 4×4 подвыборкам.
func coverage(x, y int, cx, cy, r float64) float64 {
	const n = 4
	inside := 0
	for i := range n {
		for j := range n {
			px := float64(x) + (float64(i)+0.5)/n
			py := float64(y) + (float64(j)+0.5)/n
			if math.Hypot(px-cx, py-cy) <= r {
				inside++
			}
		}
	}
	return float64(inside) / (n * n)
}

func withAlpha(c color.NRGBA, a float64) color.NRGBA {
	c.A = uint8(math.Round(float64(c.A) * a))
	return c
}

// over накладывает src на dst; цвета без предумножения альфы.
func over(dst, src color.NRGBA) color.NRGBA {
	sa, da := float64(src.A)/255, float64(dst.A)/255
	oa := sa + da*(1-sa)
	if oa == 0 {
		return color.NRGBA{}
	}
	mix := func(s, d uint8) uint8 {
		return uint8(math.Round((float64(s)*sa + float64(d)*da*(1-sa)) / oa))
	}
	return color.NRGBA{mix(src.R, dst.R), mix(src.G, dst.G), mix(src.B, dst.B), uint8(math.Round(oa * 255))}
}
