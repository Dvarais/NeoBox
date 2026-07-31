package i18n

// Message IDs. Grouped by the surface they appear on so a missing translation
// is easy to trace back to the screen it belongs to.
const (
	// System tray menu.
	TrayStatusDisconnected = "tray.status.disconnected"
	TrayStatusConnected    = "tray.status.connected"
	TrayStatusTooltip      = "tray.status.tooltip"
	TrayShowWindow         = "tray.show"
	TrayHideWindow         = "tray.hide"
	TrayToggleTooltip      = "tray.toggle.tooltip"
	TraySelectServer       = "tray.servers"
	TraySelectTooltip      = "tray.servers.tooltip"
	TrayRestart            = "tray.restart"
	TrayRestartTooltip     = "tray.restart.tooltip"
	TrayDisconnect         = "tray.disconnect"
	TrayDisconnectTooltip  = "tray.disconnect.tooltip"
	TrayQuit               = "tray.quit"
	TrayQuitTooltip        = "tray.quit.tooltip"

	// Windows toast notifications.
	ToastConnectedTitle    = "toast.connected.title"
	ToastConnectedBody     = "toast.connected.body"
	ToastDisconnectedTitle = "toast.disconnected.title"
	ToastDisconnectedBody  = "toast.disconnected.body"

	// Errors surfaced to the user by the connection flow.
	ErrParseSettings     = "error.settings.parse"
	ErrParseLink         = "error.link.parse"
	ErrGenerateConfig    = "error.config.generate"
	ErrSerializeConfig   = "error.config.serialize"
	ErrStartCore         = "error.core.start"
	ErrPortBusy          = "error.port.busy"
	ErrKillSwitchFailed  = "error.killswitch.failed"
	ErrSignatureRejected = "error.signature.rejected"
	ErrSecretGeneration  = "error.secret.generate"

	// Errors produced while parsing links and subscriptions.
	ErrLinkTooLong          = "error.link.too_long"
	ErrSubURLTooLong        = "error.suburl.too_long"
	ErrSubURLNotSecure      = "error.suburl.insecure"
	ErrSubBlockedByHost     = "error.sub.blocked"
	ErrSubUnreachable       = "error.sub.unreachable"
	ErrSubNoNodes           = "error.sub.no_nodes"
	ErrWireGuardNoAddress   = "error.wireguard.no_address"
	ErrTransportUnsupported = "error.transport.unsupported"

	// Diagnostics screen.
	DiagWintunName         = "diag.wintun.name"
	DiagWintunFound        = "diag.wintun.found"
	DiagWintunMissing      = "diag.wintun.missing"
	DiagAdminName          = "diag.admin.name"
	DiagAdminYes           = "diag.admin.yes"
	DiagAdminNo            = "diag.admin.no"
	DiagProxyPortName      = "diag.proxyport.name"
	DiagClashPortName      = "diag.clashport.name"
	DiagPortInUseByVPN     = "diag.port.inuse_vpn"
	DiagPortInUseByVPNAPI  = "diag.port.inuse_vpn_api"
	DiagPortBusy           = "diag.port.busy"
	DiagClashPortBusy      = "diag.clashport.busy"
	DiagPortFree           = "diag.port.free"
	DiagInternetName       = "diag.internet.name"
	DiagInternetOK         = "diag.internet.ok"
	DiagInternetFail       = "diag.internet.fail"
	DiagDNSName            = "diag.dns.name"
	DiagDNSOK              = "diag.dns.ok"
	DiagDNSFail            = "diag.dns.fail"
	DiagKillSwitchName     = "diag.killswitch.name"
	DiagKillSwitchLeftover = "diag.killswitch.leftover"
	DiagCoreName           = "diag.core.name"
	DiagCoreRunning        = "diag.core.running"
	DiagCoreStopped        = "diag.core.stopped"
)

