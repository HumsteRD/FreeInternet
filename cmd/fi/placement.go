package main

// winRect — прямоугольник в физических пикселях, как RECT в Windows.
type winRect struct{ Left, Top, Right, Bottom int32 }

// trayPlacement — где поставить окно размером w×h у значка в трее. Панель задач — с той стороны,
// где рабочая область меньше монитора; окно встаёт в угол у неё, справа (там трей), с отступом
// margin и целиком внутри рабочей области.
func trayPlacement(monitor, work winRect, w, h, margin int32) (x, y, width, height int32) {
	maxH := work.Bottom - work.Top - 2*margin
	maxW := work.Right - work.Left - 2*margin
	width, height = min(w, maxW), min(h, maxH)
	x = work.Right - width - margin
	y = work.Bottom - height - margin
	switch {
	case work.Top > monitor.Top: // панель сверху
		y = work.Top + margin
	case work.Left > monitor.Left: // панель слева
		x = work.Left + margin
	}
	return x, y, width, height
}
