package strategy

import (
	"embed"
	"path"
	"sort"
	"strings"
)

// Конфиги самого FI. Это обычные general*.bat в формате Flowseal: они работают и в его папке,
// а FI показывает их в списке стратегий и проверяет при подборе рядом с набором.
//
//	FI STABLE — основа ALT13: YouTube и в правиле QUIC, подделка для обычных сайтов — max.ru.
//	FI LIGHT  — минимум перехвата для игр и слабых компьютеров: только списки сайтов.
//	FI HARD   — подделка плюс разбиение с наложением, когда обычные не проходят.
//	FI SOCIAL — как STABLE, но соцсети и RuTracker уже внутри, в список добавлять не нужно.
//	FI DISORDER — подделка и разбиение с кусками в обратном порядке.
//	FI SEQOVL — без поддельных пакетов: для сетей, где подделки ломают соединения.
//
//go:embed builtin/*.bat
var builtinFiles embed.FS

// Builtin возвращает конфиги FI. Файлы подделок и списки они берут из установленного набора:
// какие из конфигов можно запустить, решает служба по CustomFiles.
func Builtin() []Strategy {
	entries, err := builtinFiles.ReadDir("builtin")
	if err != nil {
		return nil
	}
	sort.Slice(entries, func(i, j int) bool { return naturalKey(entries[i].Name()) < naturalKey(entries[j].Name()) })
	list := make([]Strategy, 0, len(entries))
	for _, e := range entries {
		s, err := parseBuiltin(e.Name())
		if err == nil {
			list = append(list, s)
		}
	}
	return list
}

func parseBuiltin(file string) (Strategy, error) {
	data, err := builtinFiles.ReadFile(path.Join("builtin", file))
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
	}
	if err := ValidateCustom(args); err != nil {
		return Strategy{}, err
	}
	return Strategy{Name: strings.TrimSuffix(file, ".bat"), Source: "fi", Args: args}, nil
}
