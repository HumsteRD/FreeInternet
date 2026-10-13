package tgproxy

import (
	_ "embed"
	"strings"
)

// workerSource — код Cloudflare Worker (копия docs/cf-worker.js: тест следит, чтобы они совпадали).
//
//go:embed cf-worker.js
var workerSource string

const workerKeyLine = `const KEY = "";`

// WorkerCode — код воркера с ключом FI: с ним воркер пускает к сайтам из списка «через воркер».
func WorkerCode(key string) string {
	return strings.Replace(workerSource, workerKeyLine, `const KEY = "`+key+`";`, 1)
}
