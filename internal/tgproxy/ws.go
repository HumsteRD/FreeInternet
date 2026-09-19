package tgproxy

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

var errTooLarge = errors.New("слишком большое сообщение WebSocket")

// rootCAs — подмена корневых сертификатов для тестов.
var rootCAs *x509.CertPool

// wsConn — минимальный клиент WebSocket: только двоичные сообщения, чего хватает MTProto.
// raw — на том конце не сервер Telegram, а ретранслятор в TCP (Cloudflare Worker): границы
// сообщений ему безразличны, поэтому пакеты MTProto не нужно раскладывать по кадрам.
type wsConn struct {
	conn      net.Conn
	br        *bufio.Reader
	raw       bool
	viaWorker bool   // соединение прошло через Cloudflare Worker пользователя
	viaCF     bool   // через общий домен Cloudflare
	host      string // домен, к которому открыт WebSocket
	cfBase    string // общий домен Cloudflare, если соединение через него
	wmu       sync.Mutex
}

// statusError — сервер ответил не переходом на WebSocket.
type statusError struct {
	code     int
	location string
}

func (e *statusError) Error() string {
	if e.location != "" {
		return fmt.Sprintf("HTTP %d → %s", e.code, e.location)
	}
	return fmt.Sprintf("HTTP %d", e.code)
}

// dialWS открывает wss://host/path, подключаясь к addr. Сертификат проверяется по host,
// поэтому подключение к адресу напрямую не ослабляет защиту.
func dialWS(ctx context.Context, addr, host, path string) (*wsConn, error) {
	var d net.Dialer
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	conn := tls.Client(raw, &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}, RootCAs: rootCAs})
	if deadline, ok := ctx.Deadline(); ok {
		conn.SetDeadline(deadline)
	}
	if err := conn.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}

	nonce := make([]byte, 16)
	rand.Read(nonce)
	key := base64.StdEncoding.EncodeToString(nonce)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + host + "\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\n" +
		"Sec-WebSocket-Version: 13\r\n" +
		"Sec-WebSocket-Protocol: binary\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		conn.Close()
		return nil, err
	}

	br := bufio.NewReaderSize(conn, 64<<10)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		resp.Body.Close()
		conn.Close()
		return nil, &statusError{code: resp.StatusCode, location: resp.Header.Get("Location")}
	}
	if resp.Header.Get("Sec-WebSocket-Accept") != acceptKey(key) {
		conn.Close()
		return nil, errors.New("сервер WebSocket ответил неверным ключом")
	}
	conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, br: br, host: host}, nil
}

func acceptKey(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func (w *wsConn) framed() bool { return !w.raw }

func (w *wsConn) send(p []byte) error { return w.writeFrame(opBinary, p) }

// writeFrame пишет один кадр; клиент обязан маскировать данные.
func (w *wsConn) writeFrame(op byte, p []byte) error {
	var header [14]byte
	header[0] = 0x80 | op
	n := 2
	switch l := len(p); {
	case l < 126:
		header[1] = 0x80 | byte(l)
	case l < 1<<16:
		header[1] = 0x80 | 126
		binary.BigEndian.PutUint16(header[2:], uint16(l))
		n = 4
	default:
		header[1] = 0x80 | 127
		binary.BigEndian.PutUint64(header[2:], uint64(l))
		n = 10
	}
	mask := header[n : n+4]
	rand.Read(mask)
	n += 4

	frame := make([]byte, n+len(p))
	copy(frame, header[:n])
	for i, b := range p {
		frame[n+i] = b ^ mask[i%4]
	}
	w.wmu.Lock()
	defer w.wmu.Unlock()
	_, err := w.conn.Write(frame)
	return err
}

// recv возвращает следующее сообщение; на ping отвечает сам, закрытие даёт io.EOF.
func (w *wsConn) recv() ([]byte, error) {
	var msg []byte
	for {
		fin, op, payload, err := w.readFrame()
		if err != nil {
			return nil, err
		}
		switch op {
		case opClose:
			w.writeFrame(opClose, payload[:min(2, len(payload))])
			return nil, io.EOF
		case opPing:
			if err := w.writeFrame(opPong, payload); err != nil {
				return nil, err
			}
		case opPong:
		case opContinuation, opText, opBinary:
			if fin && msg == nil {
				return payload, nil
			}
			msg = append(msg, payload...)
			if len(msg) > maxPacket {
				return nil, errTooLarge
			}
			if fin {
				return msg, nil
			}
		}
	}
}

func (w *wsConn) readFrame() (fin bool, op byte, payload []byte, err error) {
	var h [2]byte
	if _, err = io.ReadFull(w.br, h[:]); err != nil {
		return
	}
	fin, op = h[0]&0x80 != 0, h[0]&0x0F
	length := uint64(h[1] & 0x7F)
	switch length {
	case 126:
		var b [2]byte
		if _, err = io.ReadFull(w.br, b[:]); err != nil {
			return
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	case 127:
		var b [8]byte
		if _, err = io.ReadFull(w.br, b[:]); err != nil {
			return
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > maxPacket {
		err = errTooLarge
		return
	}
	var mask [4]byte
	masked := h[1]&0x80 != 0
	if masked {
		if _, err = io.ReadFull(w.br, mask[:]); err != nil {
			return
		}
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(w.br, payload); err != nil {
		return
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return
}

// alive проверяет простаивающее соединение, не забирая из него данных. Сервер ничего не шлёт
// первым, поэтому и закрытие, и любые пришедшие байты значат, что брать соединение нельзя.
func (w *wsConn) alive() bool {
	if w.br.Buffered() > 0 {
		return false
	}
	w.conn.SetReadDeadline(time.Now().Add(time.Millisecond))
	_, err := w.br.Peek(1)
	w.conn.SetReadDeadline(time.Time{})
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

func (w *wsConn) close() {
	w.writeFrame(opClose, nil)
	w.conn.Close()
}
