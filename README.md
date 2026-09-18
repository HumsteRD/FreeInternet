# FI

Лёгкое приложение в трее: возвращает нормальную работу Discord, YouTube, Telegram,
заблокированных сайтов и игр без ручной настройки. Само подбирает стратегию обхода
под провайдера, диагностирует, почему сервис не работает, и чинит, когда блокировки меняются.

> Статус: **этап 1** — служба и окно в трее, первая рабочая версия. План и архитектура: [docs/PLAN.md](docs/PLAN.md).

## Установка

```
powershell -ExecutionPolicy Bypass -File scripts\build.ps1
```

Скрипт соберёт программы и `build\fi-setup.exe`. Установщик кладёт FI в
`C:\Program Files\FI`, ставит службу, ярлык в «Пуске» и автозапуск окна; найдя
службу zapret или GoodbyeDPI, предложит остановить её (при удалении — вернуть).
Удаление — через «Установленные приложения» или `uninstall.exe` в папке программы.

## Telegram через Cloudflare

У многих провайдеров адреса Telegram закрыты по IP: тогда прокси FI не может дойти
до дата-центров ни напрямую, ни через веб-серверы Telegram, а обход DPI против блокировки
по адресу не помогает. Тогда прокси идёт через Cloudflare, двумя способами.

**Общие домены (включены по умолчанию).** Проект [tg-ws-proxy](https://github.com/Flowseal/tg-ws-proxy)
публикует [список доменов](https://github.com/Flowseal/tg-ws-proxy/blob/main/.github/cfproxy-domains.txt),
которые ведут к веб-серверам Telegram через Cloudflare. FI раз в час берёт свежий список,
запоминает его и добавляет эти домены в список обхода DPI. Отключается в
Настройки → Telegram → «Общие домены Cloudflare». Домены держат сторонние люди, их делят
все пользователи tg-ws-proxy, и они могут перестать работать без предупреждения.

**Свой воркер (надёжнее).** Бесплатный Cloudflare Worker принимает WebSocket и перекладывает
байты в дата-центр. Этот путь личный, его никто не делит с вами.

1. [dash.cloudflare.com](https://dash.cloudflare.com/) → Workers & Pages → Create → Deploy.
2. Edit code: вставить [docs/cf-worker.js](docs/cf-worker.js) целиком → Deploy.
3. Домен воркера (`имя-1234.логин.workers.dev`) вписать в FI: Настройки → Telegram →
   «Свой Cloudflare Worker».

Ни воркер, ни общие домены не расшифровывают переписку: внутри соединения идёт MTProto,
ключ знают только Telegram и ваш клиент. Порядок путей: свой воркер, затем прямой путь
и общие домены (первым идёт тот, что сработал недавно), в конце прямое TCP-соединение.

## Выпуск обновлений

Автообновление приложения включается сборкой с адресом манифеста и открытым ключом. Без них
FI работает как обычно, просто не обновляется сам.

```powershell
# один раз: ключи (секретный хранить отдельно, в репозиторий не класть)
go run ./cmd/fi-sign keygen keys\update.key

# сборка версии: вшить адрес и ключ, подписать манифест
powershell -ExecutionPolicy Bypass -File scripts\build.ps1 -Version 0.2.0 `
  -UpdateURL https://…/update.json -PublicKey <открытый ключ> `
  -SignKey keys\update.key -InstallerURL https://…/fi-setup-0.2.0.exe
```

Опубликовать нужно три файла: `build\update.json`, `build\update.json.sig` и установщик по
адресу `-InstallerURL`. Служба раз в сутки проверяет подпись манифеста, скачивает установщик,
сверяет его хеш и ставит новую версию без окон; ошибки пишутся в
`%ProgramData%\FI\update.log`.

## Приложение (этап 1)

Две программы: служба `fi-service` держит обход (нужны права администратора, WinDivert),
окно `fi` в трее показывает состояние и передаёт команды (права не нужны).

Сборка (Go 1.24+):

```
go build -o build/fi-service.exe ./cmd/fi-service
go build -tags production -ldflags "-H windowsgui" -o build/fi.exe ./cmd/fi
```

Первый запуск — из терминала администратора. Другой обход (служба zapret, GoodbyeDPI)
должен быть остановлен, иначе служба не запустит движок:

```
build\fi-service.exe install
build\fi.exe
```

Дальше всё в окне: «Скачать набор» загрузит последний релиз zapret-discord-youtube
с GitHub (архив сверяется с опубликованным sha256), «Начать подбор» проверит стратегии
и включит лучшую. Раз в сутки служба сама ставит новый набор, выбранная стратегия
сохраняется. Набор из своей папки: `build\fi-service.exe import-base <папка>`
(служба должна быть остановлена).
Удаление службы: `build\fi-service.exe uninstall`. Журнал: `%ProgramData%\FI\fi-service.log`.

Так служба ставится из папки сборки — это только для разработки: такую папку может
изменить любой пользователь компьютера. Для обычной установки есть `fi-setup.exe`,
он кладёт программы в Program Files.

## Telegram

Служба держит MTProto-прокси на `127.0.0.1:1453`. Трафик Telegram Desktop уходит к
Telegram через WebSocket веб-версии (`kwsN.web.telegram.org`), который не режут вместе с
обычным протоколом; содержимое остаётся зашифрованным MTProto — прокси его не видит.
Если адреса Telegram закрыты, см. [Telegram через Cloudflare](#telegram-через-cloudflare).

Подключение: Настройки → Telegram → «Подключить в Telegram» / «Подключить в AyuGram».
FI находит Telegram Desktop и форки (AyuGram, Kotatogram, 64Gram, materialgram, iMe) среди
запущенных программ, в обычных папках установки и на рабочем столе, в том числе в OneDrive.
Если программы нет в списке, «Другая программа…» даёт выбрать её `.exe`. Клиент откроет
ссылку прокси и предложит его включить.

Секрет прокси выводится из идентификатора Windows (MachineGuid), поэтому после
переустановки FI прокси, сохранённый в Telegram, продолжает работать.

Проверка через настоящие серверы Telegram:

```
go test -tags live -run Live -v ./internal/tgproxy
```

## Стенд тестирования (этап 0)

Сборка (Go 1.24+):

```
go build -o build/fi-probe.exe ./cmd/fi-probe
```

Проверить сеть как есть (права администратора не нужны):

```
build\fi-probe.exe -baseline
```

Сравнить стратегии zapret-discord-youtube — из терминала администратора, другой обход
(служба zapret, GoodbyeDPI) должен быть остановлен. Файлы в папке Flowseal не изменяются:

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

## Благодарности

- [bol-van/zapret](https://github.com/bol-van/zapret) — движок обхода DPI (winws), MIT
- [Flowseal/zapret-discord-youtube](https://github.com/Flowseal/zapret-discord-youtube) — стратегии и списки, MIT
- [Flowseal/tg-ws-proxy](https://github.com/Flowseal/tg-ws-proxy) — идея Telegram WS-прокси и список общих доменов Cloudflare, MIT
- [hyperion-cs/dpi-checkers](https://github.com/hyperion-cs/dpi-checkers) — методика проверки «16–20 КБ», Apache-2.0
