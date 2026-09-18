package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/net/idna"
	"golang.org/x/net/publicsuffix"

	"fi/internal/lists"
	"fi/internal/probe"
)

// SiteResult — итог добавления сайта для окна «Добавить сайт».
type SiteResult struct {
	Host    string       `json:"host"`
	Verdict string       `json:"verdict"` // works, fixed, not_fixed, added, dns, tunnel, unreachable, not_found, cert
	Before  probe.Status `json:"before"`
	After   probe.Status `json:"after,omitempty"`
	Added   bool         `json:"added"`
	Related []string     `json:"related,omitempty"` // домены, с которых сайт грузит содержимое
	Message string       `json:"message"`
}

// AddSite проверяет сайт при текущем обходе. Если провайдер мешает соединению,
// сайт попадает в список обхода и проверяется снова.
func (d *Daemon) AddSite(ctx context.Context, input string) (SiteResult, error) {
	host, err := NormalizeHost(input)
	if err != nil {
		return SiteResult{}, err
	}
	if !d.beginTask(&Task{Kind: "site", Title: "Проверка " + host}) {
		return SiteResult{}, ErrBusy
	}
	defer d.endTask()

	target := probe.Target{Name: host, Group: userGroup, Kind: probe.KindPage, Host: host, Weight: 1}
	before := d.prober.Check(ctx, target)
	if err := ctx.Err(); err != nil {
		return SiteResult{}, err
	}
	res := SiteResult{Host: host, Before: before.Status}
	action := siteActionFor(before.Status)
	res.Verdict, res.Message = action.verdict, action.message
	if !action.add {
		return res, nil
	}

	d.mu.Lock()
	d.cfg.AddSite(host, describe(before.Status), time.Now())
	err = lists.WriteUserHosts(d.listsDir(), d.bypassHostsLocked())
	if err == nil {
		err = d.saveLocked()
	}
	running := d.proc != nil
	d.mu.Unlock()
	if err != nil {
		return res, err
	}
	res.Added = true

	if !running {
		res.Verdict, res.Message = "added", "Сайт добавлен в список. Он заработает, когда обход будет включён."
		return res, nil
	}
	// winws перечитывает список при следующем соединении, если изменилось время файла.
	select {
	case <-time.After(time.Second):
	case <-ctx.Done():
		return res, ctx.Err()
	}
	after := d.prober.Check(ctx, target)
	res.After = after.Status
	if after.Status == probe.OK || after.Status == probe.Slow {
		res.Verdict, res.Message = "fixed", "Сайт добавлен в список обхода и открывается."
	} else {
		res.Verdict = "not_fixed"
		res.Message = "Сайт добавлен, но текущая стратегия его не открывает (" + describe(after.Status) + "). Попробуйте подобрать стратегию заново."
	}

	// Сайт открылся, но музыка, видео и картинки часто идут с других доменов — их тоже добавим.
	if res.Related = d.addRelated(ctx, host); len(res.Related) > 0 {
		res.Message += " Заодно добавлены домены, с которых сайт грузит содержимое: " + strings.Join(res.Related, ", ") + "."
	}
	return res, nil
}

// relatedPattern — адреса в HTML страницы: по ним видно, откуда сайт грузит содержимое.
var relatedPattern = regexp.MustCompile(`(?i)https?://([a-z0-9][a-z0-9.\-]{1,80}\.[a-z]{2,24})`)

// maxRelated — сколько доменов проверять: больше нет смысла, страницы тянут десятки мелочей.
const maxRelated = 12

// addRelated находит домены, с которых сайт грузит содержимое, проверяет их и добавляет в список
// те, которым мешает провайдер. Так не приходится искать вручную, что ещё разблокировать.
func (d *Daemon) addRelated(ctx context.Context, host string) []string {
	ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()

	candidates := relatedHosts(ctx, d.http, host)
	if len(candidates) == 0 {
		return nil
	}
	targets := make([]probe.Target, 0, len(candidates))
	for _, c := range candidates {
		targets = append(targets, probe.Target{Name: c, Group: userGroup, Kind: probe.KindTLS, Host: c, Weight: 1})
	}
	var blocked []string
	for _, r := range d.prober.Run(ctx, targets) {
		if r.Status != probe.OK && r.Status != probe.Slow {
			blocked = append(blocked, r.Target.Host)
		}
	}
	if len(blocked) == 0 || ctx.Err() != nil {
		return nil
	}

	d.mu.Lock()
	added := make([]string, 0, len(blocked))
	for _, h := range blocked {
		if d.cfg.AddSite(h, "нужен для "+host, time.Now()) {
			added = append(added, h)
		}
	}
	err := lists.WriteUserHosts(d.listsDir(), d.bypassHostsLocked())
	if err == nil {
		err = d.saveLocked()
	}
	d.mu.Unlock()
	if err != nil {
		d.log.Warn("связанные домены не сохранены", "err", err)
		return nil
	}
	d.log.Info("добавлены связанные домены", "site", host, "hosts", strings.Join(added, ", "))
	return added
}

