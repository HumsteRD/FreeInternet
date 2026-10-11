package strategy

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Свои стратегии человек добавляет сам — из .bat другого набора или строкой аргументов winws.
// winws работает от имени системы, поэтому чужие аргументы проверяются строго: разрешены только
// известные параметры обхода, а файлы — только из папок bin и lists установленного набора.
// Так через стратегию нельзя ни записать файл (--debug=@…, --hostlist-auto), ни прочитать чужой.

// CustomPrefix — с чего начинается название своей стратегии в списке.
const CustomPrefix = "свой: "

// maxCustomArgs — больше аргументов в стратегии не бывает; длиннее — скорее мусор.
const maxCustomArgs = 400

var (
	customName = regexp.MustCompile(`^[\p{L}\p{N} _().,+\-]{1,40}$`)
	fileName   = regexp.MustCompile(`^[A-Za-z0-9_.\- ]{1,80}$`)
	hexValue   = regexp.MustCompile(`^0x[0-9A-Fa-f]+$`)
	builtinVal = regexp.MustCompile(`^![A-Za-z0-9=,+_.\-]*$`) // «!» — встроенная подделка winws
	portsValue = regexp.MustCompile(`^~?[0-9{}a-z_\-]+(,~?[0-9{}a-z_\-]+)*$`)
)

// allowedOptions — параметры winws, которые можно задать в своей стратегии.
var allowedOptions = map[string]bool{
	"--wf-tcp": true, "--wf-udp": true, "--wf-l3": true, "--wf-filter-lan": true,
	"--filter-tcp": true, "--filter-udp": true, "--filter-l3": true, "--filter-l7": true,
	"--new": true, "--skip": true, "--comment": true,
	"--hostlist": true, "--hostlist-exclude": true, "--hostlist-domains": true, "--hostlist-exclude-domains": true,
	"--ipset": true, "--ipset-exclude": true, "--ipset-ip": true, "--ipset-exclude-ip": true,
	"--ip-id": true, "--hostcase": true, "--hostspell": true, "--hostnospace": true, "--domcase": true,
	"--methodeol": true, "--synack-split": true, "--wssize": true, "--wssize-cutoff": true,
	"--ctrack-timeouts": true, "--ipcache-lifetime": true, "--ipcache-hostname": true,
}

// allowedPrefixes — семейства параметров подделки и дублирования пакетов.
var allowedPrefixes = []string{"--dpi-desync", "--dup", "--orig-"}

// fileOptions — параметры, значение которых — путь к файлу.
var fileOptions = map[string]bool{
	"--hostlist": true, "--hostlist-exclude": true, "--ipset": true, "--ipset-exclude": true,
}

func isFileOption(name string) bool {
	if fileOptions[name] {
		return true
	}
	if strings.HasSuffix(name, "-pattern") {
		return true
	}
	// --dpi-desync-fake-tls, --dpi-desync-fake-quic… — файлы с подделкой; …-mod — нет.
	return strings.HasPrefix(name, "--dpi-desync-fake-") && !strings.HasSuffix(name, "-mod")
}

func isPortsOption(name string) bool {
	switch name {
	case "--wf-tcp", "--wf-udp", "--filter-tcp", "--filter-udp":
		return true
	}
	return false
}

// ParseCustom разбирает свою стратегию: содержимое .bat (берётся строка запуска winws.exe)
// или строку аргументов, можно в несколько строк. Переменные Flowseal (%BIN%, %LISTS%,
// %GameFilterTCP%…) становятся плейсхолдерами FI.
func ParseCustom(name, text string) (Strategy, error) {
	name = strings.TrimSpace(name)
	if !customName.MatchString(name) {
		return Strategy{}, errors.New("название — до 40 букв, цифр, пробелов и знаков _ ( ) . , + -")
	}
	text = strings.TrimPrefix(text, string(rune(0xFEFF)))
	text = strings.ReplaceAll(text, "\r", "")
	var cmdline string
	if strings.Contains(strings.ToLower(text), `winws.exe"`) {
		var err error
		if cmdline, err = winwsCommandLine(text); err != nil {
			return Strategy{}, err
		}
	} else {
		var lines []string
		for line := range strings.Lines(text) {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "::") {
				continue
			}
			lines = append(lines, strings.TrimSuffix(line, "^"))
		}
		cmdline = strings.Join(lines, " ")
	}
	args := splitArgs(cmdline)
	for i, a := range args {
		args[i] = flowsealVars.Replace(a)
		if v := batVar.FindString(args[i]); v != "" {
			return Strategy{}, fmt.Errorf("неизвестная переменная %s в %q", v, a)
		}
	}
	if err := ValidateCustom(args); err != nil {
		return Strategy{}, err
	}
	return Strategy{Name: CustomPrefix + name, Args: args}, nil
}

// ValidateCustom проверяет аргументы своей стратегии.
func ValidateCustom(args []string) error {
	switch {
	case len(args) == 0:
		return errors.New("в стратегии нет аргументов winws")
	case len(args) > maxCustomArgs:
		return fmt.Errorf("слишком много аргументов: %d", len(args))
	}
	hasFilter := false
	for _, a := range args {
		name, value, hasValue := strings.Cut(a, "=")
		if !strings.HasPrefix(name, "--") {
			return fmt.Errorf("%q: ждали параметр вида --имя=значение", a)
		}
		if strings.ContainsAny(a, "@\x00\n") {
			return fmt.Errorf("%q: ссылки на файлы через @ запрещены", a)
		}
		if !optionAllowed(name) {
			return fmt.Errorf("параметр %s в своей стратегии запрещён", name)
		}
		if name == "--wf-tcp" || name == "--wf-udp" {
			hasFilter = true
		}
		switch {
		case !hasValue:
		case isFileOption(name):
			if err := checkFileValue(name, value); err != nil {
				return err
			}
		case isPortsOption(name):
			if !portsValue.MatchString(value) {
				return fmt.Errorf("%s: странный список портов %q", name, value)
			}
		}
	}
	if !hasFilter {
		return errors.New("нет --wf-tcp или --wf-udp: winws не будет знать, какие пакеты перехватывать")
	}
	return nil
}

func optionAllowed(name string) bool {
	if allowedOptions[name] {
		return true
	}
	for _, p := range allowedPrefixes {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// checkFileValue — файл только из bin или lists набора, без путей; для подделок ещё hex и «!».
func checkFileValue(name, value string) error {
	if !fileOptions[name] && (hexValue.MatchString(value) || builtinVal.MatchString(value)) {
		return nil
	}
	for _, dir := range []string{"{bin}", "{lists}"} {
		if file, ok := strings.CutPrefix(value, dir); ok && fileName.MatchString(file) && !strings.Contains(file, "..") {
			return nil
		}
	}
	return fmt.Errorf("%s=%s: файл можно брать только из папок набора — %%BIN%%имя или %%LISTS%%имя", name, value)
}

// CustomFiles — файлы набора, на которые ссылается стратегия: {bin}x → bin/x, {lists}y → lists/y.
func (s Strategy) CustomFiles() []string {
	var files []string
	for _, a := range s.Args {
		_, value, _ := strings.Cut(a, "=")
		if file, ok := strings.CutPrefix(value, "{bin}"); ok {
			files = append(files, "bin/"+file)
		} else if file, ok := strings.CutPrefix(value, "{lists}"); ok {
			files = append(files, "lists/"+file)
		}
	}
	return files
}
