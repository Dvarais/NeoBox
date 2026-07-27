# NeoBox

A VPN client for Windows built on the sing-box core, written in Go with a Wails frontend.

[English](#english) · [Русский](#русский)

---

## English

### What it is

NeoBox is a desktop client for proxy protocols — you paste a subscription link or a
single server URL, pick a location, and it routes your traffic through it. The core
doing the actual work is [sing-box](https://github.com/SagerNet/sing-box); NeoBox is
the interface around it, plus the Windows-specific parts sing-box does not handle on
its own: the TUN adapter, the system proxy, firewall rules, the tray icon.

The window is frameless with the native Windows Acrylic backdrop, so it looks like it
belongs on Windows 10/11 rather than like a browser in a box.

### What it does

**Protocols.** VLESS, VMess, Trojan, Shadowsocks, TUIC, Hysteria and Hysteria 2.
Servers come from a subscription URL, from your clipboard, or from a QR code — either
scanned with a webcam or loaded from an image file.

**Two ways to route traffic.** TUN mode captures everything on the machine through a
virtual adapter (this needs Administrator rights — Windows will ask). System proxy
mode is lighter: NeoBox listens on `127.0.0.1:20809` and points Windows at it, which
covers browsers and most apps that respect the system setting.

**Split tunneling.** In TUN mode you can list `.exe` names that should skip the VPN,
or invert it into a whitelist where only the listed apps go through the tunnel. Useful
for games and remote desktop.

**Routing rules.** There is a one-click "bypass Russian blocks" toggle that pulls the
`geoip-ru` and `geosite-ru` rule sets, a list of domains to always send direct, and a
custom rules table where you can match a domain, a domain suffix, a keyword or an IP
range and send it direct, through the proxy, or block it outright. Custom rules win
over the geo rules.

**Leak protection.** Kill Switch blocks internet access through Windows Firewall if the
tunnel drops. Separately you can turn on DNS leak protection, IPv6 blocking, and
FakeDNS. There is also a built-in DNS leak test that shows which resolver your queries
actually reach.

**Day-to-day things.** Ping all servers and sort by latency, star favourites, search,
auto-pick the fastest server, a connection history with per-session traffic and
duration, a tray menu you can connect from, auto-connect on launch, start minimised,
launch with Windows. If the connection dies, a watchdog reconnects with backoff instead
of leaving you offline. Subscriptions refresh themselves once a day. The interface is
in Russian and English.

### Installing

You need Windows — 10 or 11 if you want the Acrylic effect to actually render.

1. Grab `NeoBox_Setup_v1.7.5.1.exe` from the [Releases](https://github.com/Dvarais/NeoBox/releases)
   page, or `NeoBox.exe` if you would rather run it without installing.
2. Run it, then launch the app.
3. Add a subscription and pick a server.

If you enable TUN mode, the app will ask for Administrator rights. That is unavoidable:
creating a virtual network adapter to capture system-wide traffic requires them. The
`wintun.dll` driver ships with the app and loads by itself — you do not install it
separately.

### Security

Your subscriptions contain full proxy links: UUIDs, passwords, the lot. NeoBox keeps
them encrypted at rest with AES-256-GCM, under a key sealed by Windows DPAPI — so the
files are useless if someone copies them off the machine to a different user or PC. The
same applies to the last selected server and your favourites, which live in `state.json`.
`settings.json` stays plain, readable JSON on purpose: it holds toggles, DNS choice and
routing lists, nothing worth hiding, and you should be able to edit it in Notepad.

Updates are verified with an Ed25519 signature before anything is executed, and the
check is fail-closed: no valid signature means no in-app install, and the app just opens
the release page instead.

Kill Switch rules are tracked with a marker file, so a crashed session cannot leave your
machine firewalled off the internet with no way back — the rules are cleared on the next
start.

### Building from source

You will need:

- Go 1.24 or newer (see `go.mod`)
- Node.js 16 or newer with npm
- Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

For development, with hot reload:

```bash
wails dev -tags with_utls
```

The `with_utls` tag matters — without it sing-box cannot do TLS fingerprint
masquerading, and a lot of servers expect it.

For a release build with everything switched on:

```bash
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
```

Binaries land in `build/bin/`.

#### Using the Go toolchain directly

`go build ./...` and `go test ./...` work on a fresh clone. `frontend/dist/` ships a
tracked `.gitkeep` so the `//go:embed all:frontend/dist` pattern always matches
something — without it Go refuses to build before npm has ever run.

The catch: a binary built that way contains **no user interface**. Build the frontend
first if you intend to run it:

```bash
cd frontend && npm ci && npm run build
```

`wails build` and `wails dev` do this for you.

#### Project layout

| Directory | What's in it |
|---|---|
| `backend/core/` | sing-box lifecycle, config generation, proxy link parsing |
| `backend/service/` | Wails bindings — settings, subscriptions, updates, tray, watchdog |
| `backend/security/` | Encryption, Ed25519 signatures, firewall rules, autostart |
| `backend/storage/` | Encrypted file storage with atomic writes |
| `backend/i18n/` | Translations for everything rendered in Go (tray, notifications) |
| `frontend/` | Vanilla JS UI bundled with Vite |
| `build/` | Icons, Windows manifests, NSIS installer template |
| `cmd/` | Signing tools: `keygen`, `sign` |

---

## Русский

VPN-клиент для Windows на ядре sing-box, написанный на Go с интерфейсом на Wails.

### Что это

NeoBox — десктопный клиент для прокси-протоколов: вставляете ссылку на подписку или
адрес отдельного сервера, выбираете локацию, и трафик идёт через неё. Всю работу с
протоколами делает [sing-box](https://github.com/SagerNet/sing-box); NeoBox — это
интерфейс вокруг него плюс то, чем sing-box сам по себе не занимается: TUN-адаптер,
системный прокси, правила брандмауэра, иконка в трее.

Окно без стандартной рамки, с нативным эффектом Windows Acrylic — выглядит как
приложение для Windows 10/11, а не как браузер в коробке.

### Что умеет

**Протоколы.** VLESS, VMess, Trojan, Shadowsocks, TUIC, Hysteria и Hysteria 2. Серверы
добавляются из подписки по URL, из буфера обмена или через QR-код — со сканированием
камерой или из файла с картинкой.

**Два способа пустить трафик.** TUN-режим перехватывает всё на машине через виртуальный
адаптер (нужны права администратора, Windows их запросит). Системный прокси — вариант
полегче: NeoBox слушает `127.0.0.1:20809` и прописывает себя в настройки Windows, чего
хватает для браузеров и большинства приложений, которые эти настройки уважают.

**Раздельное туннелирование.** В TUN-режиме можно перечислить имена `.exe`, которые
пойдут мимо VPN, либо перевернуть список в белый — тогда через туннель пойдут только
указанные программы. Удобно для игр и удалённого рабочего стола.

**Правила маршрутизации.** Есть переключатель обхода российских блокировок в один клик
(подтягивает наборы правил `geoip-ru` и `geosite-ru`), список доменов, которые всегда
идут напрямую, и таблица своих правил: домен, суффикс домена, ключевое слово или
диапазон IP — и действие «напрямую», «через VPN» или «заблокировать». Свои правила
приоритетнее гео-правил.

**Защита от утечек.** Kill Switch перекрывает интернет через брандмауэр Windows, если
туннель оборвался. Отдельно включаются защита от утечек DNS, блокировка IPv6 и FakeDNS.
Ещё есть встроенный тест на утечку DNS — он показывает, до какого резолвера реально
доходят ваши запросы.

**Повседневные мелочи.** Пинг всех серверов и сортировка по задержке, избранное, поиск,
автовыбор самого быстрого сервера, история подключений с трафиком и длительностью каждой
сессии, меню в трее, из которого можно подключиться, автоподключение при запуске, старт
свёрнутым, запуск вместе с Windows. Если соединение отвалится, watchdog переподключится
сам, с нарастающей паузой между попытками, а не оставит вас без сети. Подписки
обновляются раз в сутки. Интерфейс на русском и английском.

### Установка

Нужна Windows — 10 или 11, если хотите, чтобы эффект Acrylic действительно отрисовался.

1. Скачайте `NeoBox_Setup_v1.7.5.1.exe` со страницы
   [Releases](https://github.com/Dvarais/NeoBox/releases) или `NeoBox.exe`, если
   предпочитаете портативный вариант без установки.
2. Запустите установщик, затем само приложение.
3. Добавьте подписку и выберите сервер.

При включении TUN-режима приложение попросит права администратора. Без них никак:
создание виртуального сетевого адаптера для перехвата системного трафика их требует.
Драйвер `wintun.dll` идёт в комплекте и загружается сам — отдельно ставить не нужно.

### Безопасность

В подписках лежат полные прокси-ссылки: UUID, пароли, всё сразу. NeoBox хранит их на
диске зашифрованными (AES-256-GCM) под ключом, запечатанным через Windows DPAPI — то
есть файлы бесполезны, если их скопировать на другую машину или к другому пользователю.
То же касается последнего выбранного сервера и избранного, они лежат в `state.json`.
`settings.json` намеренно остаётся обычным читаемым JSON: там переключатели, выбор DNS
и списки маршрутизации — ничего, что стоило бы прятать, и это должно нормально
правиться в блокноте.

Обновления проверяются подписью Ed25519 до того, как что-либо будет запущено, причём
проверка fail-closed: нет валидной подписи — нет установки внутри приложения, вместо
неё просто откроется страница релиза.

Правила Kill Switch помечаются файлом-маркером, поэтому упавшая сессия не оставит машину
отрезанной от интернета без возможности это откатить — правила снимутся при следующем
запуске.

### Сборка из исходников

Понадобится:

- Go 1.24 или новее (см. `go.mod`)
- Node.js 16 или новее и npm
- Wails CLI: `go install github.com/wailsapp/wails/v2/cmd/wails@latest`

Режим разработки с горячей перезагрузкой:

```bash
wails dev -tags with_utls
```

Тег `with_utls` важен: без него sing-box не умеет маскировать отпечаток TLS, а этого
ждут многие серверы.

Релизная сборка со всем включённым:

```bash
wails build -tags "with_utls,with_clash_api,with_quic,with_wireguard,with_gvisor"
```

Результат складывается в `build/bin/`.

#### Работа напрямую через Go

`go build ./...` и `go test ./...` работают на свежем клоне. В `frontend/dist/` лежит
отслеживаемый `.gitkeep`, чтобы шаблону `//go:embed all:frontend/dist` всегда было что
найти — без него Go прерывает сборку ещё до того, как запускался npm.

Нюанс: собранный так бинарник **не содержит интерфейса**. Если собираетесь его
запускать, сначала соберите фронтенд:

```bash
cd frontend && npm ci && npm run build
```

`wails build` и `wails dev` делают это за вас.

#### Структура проекта

| Каталог | Что внутри |
|---|---|
| `backend/core/` | Жизненный цикл sing-box, генерация конфига, разбор прокси-ссылок |
| `backend/service/` | Биндинги Wails — настройки, подписки, обновления, трей, watchdog |
| `backend/security/` | Шифрование, подписи Ed25519, правила брандмауэра, автозапуск |
| `backend/storage/` | Шифрованное хранилище файлов с атомарной записью |
| `backend/i18n/` | Переводы для всего, что рисуется на стороне Go (трей, уведомления) |
| `frontend/` | Интерфейс на чистом JS, собирается через Vite |
| `build/` | Иконки, манифесты Windows, шаблон установщика NSIS |
| `cmd/` | Утилиты подписи: `keygen`, `sign` |
