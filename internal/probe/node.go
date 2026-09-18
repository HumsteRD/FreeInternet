package probe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"
)

// videoRedirector — общий адрес видеосерверов YouTube; проверка идёт на нём, пока узел сети не известен.
const videoRedirector = "redirector.googlevideo.com"

// reportMappingURL отвечает строкой «<адрес> => <кластер> : router: …» — какой кластер кэша
// YouTube обслуживает адрес, с которого пришёл запрос.
var reportMappingURL = "https://" + videoRedirector + "/report_mapping?di=no"

// lookupHost — подмена DNS в тестах.
var lookupHost = net.DefaultResolver.LookupHost

var mappingCluster = regexp.MustCompile(`=>\s*([a-z0-9-]+)`)

const nodeAlphabet = "0123456789abcdefghijklmnopqrstuvwxyz"

// NodeHost переводит имя кластера в адрес видеосервера: «svo04s41» → «rr1---sn-n8v7znze.googlevideo.com».
// Google записывает имя шифром: символ алфавита 0–9a–z с номером x заменяется символом с номером
// (7x+7) mod 36, дефис остаётся. Правило выведено по обратным DNS-записям узлов (2026-09-15).
func NodeHost(cluster string) (string, bool) {
	if cluster == "" {
		return "", false
	}
	var b strings.Builder
	for _, c := range cluster {
		if c == '-' {
			b.WriteByte('-')
			continue
		}
		x := strings.IndexRune(nodeAlphabet, c)
		if x < 0 {
			return "", false
		}
		b.WriteByte(nodeAlphabet[(7*x+7)%36])
	}
	return "rr1---sn-" + b.String() + ".googlevideo.com", true
}

// nodeAttempts — сколько раз спрашивать redirector: он называет то кластер («ams17s15»),
// то транзитную зону («tzamsa-bb»), у которой своего видеосервера нет.
const nodeAttempts = 5

// FindYouTubeNode узнаёт у Google видеосервер, с которого YouTube отдаёт видео этой сети,
// и проверяет, что такой адрес есть в DNS. Проверять стоит именно его: общий redirector
// может отвечать, пока видео с узла сети идёт с перебоями.
func FindYouTubeNode(ctx context.Context, client *http.Client) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var err error
	for range nodeAttempts {
		var host string
		var again bool
		if host, again, err = askNode(ctx, client); err == nil {
			return host, nil
		}
		if !again || ctx.Err() != nil {
			break
		}
	}
	return "", err
}

// askNode задаёт вопрос один раз; again — ответ пришёл, но узла по нему нет, стоит спросить снова.
func askNode(ctx context.Context, client *http.Client) (host string, again bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reportMappingURL, nil)
	if err != nil {
		return "", false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", false, fmt.Errorf("кластер YouTube: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, fmt.Errorf("кластер YouTube: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if err != nil {
		return "", false, fmt.Errorf("кластер YouTube: %w", err)
	}

	m := mappingCluster.FindSubmatch(bytes.ToLower(body))
	if m == nil {
		return "", false, fmt.Errorf("кластер YouTube: непонятный ответ %.60q", body)
	}
	host, ok := NodeHost(string(m[1]))
	if !ok {
		return "", true, fmt.Errorf("кластер YouTube: странное имя %q", m[1])
	}
	if _, err := lookupHost(ctx, host); err != nil {
		return "", true, fmt.Errorf("кластер YouTube %s: адреса %s нет в DNS", m[1], host)
	}
	return host, false, nil
}

// WithYouTubeNode направляет проверку видеосервера YouTube на узел этой сети.
func WithYouTubeNode(targets []Target, host string) []Target {
	if host == "" {
		return targets
	}
	out := slices.Clone(targets)
	for i := range out {
		if out[i].Host == videoRedirector {
			out[i].Host = host
			out[i].Name = "YouTube: видеосервер вашей сети"
		}
	}
	return out
}
