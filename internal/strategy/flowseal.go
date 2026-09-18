package strategy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var flowsealVars = strings.NewReplacer(
	"%BIN%", "{bin}",
	"%LISTS%", "{lists}",
	"%GameFilterTCP%", "{game_tcp}",
	"%GameFilterUDP%", "{game_udp}",
)

var batVar = regexp.MustCompile(`%[~\w]+%?`)

// ImportFlowsealDir импортирует все general*.bat из папки zapret-discord-youtube
// в естественном порядке (ALT2 раньше ALT10).
func ImportFlowsealDir(dir string) ([]Strategy, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "general*.bat"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("в %s нет general*.bat", dir)
	}
	sort.Slice(paths, func(i, j int) bool {
		return naturalKey(filepath.Base(paths[i])) < naturalKey(filepath.Base(paths[j]))
	})

	strategies := make([]Strategy, 0, len(paths))
	for _, p := range paths {
		s, err := ParseFlowsealBat(p)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(p), err)
		}
		strategies = append(strategies, s)
	}
	return strategies, nil
}

// ParseFlowsealBat извлекает аргументы winws из одного .bat.
func ParseFlowsealBat(path string) (Strategy, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Strategy{}, err
	}
	cmdline, err := winwsCommandLine(string(data))
	if err != nil {
		return Strategy{}, err
	}

	args := splitArgs(cmdline)
	for i, a := range args {
		args[i] = flowsealVars.Replace(a)
		if v := batVar.FindString(args[i]); v != "" {
			return Strategy{}, fmt.Errorf("неизвестная переменная %s в %q", v, a)
		}
	}
	base := filepath.Base(path)
	return Strategy{
		Name:   strings.TrimSuffix(base, filepath.Ext(base)),
		Source: base,
		Args:   args,
	}, nil
}

// winwsCommandLine склеивает строку запуска winws.exe с продолжениями через ^
// и возвращает всё, что идёт после пути к exe.
func winwsCommandLine(bat string) (string, error) {
	const exe = `winws.exe"`
	var b strings.Builder
	collecting := false
	for _, line := range strings.Split(bat, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "::") || strings.HasPrefix(strings.ToLower(line), "rem ") {
			continue
		}
		if !collecting {
			i := strings.Index(line, exe)
			if i < 0 {
				continue
			}
			collecting = true
			line = line[i+len(exe):]
		}
		more := strings.HasSuffix(line, "^")
		b.WriteString(strings.TrimSuffix(line, "^"))
		b.WriteByte(' ')
		if !more {
			break
		}
	}
	if !collecting {
		return "", errors.New("строка запуска winws.exe не найдена")
	}
	return b.String(), nil
}

// splitArgs делит строку по пробелам вне кавычек и снимает кавычки:
// --hostlist="%LISTS%a b.txt" → --hostlist=%LISTS%a b.txt.
// Вне кавычек ^ экранирует следующий символ, как в cmd: --dpi-desync-fake-tls=^! → =!.
func splitArgs(s string) []string {
	var (
		args    []string
		cur     strings.Builder
		inQuote bool
		started bool
		escaped bool
	)
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			started = true
			escaped = false
		case r == '^' && !inQuote:
			escaped = true
		case r == '"':
			inQuote = !inQuote
			started = true
		case (r == ' ' || r == '\t') && !inQuote:
			if started {
				args = append(args, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if started {
		args = append(args, cur.String())
	}
	return args
}

var digits = regexp.MustCompile(`\d+`)

func naturalKey(s string) string {
	return digits.ReplaceAllStringFunc(s, func(d string) string {
		return strings.Repeat("0", max(0, 8-len(d))) + d
	})
}
