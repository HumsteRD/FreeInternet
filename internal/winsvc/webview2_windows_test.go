package winsvc

import "testing"

func TestWithPAC(t *testing.T) {
	const ours = "http://127.0.0.1:1454/telegram.pac"
	cases := []struct{ args, pac, want string }{
		{"", ours, "--proxy-pac-url=" + ours},
		{"--mute-audio", ours, "--mute-audio --proxy-pac-url=" + ours},
		{"--proxy-pac-url=http://127.0.0.1:1460/telegram.pac", ours, "--proxy-pac-url=" + ours}, // порт сменился
		{"--mute-audio --proxy-pac-url=" + ours, "", "--mute-audio"},                            // выключили
		{"--proxy-pac-url=" + ours, "", ""},
		{"--proxy-pac-url=http://corp/proxy.pac", ours, "--proxy-pac-url=http://corp/proxy.pac"}, // чужой PAC не трогаем
	}
	for _, c := range cases {
		if got := withPAC(c.args, c.pac); got != c.want {
			t.Errorf("withPAC(%q, %q) = %q, ждали %q", c.args, c.pac, got, c.want)
		}
	}
}