// relatedHosts берёт страницу сайта и вытаскивает домены, на которые она ссылается.
func relatedHosts(ctx context.Context, client *http.Client, host string) []string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+host+"/", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	page, err := io.ReadAll(io.LimitReader(resp.Body, 512<<10))
	if err != nil {
		return nil
	}

	own, _ := publicsuffix.EffectiveTLDPlusOne(host)
	var found []string
	for _, m := range relatedPattern.FindAllSubmatch(page, -1) {
		name := strings.ToLower(strings.Trim(string(m[1]), "."))
		domain, err := publicsuffix.EffectiveTLDPlusOne(name)
		if err != nil || domain == own || domain == host || slices.Contains(found, domain) {
			continue
		}
		if !hostPattern.MatchString(domain) {
			continue
		}
		if found = append(found, domain); len(found) == maxRelated {
			break
		}
	}
	return found
}

// RemoveSite убирает сайт из списка обхода.
func (d *Daemon) RemoveSite(host string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.cfg.RemoveSite(host) {
		return fmt.Errorf("сайта %s нет в списке", host)
	}
	if err := lists.WriteUserHosts(d.listsDir(), d.bypassHostsLocked()); err != nil {
		return err
	}
	return d.saveLocked()
}

type siteAction struct {
	verdict, message string
	add              bool
}

// siteActionFor решает по первой проверке, поможет ли список обхода.
func siteActionFor(s probe.Status) siteAction {
	switch s {
	case probe.OK, probe.Slow:
		return siteAction{"works", "Сайт открывается и без добавления.", false}
	case probe.TLSReset, probe.TLSTimeout, probe.Freeze16K, probe.ConnReset, probe.QUICFail, probe.Failed:
		return siteAction{"dpi", "Провайдер мешает соединению — сайт добавлен в список обхода.", true}
	case probe.DNSSpoof:
		return siteAction{"dns", "Провайдер подменяет адрес сайта. Поможет защищённый DNS в настройках.", false}
	case probe.HTTPBlocked:
		return siteAction{"tunnel", "Сайт сам ограничивает доступ из России. Нужен туннель через другую страну.", false}
	case probe.TCPFail:
		return siteAction{"unreachable", "Сервер не отвечает: он заблокирован по адресу или не работает. Список обхода тут не поможет.", false}
	case probe.DNSFail:
		return siteAction{"not_found", "Такой сайт не найден. Проверьте адрес.", false}
	case probe.TLSCert:
		return siteAction{"cert", "Сайт отвечает чужим сертификатом — похоже на подмену. Добавлять не будем.", false}
	}
	return siteAction{"unknown", describe(s), false}
}

var hostPattern = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z0-9-]{2,63}$`)

// NormalizeHost превращает введённый адрес («https://www.x.com/home») в домен для списка («x.com»).
// Кириллические домены переводятся в punycode, как их видит winws.
func NormalizeHost(input string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(input))
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	}
	if i := strings.IndexAny(s, "/?#"); i >= 0 {
		s = s[:i]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if h, _, err := net.SplitHostPort(s); err == nil {
		s = h
	}
	s = strings.TrimSuffix(strings.TrimPrefix(s, "www."), ".")

	invalid := fmt.Errorf("не похоже на адрес сайта: %q", strings.TrimSpace(input))
	if s == "" || net.ParseIP(s) != nil {
		return "", invalid
	}
	ascii, err := idna.Lookup.ToASCII(s)
	if err != nil || !hostPattern.MatchString(ascii) {
		return "", invalid
	}
	return ascii, nil
}
