package tgproxy

import "encoding/binary"

// badDCCode — ошибка транспорта MTProto «неверный дата-центр». Получив её, Telegram Desktop
// и его форки показывают «прокси настроен неправильно» и переключаются на системный прокси,
// поэтому прокси её клиенту не передаёт, а закрывает соединение: клиент просто переподключится.
const badDCCode = -444

// watcher следит за границами пакетов в потоке от Telegram, не задерживая данные,
// и находит пакет с кодом -444. Коротким (меньше 12 байт) пакетом Telegram Desktop
// считает код ошибки в первых четырёх байтах.
type watcher struct {
	proto uint32
	left  int    // сколько байт текущего пакета ещё впереди
	head  []byte // заголовок следующего пакета, пока он приходит по частям
	short int    // длина короткого пакета, чей код ещё читаем
	code  []byte // прочитанные байты кода
	off   bool   // поток не разобрать — перестаём следить
}

// scan возвращает байты, которые можно отдать клиенту: первые четыре байта короткого пакета
// придерживаются, пока не станет ясно, код ли это -444. bad — пришёл код -444, отдать нужно
// только возвращённое, дальше соединение закрывается. Заголовок пакета с кодом клиент
// при этом может получить — без тела он безвреден.
func (w *watcher) scan(p []byte) (out []byte, bad bool) {
	pending, split := 0, false // p[pending:] ещё не отдано; split — out собран по частям
	for i := 0; i < len(p); {
		switch {
		case w.off:
			i = len(p)
		case w.left > 0:
			k := min(w.left, len(p)-i)
			w.left -= k
			i += k
		case w.short > 0:
			out, split = append(out, p[pending:i]...), true
			k := min(4-len(w.code), len(p)-i)
			w.code = append(w.code, p[i:i+k]...)
			i += k
			pending = i
			if len(w.code) < 4 {
				continue
			}
			if int32(binary.LittleEndian.Uint32(w.code)) == badDCCode {
				w.code = w.code[:0]
				return out, true
			}
			out = append(out, w.code...)
			w.left, w.short, w.code = w.short-4, 0, w.code[:0]
		default:
			w.head = append(w.head, p[i])
			i++
			size, ok := w.packetSize()
			if !ok {
				continue
			}
			w.head = w.head[:0]
			switch {
			case size <= 0 || size > maxPacket:
				w.off = true
			case size >= 4 && size < 12:
				w.short = size
			default:
				w.left = size
			}
		}
	}
	if !split {
		return p, false
	}
	return append(out, p[pending:]...), false
}

// packetSize — длина тела пакета по заголовку; ok=false — заголовок ещё не пришёл целиком,
// size<=0 — поток не разобрать.
func (w *watcher) packetSize() (size int, ok bool) {
	h := w.head
	switch w.proto {
	case protoAbridged:
		switch {
		case h[0] == 0x7F:
			if len(h) < 4 {
				return 0, false
			}
			return int(uint32(h[1])|uint32(h[2])<<8|uint32(h[3])<<16) * 4, true
		case h[0] > 0x7F: // быстрое подтверждение — такое клиент не заказывает
			return 0, true
		}
		return int(h[0]) * 4, true
	case protoIntermediate, protoPadded:
		if len(h) < 4 {
			return 0, false
		}
		v := binary.LittleEndian.Uint32(h)
		if v&0x80000000 != 0 {
			return 0, true
		}
		return int(v), true
	}
	return 0, true
}
