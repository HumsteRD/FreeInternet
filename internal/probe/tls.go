package probe

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	utls "github.com/refraction-networking/utls"
)

const (
	// freezeLimit — замирание раньше этого объёма похоже на обрыв «16–20 КБ»
	// (с запасом на рукопожатие и заголовки).
	freezeLimit = 64 << 10
	// freezeProof — ответ такого размера, дошедший целиком, доказывает, что обрыва нет.
	freezeProof = 32 << 10
	// speedSample — на меньшем объёме скорость определяется задержкой, а не пропускной способностью.
	speedSample = 128 << 10
)

// dialTLS устанавливает TLS с отпечатком Chrome: DPI может по-разному обращаться
// с ClientHello Go и браузера. ALPN сужен до http/1.1, чтобы говорить HTTP вручную.
func (p *Prober) dialTLS(ctx context.Context, host, addr string) (*utls.UConn, error) {
	d := net.Dialer{Timeout: p.ConnectTimeout}
	raw, err := d.DialContext(ctx, "tcp4", addr)
	if err != nil {
		return nil, &stageError{TCPFail, err}
	}

	spec, err := utls.UTLSIdToSpec(utls.HelloChrome_Auto)
	if err != nil {
		raw.Close()
		return nil, err
	}
	for _, ext := range spec.Extensions {
		if alpn, ok := ext.(*utls.ALPNExtension); ok {
			alpn.AlpnProtocols = []string{"http/1.1"}
		}
	}

	conn := utls.UClient(raw, &utls.Config{ServerName: host, RootCAs: p.rootCAs}, utls.HelloCustom)
	if err := conn.ApplyPreset(&spec); err != nil {
		raw.Close()
		return nil, err
	}

	hctx, cancel := context.WithTimeout(ctx, p.ConnectTimeout)
	defer cancel()
	if err := conn.HandshakeContext(hctx); err != nil {
		conn.Close()
		switch {
		case isCertError(err):
			return nil, &stageError{TLSCert, err}
		case isTimeout(err):
			return nil, &stageError{TLSTimeout, err}
		case isReset(err):
			return nil, &stageError{TLSReset, err}
		default:
			return nil, &stageError{Failed, err}
		}
	}
	return conn, nil
}

