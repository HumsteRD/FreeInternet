# Разработка FI

## Как устроен репозиторий

| Папка | Что внутри |
|---|---|
| `cmd/fi-service` | служба Windows: держит обход (winws), прокси Telegram, проверяет сервисы |
| `cmd/fi` | окно и значок в трее (Wails v3); интерфейс — `cmd/fi/ui` (HTML, CSS, JS без сборки) |
| `cmd/fi-setup` | установщик и деинсталлятор (`-tags uninstaller`) |
| `cmd/fi-probe` | стенд: сравнивает стратегии на настоящих сайтах |
| `cmd/fi-res` | значки и ресурсы Windows для сборки |
| `cmd/fi-sign` | ключи и подпись обновлений |
| `cmd/fi-ctl` | команда запущенной службе из консоли, для отладки: `fi-ctl status`, `fi-ctl set_strategy {"name":"general (ALT13)"}` |
| `internal/daemon` | ядро службы: настройки, проверки, подбор, сайты, Telegram |
| `internal/tgproxy` | MTProto-прокси: WebSocket веб-версии, Cloudflare, пул соединений |
| `internal/probe`, `internal/selector`, `internal/autoselect` | проверки сайтов и выбор стратегии |
| `internal/diag` | диагностика компьютера для «Не работает?» |
| `internal/...` | остальное: движок, списки, набор Flowseal, IPC, журнал, значки |
| `docs` | план, воркер Cloudflare, дизайн (`docs/design`) |
| `scripts/build.ps1` | сборка всех программ и установщика |

Служба работает от имени системы и хранит данные в `%ProgramData%\FI`: `config.json`
(настройки и список сайтов), `fi-service.log` (журнал, не больше 5 МБ плюс `.old`), набор
стратегий и списки. Окно пишет свой журнал в `%LOCALAPPDATA%\FI\fi.log` и говорит со службой
через `\\.\pipe\fi`.

## Сборка

Нужен Go (версия — в `go.mod`).

```powershell
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Version 0.2.0
```

Всё окажется в `build\`, установщик — `build\fi-setup.exe`. Тесты: `go test ./...`.
Проверки через настоящие серверы (нужен интернет) помечены тегом `live`:

```
go test -tags live -run Live -v ./internal/tgproxy
```

Для отладки службу можно поставить прямо из папки сборки — из терминала администратора,
другой обход (zapret, GoodbyeDPI) должен быть остановлен:

```
build\fi-service.exe install
build\fi.exe
```

Такую папку может изменить любой пользователь компьютера, поэтому это только для
разработки. Удалить: `build\fi-service.exe uninstall`.

## Выпуск версии

```
git tag v0.2.0
git push origin v0.2.0
```

GitHub Actions (`.github/workflows/release.yml`) прогонит тесты, соберёт и подпишет установщик
и выложит в [Releases](https://github.com/HumsteRD/FreeInternet/releases) `fi-setup.exe`,
`update.json` и `update.json.sig`. Ссылка `releases/latest/download/fi-setup.exe` в README
всегда ведёт на последнюю версию.

### Автообновление

Выпуски из GitHub Actions обновляются сами: манифест — `releases/latest/download/update.json`,
открытый ключ — `FI_UPDATE_PUBLIC_KEY` в `release.yml`, секретный — в секрете репозитория
`FI_UPDATE_KEY` (Settings → Secrets and variables → Actions). Без секрета выпуск не соберётся.
Секретный ключ храните ещё и в надёжном месте: если его потерять, установленные FI перестанут
принимать обновления, и их придётся переустановить вручную.

Сборка без адреса манифеста и ключа (например, локальная) сама не обновляется, но раз в сутки
смотрит последний выпуск на GitHub и предлагает скачать его. Подписать локальную сборку:

```powershell
# один раз: ключи (секретный хранить отдельно, в репозиторий не класть)
go run ./cmd/fi-sign keygen keys\update.key

# сборка версии: вшить адрес и ключ, подписать манифест
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Version 0.2.0 `
  -UpdateURL https://…/update.json -PublicKey <открытый ключ> `
  -SignKey keys\update.key -InstallerURL https://…/fi-setup-0.2.0.exe
