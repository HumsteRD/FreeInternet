// Package probe проверяет, доступен ли сервис и как именно его блокируют.
package probe

import "time"

// Status — итог проверки одной цели.
type Status string

const (
	OK          Status = "OK"
	DNSFail     Status = "DNS_FAIL"     // хост не резолвится ни системой, ни через DoH
	DNSSpoof    Status = "DNS_SPOOF"    // системный DNS отдаёт заглушку или ничего, DoH — настоящий адрес
	TCPFail     Status = "TCP_FAIL"     // не устанавливается TCP-соединение (блок по IP)
	TLSReset    Status = "TLS_RESET"    // рукопожатие оборвано сбросом — типичный DPI по SNI
	TLSTimeout  Status = "TLS_TIMEOUT"  // рукопожатие повисло — DPI отбрасывает пакеты
	TLSCert     Status = "TLS_CERT"     // чужой сертификат — подмена или страница-заглушка
	Freeze16K   Status = "FREEZE_16K"   // соединение есть, передача замирает на ~16–20 КБ
	ConnReset   Status = "CONN_RESET"   // соединение сброшено уже во время передачи
	Slow        Status = "SLOW"         // данные идут, но скорость как у замедления
	QUICFail    Status = "QUIC_FAIL"    // QUIC/HTTP3 не проходит
	UDPFail     Status = "UDP_FAIL"     // нет ответа STUN — так выглядит блокировка звонков и голоса
	HTTPBlocked Status = "HTTP_BLOCKED" // сервер ответил 451
	Failed      Status = "ERROR"        // прочая ошибка
)

// Kind — что именно проверяем на цели.
type Kind string

const (
	KindTLS    Kind = "tls"    // только рукопожатие
	KindPage   Kind = "page"   // GET и чтение тела: ловит обрыв на 16–20 КБ и замедление
	KindUpload Kind = "upload" // POST 64 КБ случайных данных — метод dpi-checkers для хостингов
	KindQUIC   Kind = "quic"   // рукопожатие QUIC с ALPN h3
	KindSTUN   Kind = "stun"   // STUN Binding Request по UDP — начало звонка
	KindWS     Kind = "ws"     // рукопожатие WebSocket — канал веб-версии Telegram и прокси FI
)

// Target — одна проверяемая точка сервиса.
type Target struct {
	Name   string `json:"name"`
	Group  string `json:"group"`
	Kind   Kind   `json:"kind"`
	Host   string `json:"host"`
	Path   string `json:"path,omitempty"`
	Port   int    `json:"port,omitempty"` // по умолчанию 443, для STUN — 3478
	Weight int    `json:"weight"`         // вклад в оценку группы; критичные для работы сервиса цели весят больше
}

// Result — итог проверки цели.
type Result struct {
	Target     Target        `json:"target"`
	Status     Status        `json:"status"`
	Detail     string        `json:"detail,omitempty"`
	IP         string        `json:"ip,omitempty"`
	TLSVersion string        `json:"tls_version,omitempty"`
	HTTPCode   int           `json:"http_code,omitempty"`
	Bytes      int64         `json:"bytes,omitempty"`
	KBps       float64       `json:"kbps,omitempty"`
	Duration   time.Duration `json:"duration"`
}
