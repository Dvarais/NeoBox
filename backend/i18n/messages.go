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
	TrayProfiles           = "tray.profiles"
	TrayProfilesTooltip    = "tray.profiles.tooltip"
	TrayRestart            = "tray.restart"
	TrayRestartTooltip     = "tray.restart.tooltip"
	TrayDisconnect         = "tray.disconnect"
	TrayDisconnectTooltip  = "tray.disconnect.tooltip"
	TrayQuit               = "tray.quit"
	TrayQuitTooltip        = "tray.quit.tooltip"
	TrayKillSwitch         = "tray.killswitch"
	TrayKillSwitchTooltip  = "tray.killswitch.tooltip"
	TrayTunMode            = "tray.tun"
	TrayTunModeTooltip     = "tray.tun.tooltip"
	TraySystemProxy        = "tray.sysproxy"
	TraySystemProxyTooltip = "tray.sysproxy.tooltip"
	// TrayMoreServers closes an over-long server list. See trayServerLimit.
	TrayMoreServers = "tray.servers.more"
	// The hover text on the icon itself — the only part of the tray that is
	// readable without opening the menu, and the reason the traffic totals are
	// in it.
	TrayTipConnected    = "tray.tip.connected"
	TrayTipDisconnected = "tray.tip.disconnected"

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
	ErrHotkeyInvalid     = "error.hotkey.invalid"

	// Errors produced while parsing links and subscriptions.
	ErrLinkTooLong          = "error.link.too_long"
	ErrSubURLTooLong        = "error.suburl.too_long"
	ErrSubURLNotSecure      = "error.suburl.insecure"
	ErrSubBlockedByHost     = "error.sub.blocked"
	ErrSubUnreachable       = "error.sub.unreachable"
	ErrSubNoNodes           = "error.sub.no_nodes"
	ErrWireGuardNoAddress   = "error.wireguard.no_address"
	ErrTransportUnsupported = "error.transport.unsupported"

	// Import and export of the portable settings file. These reach the user as
	// the titles of the two system dialogs and as the reason an import was
	// refused, so they need the same parity as everything else on screen.
	TransferExportTitle    = "transfer.export.title"
	TransferImportTitle    = "transfer.import.title"
	TransferJSONFilter     = "transfer.filter.json"
	ErrWindowNotReady      = "error.window.not_ready"
	ErrTransferEncode      = "error.transfer.encode"
	ErrTransferSaveDialog  = "error.transfer.save_dialog"
	ErrTransferOpenDialog  = "error.transfer.open_dialog"
	ErrTransferWrite       = "error.transfer.write"
	ErrTransferRead        = "error.transfer.read"
	ErrTransferSave        = "error.transfer.save"
	ErrTransferForeign     = "error.transfer.foreign"
	ErrTransferMalformed   = "error.transfer.malformed"
	ErrTransferNewerSchema = "error.transfer.newer_schema"
	ErrTransferNoSettings  = "error.transfer.no_settings"
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
		TrayProfiles:           "Профили",
		TrayProfilesTooltip:    "Переключить сохранённый набор настроек",
		TrayRestart:            "Перезапустить VPN",
		TrayRestartTooltip:     "Перезапустить текущее VPN соединение",
		TrayDisconnect:         "Отключиться",
		TrayDisconnectTooltip:  "Разорвать VPN соединение",
		TrayQuit:               "Выход",
		TrayQuitTooltip:        "Закрыть NeoBox",
		TrayKillSwitch:         "Kill Switch",
		TrayKillSwitchTooltip:  "Блокировать интернет вне VPN",
		TrayTunMode:            "Режим TUN",
		TrayTunModeTooltip:     "Пускать через VPN весь трафик системы",
		TraySystemProxy:        "Системный прокси",
		TraySystemProxyTooltip: "Прописывать NeoBox в настройки прокси Windows",
		TrayMoreServers:        "…и ещё %d",
		TrayTipConnected:       "NeoBox — подключено\n%s\n↑ %s   ↓ %s",
		TrayTipDisconnected:    "NeoBox — отключено",

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
		ErrHotkeyInvalid:     "сочетание «%s» приложению непонятно",

		ErrLinkTooLong:          "Ссылка прокси слишком длинная (максимум %d символов)",
		ErrSubURLTooLong:        "URL подписки слишком длинный (максимум %d символов)",
		ErrSubURLNotSecure:      "Небезопасный URL подписки: используйте HTTPS вместо HTTP для защиты от перехвата",
		ErrSubBlockedByHost:     "Сервер подписки вернул веб-страницу вместо списка серверов. Обычно это страница входа, оплаты или проверки — откройте ссылку в браузере и проверьте, активна ли подписка.",
		ErrSubUnreachable:       "Не удалось загрузить подписку: %v",
		ErrSubNoNodes:           "Подписка загружена, но не содержит серверов в поддерживаемом формате",
		ErrWireGuardNoAddress:   "В ссылке WireGuard не указан адрес интерфейса (параметр address) — без него туннель не поднять",
		ErrTransportUnsupported: "Транспорт %s не поддерживается ядром sing-box. Такой сервер работает только в клиентах на базе Xray.",

		TransferExportTitle:    "Экспорт настроек NeoBox",
		TransferImportTitle:    "Импорт настроек NeoBox",
		TransferJSONFilter:     "JSON (*.json)",
		ErrWindowNotReady:      "Окно ещё не готово",
		ErrTransferEncode:      "Не удалось собрать файл настроек: %v",
		ErrTransferSaveDialog:  "Не удалось открыть диалог сохранения: %v",
		ErrTransferOpenDialog:  "Не удалось открыть диалог выбора файла: %v",
		ErrTransferWrite:       "Не удалось записать файл: %v",
		ErrTransferRead:        "Не удалось прочитать файл: %v",
		ErrTransferSave:        "Не удалось сохранить импортированные настройки",
		ErrTransferForeign:     "Это не файл настроек NeoBox",
		ErrTransferMalformed:   "Это не файл настроек NeoBox: %v",
		ErrTransferNewerSchema: "Файл сделан более новой версией NeoBox (формат %d, поддерживается %d)",
		ErrTransferNoSettings:  "В файле нет настроек",
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
		TrayProfiles:           "Profiles",
		TrayProfilesTooltip:    "Switch to a saved set of settings",
		TrayRestart:            "Restart VPN",
		TrayRestartTooltip:     "Restart the current VPN connection",
		TrayDisconnect:         "Disconnect",
		TrayDisconnectTooltip:  "Drop the VPN connection",
		TrayQuit:               "Quit",
		TrayQuitTooltip:        "Close NeoBox",
		TrayKillSwitch:         "Kill Switch",
		TrayKillSwitchTooltip:  "Block all traffic outside the VPN",
		TrayTunMode:            "TUN mode",
		TrayTunModeTooltip:     "Route the whole system through the VPN",
		TraySystemProxy:        "System proxy",
		TraySystemProxyTooltip: "Point the Windows proxy settings at NeoBox",
		TrayMoreServers:        "…and %d more",
		TrayTipConnected:       "NeoBox — connected\n%s\n↑ %s   ↓ %s",
		TrayTipDisconnected:    "NeoBox — disconnected",

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
		ErrHotkeyInvalid:     "the shortcut %q means nothing to the app",

		ErrLinkTooLong:          "Proxy link is too long (maximum %d characters)",
		ErrSubURLTooLong:        "Subscription URL is too long (maximum %d characters)",
		ErrSubURLNotSecure:      "Insecure subscription URL: use HTTPS instead of HTTP to prevent interception",
		ErrSubBlockedByHost:     "The subscription host returned a web page instead of a server list. That is usually a sign-in, payment or verification page — open the link in a browser and check that the subscription is still active.",
		ErrSubUnreachable:       "Could not download the subscription: %v",
		ErrSubNoNodes:           "The subscription was downloaded but holds no servers in a supported format",
		ErrWireGuardNoAddress:   "The WireGuard link carries no interface address (the \"address\" parameter); the tunnel cannot come up without it",
		ErrTransportUnsupported: "The %s transport is not supported by the sing-box core. Such a server only works in Xray-based clients.",

		TransferExportTitle:    "Export NeoBox settings",
		TransferImportTitle:    "Import NeoBox settings",
		TransferJSONFilter:     "JSON (*.json)",
		ErrWindowNotReady:      "The window is not ready yet",
		ErrTransferEncode:      "Could not build the settings file: %v",
		ErrTransferSaveDialog:  "Could not open the save dialog: %v",
		ErrTransferOpenDialog:  "Could not open the file picker: %v",
		ErrTransferWrite:       "Could not write the file: %v",
		ErrTransferRead:        "Could not read the file: %v",
		ErrTransferSave:        "Could not save the imported settings",
		ErrTransferForeign:     "This is not a NeoBox settings file",
		ErrTransferMalformed:   "This is not a NeoBox settings file: %v",
		ErrTransferNewerSchema: "The file was made by a newer version of NeoBox (format %d, supported %d)",
		ErrTransferNoSettings:  "The file carries no settings",
	},
}
