// Package brand рисует значок приложения по геометрии из дизайна (docs/design/build.mjs: appIcon,
// markSimple, mark16): «П» с узким красным просветом на тёмной плитке.
package brand

import (
	"image"
	"image/color"
	"math"
)

// IconSizes — кадры значка в .ico: от панели задач до «Установленных приложений».
var IconSizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

// Accent — цвет стойки «I». По нему программы FI различаются в папке.
type Accent int

const (
	AccentRed    Accent = iota // окно
	AccentViolet               // служба
	AccentGrey                 // удаление
	AccentAmber                // стенд проверки стратегий
)

func (a Accent) color() rgba {
	switch a {
	case AccentViolet:
		return violet
	case AccentGrey:
		return hex(0x8C8494)
	case AccentAmber:
		return hex(0xEBA945)
	default:
		return red
	}
}

// AppIconImages рисует все кадры значка окна.
func AppIconImages() []image.Image { return AppIconImagesFor(AccentRed) }

// AppIconImagesFor рисует все кадры значка с заданным цветом стойки.
func AppIconImagesFor(a Accent) []image.Image {
	images := make([]image.Image, len(IconSizes))
	for i, size := range IconSizes {
		images[i] = AppIconFor(a, size)
	}
	return images
}

// AppIcon рисует значок окна размера size.
func AppIcon(size int) *image.NRGBA { return AppIconFor(AccentRed, size) }

// AppIconFor рисует значок размера size: крупные — с подсветкой, до 48 px — плоский,
// 16 px — выровненный по пикселям.
func AppIconFor(a Accent, size int) *image.NRGBA {
	accent := a.color()
	switch {
	case size <= 16:
		return render(size, 16, mark16(accent))
	case size <= 48:
		return render(size, float64(size), markSimple(size, accent))
	default:
		return render(size, 256, markLarge(accent))
	}
}

// rgba — цвет с предумноженной альфой в долях единицы.
type rgba struct{ r, g, b, a float64 }

func hex(c uint32) rgba {
	return rgba{float64(c>>16&0xFF) / 255, float64(c>>8&0xFF) / 255, float64(c&0xFF) / 255, 1}
}

func (c rgba) alpha(a float64) rgba { return rgba{c.r * a, c.g * a, c.b * a, c.a * a} }

// over кладёт src поверх dst (оба с предумноженной альфой).
func over(dst, src rgba) rgba {
	k := 1 - src.a
	return rgba{src.r + dst.r*k, src.g + dst.g*k, src.b + dst.b*k, src.a + dst.a*k}
}

func mix(a, b rgba, t float64) rgba {
	t = math.Max(0, math.Min(1, t))
	return rgba{a.r + (b.r-a.r)*t, a.g + (b.g-a.g)*t, a.b + (b.b-a.b)*t, a.a + (b.a-a.a)*t}
}

var (
	red      = hex(0xE5484D)
	violet   = hex(0x8C5CF2)
	ink      = hex(0xF1EEF3)
	tileFlat = hex(0x0E0C11)
	white    = hex(0xFFFFFF)
)

// render усредняет 4×4 отсчёта на пиксель; units — размер холста фигуры в её единицах.
func render(size int, units float64, paint func(u, v float64) rgba) *image.NRGBA {
	const n = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := units / float64(size)
	for y := range size {
		for x := range size {
			var sum rgba
			for i := range n {
				for j := range n {
					c := paint((float64(x)+(float64(i)+0.5)/n)*scale, (float64(y)+(float64(j)+0.5)/n)*scale)
					sum = rgba{sum.r + c.r, sum.g + c.g, sum.b + c.b, sum.a + c.a}
				}
			}
			k := 1.0 / (n * n)
			img.SetNRGBA(x, y, toNRGBA(rgba{sum.r * k, sum.g * k, sum.b * k, sum.a * k}))
		}
	}
	return img
}

func toNRGBA(c rgba) color.NRGBA {
	if c.a <= 0 {
		return color.NRGBA{}
	}
	ch := func(v float64) uint8 { return uint8(math.Round(math.Min(1, v/c.a) * 255)) }
	return color.NRGBA{ch(c.r), ch(c.g), ch(c.b), uint8(math.Round(math.Min(1, c.a) * 255))}
}

// inRoundRect — точка внутри прямоугольника [x0,x1)×[y0,y1) со скруглением r.
func inRoundRect(u, v, x0, y0, x1, y1, r float64) bool {
	if u < x0 || u >= x1 || v < y0 || v >= y1 {
		return false
	}
	cx := math.Max(x0+r, math.Min(u, x1-r))
	cy := math.Max(y0+r, math.Min(v, y1-r))
	return math.Hypot(u-cx, v-cy) <= r
}