func (p *Prober) checkTLS(ctx context.Context, t Target, r *Result) {
	conn, err := p.dialTLS(ctx, t.Host, net.JoinHostPort(r.IP, p.portFor(t, 443)))
	if err != nil {
		r.fail(err)
		return
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	r.TLSVersion = tlsVersionName(conn.ConnectionState().Version)
	switch t.Kind {
	case KindPage:
		p.fetchPage(conn, t, r)
	case KindUpload:
		p.upload(conn, t, r)
	case KindWS:
		p.upgrade(conn, t, r)
	default:
		r.Status = OK
	}
}

// fetchPage читает тело страницы и смотрит, где и как оборвалась передача.
func (p *Prober) fetchPage(conn net.Conn, t Target, r *Result) {
	path := t.Path
	if path == "" {
		path = "/"
	}
	req, err := http.NewRequest(http.MethodGet, "https://"+t.Host+path, nil)
	if err != nil {
		r.fail(err)
		return
	}
	setBrowserHeaders(req)

	conn.SetDeadline(time.Now().Add(p.StallTimeout))
	if err := req.Write(conn); err != nil {
		r.fail(transferError(err, 0))
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		r.fail(transferError(err, 0))
		return
	}
	defer resp.Body.Close()
	r.HTTPCode = resp.StatusCode
	conn.SetWriteDeadline(time.Time{})

	start := time.Now()
	buf := make([]byte, 32<<10)
	var readErr error
	for r.Bytes < p.MaxBytes {
		conn.SetReadDeadline(time.Now().Add(p.StallTimeout))
		n, err := resp.Body.Read(buf)
		r.Bytes += int64(n)
		if err != nil {
			if err != io.EOF {
				readErr = err
			}
			break
		}
	}
	elapsed := time.Since(start)

	if readErr != nil {
		r.fail(transferError(readErr, r.Bytes))
		return
	}
	if resp.StatusCode == http.StatusUnavailableForLegalReasons {
		r.Status = HTTPBlocked
		return
	}
	r.Status = OK
	if r.Bytes >= freezeProof && elapsed > 0 {
		r.KBps = float64(r.Bytes) / 1024 / elapsed.Seconds()
	}
	if r.Bytes >= speedSample && r.KBps < p.SlowKBps {
		r.Status = Slow
	}
	if r.Bytes < freezeProof {
		r.Detail = fmt.Sprintf("ответ всего %d КБ — обрыв на 16–20 КБ этой целью не проверить", r.Bytes>>10)
	}
}

// upload отправляет 64 КБ и ждёт любой HTTP-ответ. При блокировке «16–20 КБ»
// данные перестают доходить, и ответ не приходит.
func (p *Prober) upload(conn net.Conn, t Target, r *Result) {
	body := make([]byte, freezeLimit)
	rand.Read(body)
	req, err := http.NewRequest(http.MethodPost, "https://"+t.Host+"/", bytes.NewReader(body))
	if err != nil {
		r.fail(err)
		return
	}
	setBrowserHeaders(req)
	req.Header.Set("Content-Type", "application/octet-stream")

	conn.SetDeadline(time.Now().Add(2 * p.StallTimeout))
	writeErr := req.Write(conn)
	// Сервер вправе ответить и закрыть соединение, не дочитав тело, — поэтому
	// ответ пробуем прочитать даже после ошибки записи.
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		if writeErr != nil {
			err = writeErr
		}
		r.fail(transferError(err, 0))
		return
	}
	resp.Body.Close()
	r.HTTPCode = resp.StatusCode
	r.Bytes = int64(len(body))
	r.Status = OK
}

// upgrade проверяет переход на WebSocket: по этому каналу ходят веб-версия Telegram
// и прокси FI, и его режут отдельно от обычных страниц.
func (p *Prober) upgrade(conn net.Conn, t Target, r *Result) {
	path := t.Path
	if path == "" {
		path = "/"
	}
	req, err := http.NewRequest(http.MethodGet, "https://"+t.Host+path, nil)
	if err != nil {
		r.fail(err)
		return
	}
	nonce := make([]byte, 16)
	rand.Read(nonce)
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Sec-WebSocket-Key", base64.StdEncoding.EncodeToString(nonce))
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Protocol", "binary")

	conn.SetDeadline(time.Now().Add(p.StallTimeout))
	if err := req.Write(conn); err != nil {
		r.fail(transferError(err, 0))
		return
	}
	resp, err := http.ReadResponse(bufio.NewReader(conn), req)
	if err != nil {
		r.fail(transferError(err, 0))
		return
	}
	r.HTTPCode = resp.StatusCode
	if resp.StatusCode != http.StatusSwitchingProtocols {
		r.Status, r.Detail = Failed, fmt.Sprintf("сервер ответил %d вместо перехода на WebSocket", resp.StatusCode)
		return
	}
	r.Status = OK
}

func transferError(err error, got int64) error {
	switch {
	case isTimeout(err) && got < freezeLimit:
		return &stageError{Freeze16K, fmt.Errorf("передача замерла на %d КБ", got>>10)}
	case isTimeout(err):
		return &stageError{Slow, fmt.Errorf("передача замерла после %d КБ", got>>10)}
	case isReset(err):
		return &stageError{ConnReset, fmt.Errorf("сброс на %d КБ: %w", got>>10, err)}
	default:
		return err
	}
}

func setBrowserHeaders(req *http.Request) {
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	req.Header.Set("Connection", "close")
}

func tlsVersionName(v uint16) string {
	switch v {
	case utls.VersionTLS13:
		return "1.3"
	case utls.VersionTLS12:
		return "1.2"
	default:
		return fmt.Sprintf("0x%04x", v)
	}
}
