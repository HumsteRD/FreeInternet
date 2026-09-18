package tgproxy

import "encoding/binary"

// maxPacket — пакеты крупнее считаем мусором и перестаём резать поток.
const maxPacket = 16 << 20

// splitter режет расшифрованный поток клиента на пакеты транспорта MTProto:
// веб-сервер Telegram ждёт ровно один пакет в одном кадре WebSocket.
type splitter struct {
	proto    uint32
	buf      []byte
	disabled bool // поток не разобрать — отправляем как есть
}

// push добавляет данные и возвращает готовые пакеты; неполный хвост ждёт продолжения.
func (s *splitter) push(p []byte) [][]byte {
	if s.disabled {
		return [][]byte{append([]byte(nil), p...)}
	}
	s.buf = append(s.buf, p...)
	var packets [][]byte
	for len(s.buf) > 0 {
		n, ok := s.next()
		if !ok {
			break
		}
		if n <= 0 {
			packets = append(packets, s.buf)
			s.buf, s.disabled = nil, true
			break
		}
		packets = append(packets, append([]byte(nil), s.buf[:n]...))
		s.buf = s.buf[n:]
	}
	if len(s.buf) == 0 {
		s.buf = nil
	}
	return packets
}

// flush отдаёт недособранный хвост, когда клиент закрыл соединение.
func (s *splitter) flush() [][]byte {
	if len(s.buf) == 0 {
		return nil
	}
	tail := s.buf
	s.buf = nil
	return [][]byte{tail}
}

// next — длина первого пакета в буфере; ok=false — данных пока мало, n<=0 — поток не разобрать.
func (s *splitter) next() (n int, ok bool) {
	b := s.buf
	switch s.proto {
	case protoAbridged:
		header, payload := 1, int(b[0]&0x7F)*4
		if b[0] == 0x7F || b[0] == 0xFF {
			if len(b) < 4 {
				return 0, false
			}
			header, payload = 4, int(uint32(b[1])|uint32(b[2])<<8|uint32(b[3])<<16)*4
		}
		return packetLen(len(b), header, payload)
	case protoIntermediate, protoPadded:
		if len(b) < 4 {
			return 0, false
		}
		return packetLen(len(b), 4, int(binary.LittleEndian.Uint32(b)&0x7FFFFFFF))
	}
	return 0, true
}

func packetLen(have, header, payload int) (int, bool) {
	if payload <= 0 || payload > maxPacket {
		return 0, true
	}
	if have < header+payload {
		return 0, false
	}
	return header + payload, true
}