// messages holds every translation. Keep the two tables in step: a key present
// in one language and absent from the other silently falls back, which reads as
// a bug report about "half-translated" text.
var messages = map[Lang]map[string]string{
	RU: {
		TrayStatusDisconnected: "Статус: Отключено",
		TrayStatusConnected:    "Статус: Подключено (%s)",
		TrayStatusTooltip:      "Текущий статус подключения",
		TrayShowWindow:         "Открыть интерфейс",
		TrayHideWindow:         "Скрыть интерфейс",
		TrayToggleTooltip:      "Показать/Скрыть окно приложения",
		TraySelectServer:       "Выбрать сервер",
		TraySelectTooltip:      "Выбрать сервер из подписок",
		TrayRestart:            "Перезапустить VPN",
		TrayRestartTooltip:     "Перезапустить текущее VPN соединение",
		TrayDisconnect:         "Отключиться",
		TrayDisconnectTooltip:  "Разорвать VPN соединение",
		TrayQuit:               "Выход",
		TrayQuitTooltip:        "Закрыть NeoBox",

		ToastConnectedTitle:    "✅ NeoBox VPN",
		ToastConnectedBody:     "Подключено к серверу: %s",
		ToastDisconnectedTitle: "❌ NeoBox VPN",
		ToastDisconnectedBody:  "Соединение разорвано",

		ErrParseSettings:     "Не удалось прочитать настройки: %v",
		ErrParseLink:         "Не удалось разобрать ссылку прокси: %v",
		ErrGenerateConfig:    "Не удалось сформировать конфигурацию: %v",
		ErrSerializeConfig:   "Не удалось сериализовать конфигурацию: %v",
		ErrStartCore:         "Не удалось запустить sing-box: %v",
		ErrPortBusy:          "Порт %d уже занят другим процессом. Закройте конфликтующее приложение и попробуйте снова.",
		ErrKillSwitchFailed:  "Не удалось включить Kill Switch, подключение отменено: %v",
		ErrSignatureRejected: "Проверка подписи не удалась: %v",
		ErrSecretGeneration:  "Не удалось сгенерировать секрет для Clash API, подключение отменено: %v",

		ErrLinkTooLong:          "Ссылка прокси слишком длинная (максимум %d символов)",
		ErrSubURLTooLong:        "URL подписки слишком длинный (максимум %d символов)",
		ErrSubURLNotSecure:      "Небезопасный URL подписки: используйте HTTPS вместо HTTP для защиты от перехвата",
		ErrSubBlockedByHost:     "Сервер подписки вернул веб-страницу вместо списка серверов. Обычно это страница входа, оплаты или проверки — откройте ссылку в браузере и проверьте, активна ли подписка.",
		ErrSubUnreachable:       "Не удалось загрузить подписку: %v",
		ErrSubNoNodes:           "Подписка загружена, но не содержит серверов в поддерживаемом формате",
		ErrWireGuardNoAddress:   "В ссылке WireGuard не указан адрес интерфейса (параметр address) — без него туннель не поднять",
		ErrTransportUnsupported: "Транспорт %s не поддерживается ядром sing-box. Такой сервер работает только в клиентах на базе Xray.",

		DiagWintunName:         "Wintun драйвер",
		DiagWintunFound:        "Найден (%s)",
		DiagWintunMissing:      "wintun.dll не найден рядом с исполняемым файлом. TUN режим будет недоступен.",
		DiagAdminName:          "Права администратора",
		DiagAdminYes:           "Запущен с правами администратора — TUN режим доступен",
		DiagAdminNo:            "Нет прав администратора. Системный прокси работает, TUN режим — нет.",
		DiagProxyPortName:      "Порт прокси (%d)",
		DiagClashPortName:      "Порт Clash API (%d)",
		DiagPortInUseByVPN:     "Порт занят текущим активным подключением VPN",
		DiagPortInUseByVPNAPI:  "Порт занят текущим активным подключением VPN (статистика работает)",
		DiagPortBusy:           "Порт %d занят другим процессом. VPN не запустится пока порт не освобождён.",
		DiagClashPortBusy:      "Порт %d занят. Статистика трафика в реальном времени может не работать.",
		DiagPortFree:           "Порт свободен",
		DiagInternetName:       "Интернет",
		DiagInternetOK:         "Интернет доступен (успешное подключение к %s)",
		DiagInternetFail:       "Нет доступа к интернету (проверенные хосты недоступны). Проверьте сетевое соединение.",
		DiagDNSName:            "DNS резолвер",
		DiagDNSOK:              "DNS порт доступен (успешное подключение к %s)",
		DiagDNSFail:            "Порты DNS недоступны. Возможны проблемы с подпиской и DNS-over-HTTPS.",
		DiagKillSwitchName:     "Kill Switch",
		DiagKillSwitchLeftover: "В брандмауэре остались правила NeoBox, хотя VPN не запущен — интернет заблокирован. Перезапустите NeoBox от имени администратора, чтобы удалить их.",
		DiagCoreName:           "VPN ядро (sing-box)",
		DiagCoreRunning:        "Запущено и работает",
		DiagCoreStopped:        "VPN не подключён",
	},
	EN: {
		TrayStatusDisconnected: "Status: Disconnected",
		TrayStatusConnected:    "Status: Connected (%s)",
		TrayStatusTooltip:      "Current connection status",
		TrayShowWindow:         "Show window",
		TrayHideWindow:         "Hide window",
		TrayToggleTooltip:      "Show or hide the application window",
		TraySelectServer:       "Select server",
		TraySelectTooltip:      "Pick a server from your subscriptions",
		TrayRestart:            "Restart VPN",
		TrayRestartTooltip:     "Restart the current VPN connection",
		TrayDisconnect:         "Disconnect",
		TrayDisconnectTooltip:  "Drop the VPN connection",
		TrayQuit:               "Quit",
		TrayQuitTooltip:        "Close NeoBox",

		ToastConnectedTitle:    "✅ NeoBox VPN",
		ToastConnectedBody:     "Connected to: %s",
		ToastDisconnectedTitle: "❌ NeoBox VPN",
		ToastDisconnectedBody:  "Connection closed",

		ErrParseSettings:     "Failed to read settings: %v",
		ErrParseLink:         "Failed to parse the proxy link: %v",
		ErrGenerateConfig:    "Failed to build the configuration: %v",
		ErrSerializeConfig:   "Failed to serialise the configuration: %v",
		ErrStartCore:         "Failed to start sing-box: %v",
		ErrPortBusy:          "Port %d is already in use by another process. Close the conflicting application and try again.",
		ErrKillSwitchFailed:  "Could not enable the Kill Switch, connection cancelled: %v",
		ErrSignatureRejected: "Signature verification failed: %v",
		ErrSecretGeneration:  "Could not generate the Clash API secret, connection cancelled: %v",

		ErrLinkTooLong:          "Proxy link is too long (maximum %d characters)",
		ErrSubURLTooLong:        "Subscription URL is too long (maximum %d characters)",
		ErrSubURLNotSecure:      "Insecure subscription URL: use HTTPS instead of HTTP to prevent interception",
		ErrSubBlockedByHost:     "The subscription host returned a web page instead of a server list. That is usually a sign-in, payment or verification page — open the link in a browser and check that the subscription is still active.",
		ErrSubUnreachable:       "Could not download the subscription: %v",
		ErrSubNoNodes:           "The subscription was downloaded but holds no servers in a supported format",
		ErrWireGuardNoAddress:   "The WireGuard link carries no interface address (the \"address\" parameter); the tunnel cannot come up without it",
		ErrTransportUnsupported: "The %s transport is not supported by the sing-box core. Such a server only works in Xray-based clients.",

		DiagWintunName:         "Wintun driver",
		DiagWintunFound:        "Found (%s)",
		DiagWintunMissing:      "wintun.dll was not found next to the executable. TUN mode will be unavailable.",
		DiagAdminName:          "Administrator rights",
		DiagAdminYes:           "Running as administrator — TUN mode is available",
		DiagAdminNo:            "Not running as administrator. The system proxy works, TUN mode does not.",
		DiagProxyPortName:      "Proxy port (%d)",
		DiagClashPortName:      "Clash API port (%d)",
		DiagPortInUseByVPN:     "In use by the active VPN connection",
		DiagPortInUseByVPNAPI:  "In use by the active VPN connection (statistics are working)",
		DiagPortBusy:           "Port %d is used by another process. The VPN will not start until it is freed.",
		DiagClashPortBusy:      "Port %d is in use. Live traffic statistics may not work.",
		DiagPortFree:           "Free",
		DiagInternetName:       "Internet",
		DiagInternetOK:         "Reachable (connected to %s)",
		DiagInternetFail:       "No internet access (none of the probed hosts responded). Check your network connection.",
		DiagDNSName:            "DNS resolver",
		DiagDNSOK:              "DNS port reachable (connected to %s)",
		DiagDNSFail:            "DNS ports are unreachable. Subscriptions and DNS-over-HTTPS may fail.",
		DiagKillSwitchName:     "Kill Switch",
		DiagKillSwitchLeftover: "NeoBox firewall rules are still installed even though the VPN is not running — internet is blocked. Restart NeoBox as administrator to remove them.",
		DiagCoreName:           "VPN core (sing-box)",
		DiagCoreRunning:        "Running",
		DiagCoreStopped:        "Not connected",
	},
}