// letter — геометрия знака «FI»: буква F и стойка I, сквозь которую идёт свет.
type letter struct {
	x0, y0, y1     float64 // левый край буквы F, верх и низ знака
	stem           float64 // толщина стоек и перекладин
	armTop, armMid float64 // правые края верхней и средней перекладин
	midY           float64 // верх средней перекладины
	ix0, ix1       float64 // стойка I
}

// inF — точка внутри буквы F.
func (l letter) inF(u, v float64) bool {
	if v < l.y0 || v >= l.y1 || u < l.x0 {
		return false
	}
	switch {
	case u < l.x0+l.stem:
		return true // стойка
	case v < l.y0+l.stem:
		return u < l.armTop // верхняя перекладина
	case v >= l.midY && v < l.midY+l.stem:
		return u < l.armMid // средняя перекладина
	}
	return false
}

// inI — точка внутри стойки I: это и есть просвет.
func (l letter) inI(u, v float64) bool {
	return u >= l.ix0 && u < l.ix1 && v >= l.y0 && v < l.y1
}

// large — знак «FI» на холсте 256.
var large = letter{x0: 56, y0: 60, y1: 196, stem: 28, armTop: 150, armMid: 132, midY: 112, ix0: 172, ix1: 200}

// markLarge — крупный значок, холст 256: градиент плитки, подсветка снизу, светлая «F»
// и стойка «I» с градиентом — сквозь неё идёт свет.
func markLarge(accent rgba) func(u, v float64) rgba {
	light, dark := mix(accent, white, 0.45), mix(accent, hex(0x2A1030), 0.5)
	return func(u, v float64) rgba {
		if !inRoundRect(u, v, 0, 0, 256, 256, 56) {
			return rgba{}
		}
		c := mix(hex(0x1B171F), hex(0x08070A), v/256)
		if d := math.Hypot(u-128, v-212) / 112; v >= 96 && d < 1 {
			var glow rgba
			if d <= 0.42 {
				t := d / 0.42
				glow = mix(accent, violet, t).alpha(0.55 + (0.22-0.55)*t)
			} else {
				glow = violet.alpha(0.22 * (1 - (d-0.42)/0.58))
			}
			c = over(c, glow)
		}
		if !inRoundRect(u, v, 3, 3, 253, 253, 53) {
			c = over(c, white.alpha(0.08))
		}
		if large.inF(u, v) {
			c = ink
		}
		if large.inI(u, v) {
			t := (v - large.y0) / (large.y1 - large.y0)
			if t < 0.4 {
				c = mix(light, accent, t/0.4)
			} else {
				c = mix(accent, dark, (t-0.4)/0.6)
			}
		}
		return c
	}
}

// markSimple — плоский значок до 48 px. Геометрия холста 32 пересчитана в пиксели и округлена:
// грани корпуса, просвета и светлая кромка плитки лежат на целых пикселях, иначе на 20, 24
// и 48 px они размываются. На 32 px совпадает с дизайном один в один.
func markSimple(size int, accent rgba) func(u, v float64) rgba {
	px := float64(size)
	s := px / 32
	l := snapped(s)

	return func(u, v float64) rgba {
		if !inRoundRect(u, v, 0, 0, px, px, 7*s) {
			return rgba{}
		}
		c := tileFlat
		if !inRoundRect(u, v, 1, 1, px-1, px-1, 7*s-1) {
			c = over(c, white.alpha(0.1))
		}
		if l.inF(u, v) {
			c = ink
		}
		if l.inI(u, v) {
			c = accent
		}
		return c
	}
}

// snapped — «FI» с гранями на целых пикселях: иначе на 20, 24 и 48 px буквы размываются.
func snapped(s float64) letter {
	stem := math.Max(2, math.Round(4*s))
	x0 := math.Round(7 * s)
	y0 := math.Round(7 * s)
	ix1 := math.Round(26 * s)
	return letter{
		x0:     x0,
		y0:     y0,
		y1:     math.Round(25 * s),
		stem:   stem,
		armTop: math.Round(19 * s),
		armMid: math.Round(17 * s),
		midY:   y0 + math.Round(7*s),
		ix0:    ix1 - stem,
		ix1:    ix1,
	}
}

// small — «FI» на 16 px: своя геометрия, иначе буквы сливаются.
var small = letter{x0: 3, y0: 3, y1: 13, stem: 2, armTop: 10, armMid: 9, midY: 7, ix0: 11, ix1: 13}

// mark16 — 16 px, грани на целых пикселях.
func mark16(accent rgba) func(u, v float64) rgba {
	return func(u, v float64) rgba {
		if !inRoundRect(u, v, 0, 0, 16, 16, 3.5) {
			return rgba{}
		}
		c := tileFlat
		if small.inF(u, v) {
			c = ink
		}
		if small.inI(u, v) {
			c = accent
		}
		return c
	}
}
