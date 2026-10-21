package strategy

import (
	"slices"
	"strings"
)

// QUIC у YouTube. В наборе Flowseal правило QUIC (UDP 443) берёт только list-general, а YouTube
// лежит в list-google, правила которого есть лишь для TCP. QUIC до www.youtube.com и i.ytimg.com
// шёл без обхода, провайдер резал его по имени сайта, и плитка YouTube висела «частично».
// С list-google в правиле QUIC рукопожатие проходит (проверено 2026-10-21 на ALT13).

const (
	generalList = "--hostlist={lists}list-general.txt"
	googleList  = "--hostlist={lists}list-google.txt"
)

// withGoogleQUIC добавляет list-google в правила QUIC, которые обходят сайты из list-general.
func withGoogleQUIC(args []string) []string {
	out := make([]string, 0, len(args)+2)
	start := 0
	for i := 0; i <= len(args); i++ {
		if i < len(args) && args[i] != "--new" {
			continue
		}
		out = append(out, googleQUICProfile(args[start:i])...)
		if i < len(args) {
			out = append(out, args[i])
		}
		start = i + 1
	}
	return out
}

// googleQUICProfile — одно правило winws (между --new): если это QUIC с list-general,
// list-google встаёт сразу за ним.
func googleQUICProfile(p []string) []string {
	quic, general, google := false, -1, false
	for i, a := range p {
		switch {
		case strings.HasPrefix(a, "--filter-udp="):
			quic = slices.Contains(strings.Split(strings.TrimPrefix(a, "--filter-udp="), ","), "443")
		case a == generalList:
			general = i
		case a == googleList:
			google = true
		}
	}
	if !quic || general < 0 || google {
		return p
	}
	return slices.Insert(slices.Clone(p), general+1, googleList)
}