```

Опубликовать нужно `build\update.json`, `build\update.json.sig` и установщик по адресу
`-InstallerURL`. Служба раз в сутки проверяет подпись манифеста, скачивает установщик,
сверяет его хеш и ставит новую версию без окон; ошибки пишутся в `%ProgramData%\FI\update.log`.

## Стенд стратегий

```
go build -o build/fi-probe.exe ./cmd/fi-probe
build\fi-probe.exe -baseline
```

`-baseline` проверяет сеть как есть, права администратора не нужны. Сравнить стратегии
zapret-discord-youtube — из терминала администратора, файлы в папке Flowseal не изменяются:

```
build\fi-probe.exe -flowseal "E:\zapret-discord-youtube-1.10.0" -hosting
```

| Флаг | Назначение |
|---|---|
| `-list` | показать стратегии из папки и выйти |
| `-only "ALT2,FAKE"` | проверить только стратегии с этими подстроками в имени |
| `-hosting` | добавить ~36 сайтов на зарубежных хостингах (проверка обрыва 16–20 КБ) |
| `-ipset any\|loaded\|none` | режим ipset (по умолчанию `any` с `-hosting`, иначе `none`) |
| `-game off\|tcp\|udp\|all` | игровой фильтр |
| `-timeout 5s`, `-parallel 8` | таймауты и параллельность проверок |
| `-json путь` | куда сохранить отчёт (по умолчанию `reports/`) |

## Прокси Telegram

Служба держит MTProto-прокси на `127.0.0.1:1453`. Пути в Telegram по порядку: свой
Cloudflare Worker, затем WebSocket веб-версии (`kwsN.web.telegram.org`) и общие домены
Cloudflare (первым идёт тот, что сработал недавно), в конце прямое TCP-соединение.

Ответ сервера с кодом -444 («неверный дата-центр») прокси клиенту не передаёт: получив его,
Telegram Desktop и форки считают прокси неисправным и переключаются на системный. Вместо
этого соединение закрывается, клиент переподключается, а случай пишется в журнал.

Секрет прокси выводится из идентификатора Windows (MachineGuid), поэтому после
переустановки FI прокси, сохранённый в Telegram, продолжает работать.

## Воркер Cloudflare

Код воркера — `internal/tgproxy/cf-worker.js`; `docs/cf-worker.js` — его копия для ручной
вставки (тест следит, чтобы они совпадали). Версия записана в самом коде и в
`tgproxy.WorkerVersionCurrent`: меняя код, поднимайте обе. `go test ./internal/tgproxy`
прогоняет воркер на поддельных сокетах (нужен Node.js).

- **Настройка** (`internal/daemon/cloudflare.go`, `internal/cfdeploy`): служба сама проверяет
  токен, загружает воркер, ждёт ответа и подключает его. Токен, если человек согласился, лежит
  в `%ProgramData%\FI\secrets\cloudflare-token` — папку читают только система и администраторы.
  С ним служба ставит новый код воркера сама, не чаще раза в шесть часов.
- **Лимит** (`internal/tgproxy/usage.go`): служба считает запросы к воркеру за сутки по Гринвичу
  (`worker-usage.json` переживает перезапуск), после 80% отправляет Telegram запасным путём, а
  получив от Cloudflare отказ по лимиту (429 или код 1027), не трогает воркер до полуночи.
  Обрыв соединения без кадра закрытия посреди загрузки считается обрывом по процессорному времени.
- **Программы** (`internal/tgproxy/apps.go`, `cmd/fi/programs.go`): служба держит HTTP-прокси на
  `127.0.0.1:1455` (порт прокси Telegram + 2), который ведёт любое соединение через воркер.
  Программу запускает окно, а не служба: она должна открыться в сеансе человека. Программам на
  Chromium и Electron передаётся `--proxy-server`, остальным — `HTTPS_PROXY` и `HTTP_PROXY`.
- **Сайты за Cloudflare** (`internal/tgproxy/cfnet.go`): воркер открывает TCP сам, а к адресам
  самого Cloudflare платформа ему подключаться не даёт. Служба узнаёт такие сайты по диапазонам
  адресов Cloudflare и по отказу воркера: добавить такой сайт «через воркер» она не даёт (совет —
  VPN), а соединения программ к ним ведёт напрямую. Проверка вживую:
  `go test -tags live -run 'LiveWorkerReach|LiveAppProxy' -v ./internal/tgproxy`.
- **Системный прокси** (`internal/winsvc/syspac.go`): сайты через воркер открываются в браузерах
  по файлу автонастройки. VPN в режиме TUN стирает эту настройку Windows; `PACWatch` отличает
  это от решения человека и ставит её обратно, а пока VPN включён, служба файл не предлагает.
