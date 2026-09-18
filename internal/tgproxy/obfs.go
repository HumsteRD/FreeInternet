// Package tgproxy — локальный MTProto-прокси для Telegram Desktop. Трафик клиента
// перешифровывается и уходит к Telegram через WebSocket веб-версии (kwsN.web.telegram.org),
// который не режут вместе с обычным протоколом. Протокол и идея — Flowseal/tg-ws-proxy (MIT).
package tgproxy

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"slices"
)

// Первые 64 байта соединения (obfuscated2): 8 случайных байт, 32 байта ключа, 16 байт IV,
// затем зашифрованные тег протокола и номер дата-центра.
const (
	initLen  = 64
	skipLen  = 8
	keyLen   = 32
	ivLen    = 16
	protoPos = 56
	dcPos    = 60
)

// Транспорты MTProto; тег лежит в заголовке соединения.
const (
	protoAbridged     uint32 = 0xEFEFEFEF
	protoIntermediate uint32 = 0xEEEEEEEE
	protoPadded       uint32 = 0xDDDDDDDD
)

var errBadInit = errors.New("неверное начало соединения: другой секрет или протокол")

// clientInit — что прокси узнал из заголовка клиента.
type clientInit struct {
	dc    int
	media bool   // соединение для загрузки файлов
	proto uint32 // транспорт
	keyIV []byte // байты 8..56 заголовка
}

func newCTR(key, iv []byte) cipher.Stream {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err) // длина ключа задана константой
	}
	return cipher.NewCTR(block, iv)
}

func reversed(b []byte) []byte {
	r := slices.Clone(b)
	slices.Reverse(r)
	return r
}

func secretKey(prekey, secret []byte) []byte {
	sum := sha256.Sum256(append(slices.Clone(prekey), secret...))
	return sum[:]
}

// parseInit расшифровывает заголовок клиента ключом, полученным из секрета прокси.
func parseInit(init, secret []byte) (clientInit, error) {
	keyIV := init[skipLen : skipLen+keyLen+ivLen]
	plain := make([]byte, initLen)
	newCTR(secretKey(keyIV[:keyLen], secret), keyIV[keyLen:]).XORKeyStream(plain, init)

	proto := binary.LittleEndian.Uint32(plain[protoPos:])
	if proto != protoAbridged && proto != protoIntermediate && proto != protoPadded {
		return clientInit{}, errBadInit
	}
	idx := int16(binary.LittleEndian.Uint16(plain[dcPos:]))
	ci := clientInit{dc: int(idx), proto: proto, keyIV: slices.Clone(keyIV)}
	if idx < 0 {
		ci.dc, ci.media = -ci.dc, true
	}
	return ci, nil
}

// Начала, которые сервер Telegram принял бы за другой протокол.
var reservedStarts = [][]byte{
	[]byte("HEAD"), []byte("POST"), []byte("GET "),
	{0xEE, 0xEE, 0xEE, 0xEE}, {0xDD, 0xDD, 0xDD, 0xDD}, {0x16, 0x03, 0x01, 0x02},
}

// newRelayInit создаёт заголовок для соединения прокси с Telegram — обычная обфускация без секрета.
func newRelayInit(proto uint32, dcIdx int16) []byte {
	init := make([]byte, initLen)
	for {
		rand.Read(init)
		if init[0] == 0xEF || binary.LittleEndian.Uint32(init[4:8]) == 0 {
			continue
		}
		if slices.ContainsFunc(reservedStarts, func(s []byte) bool { return bytes.Equal(init[:4], s) }) {
			continue
		}
		break
	}

	keyIV := init[skipLen : skipLen+keyLen+ivLen]
	encrypted := make([]byte, initLen)
	newCTR(keyIV[:keyLen], keyIV[keyLen:]).XORKeyStream(encrypted, init)

	var tail [8]byte
	binary.LittleEndian.PutUint32(tail[0:], proto)
	binary.LittleEndian.PutUint16(tail[4:], uint16(dcIdx))
	rand.Read(tail[6:])
	// Хвост кладём зашифрованным: init ^ encrypted — это ключевой поток на этих позициях.
	for i := range tail {
		init[protoPos+i] = tail[i] ^ encrypted[protoPos+i] ^ init[protoPos+i]
	}
	return init
}

// cryptoCtx — четыре потока шифра одного соединения.
type cryptoCtx struct {
	clientDec cipher.Stream // от клиента
	clientEnc cipher.Stream // к клиенту
	tgEnc     cipher.Stream // к Telegram
	tgDec     cipher.Stream // от Telegram
}

func newCrypto(ci clientInit, secret, relayInit []byte) *cryptoCtx {
	back := reversed(ci.keyIV)
	relayKeyIV := relayInit[skipLen : skipLen+keyLen+ivLen]
	relayBack := reversed(relayKeyIV)

	c := &cryptoCtx{
		// Со стороны клиента ключи зависят от секрета, в обратную сторону — те же байты задом наперёд.
		clientDec: newCTR(secretKey(ci.keyIV[:keyLen], secret), ci.keyIV[keyLen:]),
		clientEnc: newCTR(secretKey(back[:keyLen], secret), back[keyLen:]),
		tgEnc:     newCTR(relayKeyIV[:keyLen], relayKeyIV[keyLen:]),
		tgDec:     newCTR(relayBack[:keyLen], relayBack[keyLen:]),
	}
	// Заголовки уже прочитаны и отправлены: сдвигаем соответствующие потоки на 64 байта.
	skip := make([]byte, initLen)
	c.clientDec.XORKeyStream(skip, skip)
	c.tgEnc.XORKeyStream(skip, skip)
	return c
}
