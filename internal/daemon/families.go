package daemon

import (
	"slices"
	"strings"

	"golang.org/x/net/publicsuffix"
)

// siteFamilies — домены, без которых сайт работает лишь наполовину: с них идут музыка, видео,
// картинки и скрипты. На главной странице их обычно не видно (потоковые серверы SoundCloud
// появляются, только когда трек запущен), поэтому их список ведётся вручную.
// Поддомены отдельно указывать не нужно: список обхода winws охватывает их сам.
var siteFamilies = [][]string{
	{"soundcloud.com", "sndcdn.com", "soundcloud.cloud"},
	{"instagram.com", "cdninstagram.com", "fbcdn.net"},
	{"facebook.com", "fbcdn.net", "facebook.net", "fb.com"},
	{"x.com", "twitter.com", "twimg.com", "t.co"},
	{"twitch.tv", "ttvnw.net", "jtvnw.net"},
	{"tiktok.com", "tiktokcdn.com", "tiktokv.com"},
	{"linkedin.com", "licdn.com"},
	{"reddit.com", "redd.it", "redditmedia.com", "redditstatic.com"},
	{"pinterest.com", "pinimg.com"},
	{"vimeo.com", "vimeocdn.com"},
	{"bandcamp.com", "bcbits.com"},
	{"patreon.com", "patreonusercontent.com"},
	{"chatgpt.com", "openai.com", "oaistatic.com", "oaiusercontent.com"},
	{"deviantart.com", "wixmp.com"},
	{"quora.com", "quoracdn.net"},
	{"whatsapp.com", "whatsapp.net"},
	{"spotify.com", "scdn.co", "spotifycdn.com"},
}

// familyOf — домены, которые нужны сайту host вместе с ним (без него самого).
func familyOf(host string) []string {
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		domain = host
	}
	var out []string
	for _, family := range siteFamilies {
		if !slices.Contains(family, domain) {
			continue
		}
		for _, d := range family {
			if d != domain && d != host && !slices.Contains(out, d) {
				out = append(out, d)
			}
		}
	}
	return out
}

// sameBrand — домен related принадлежит тому же сервису, что и host: в имени то же слово
// (soundcloud.com и soundcloud.cloud, discord.com и discordapp.net). Короткие имена
// не сравниваем: «x» или «vk» совпали бы с чем угодно.
func sameBrand(host, related string) bool {
	brand := brandOf(host)
	return len(brand) >= 4 && strings.Contains(brandOf(related), brand)
}

func brandOf(host string) string {
	domain, err := publicsuffix.EffectiveTLDPlusOne(host)
	if err != nil {
		return ""
	}
	label, _, _ := strings.Cut(domain, ".")
	return label
}
