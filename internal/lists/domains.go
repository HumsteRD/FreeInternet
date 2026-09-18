package lists

import (
	"bufio"
	"os"
	"strings"
)

// DomainSet — домены hostlist в формате zapret: запись покрывает поддомены,
// а с префиксом ^ — только сам домен.
type DomainSet struct {
	withSubdomains map[string]bool
	exact          map[string]bool
}

// LoadDomains читает hostlist-файлы; отсутствующие пропускает.
func LoadDomains(paths ...string) (DomainSet, error) {
	set := DomainSet{withSubdomains: map[string]bool{}, exact: map[string]bool{}}
	for _, p := range paths {
		f, err := os.Open(p)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return set, err
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.ToLower(strings.TrimSpace(sc.Text()))
			switch {
			case line == "" || strings.HasPrefix(line, "#"):
			case strings.HasPrefix(line, "^"):
				set.exact[line[1:]] = true
			default:
				set.withSubdomains[line] = true
			}
		}
		f.Close()
		if err := sc.Err(); err != nil {
			return set, err
		}
	}
	return set, nil
}

// Matches сообщает, покрыт ли хост списком.
func (s DomainSet) Matches(host string) bool {
	host = strings.ToLower(host)
	if s.exact[host] || s.withSubdomains[host] {
		return true
	}
	for i := strings.IndexByte(host, '.'); i >= 0; i = strings.IndexByte(host, '.') {
		host = host[i+1:]
		if s.withSubdomains[host] {
			return true
		}
	}
	return false
}
