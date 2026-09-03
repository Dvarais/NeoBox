// Import Wails bindings
import {
  BringToFront,
  CheckAdmin,
  CheckUpdates,
  CheckTunStatus,
  CloseConnection,
  DownloadAndInstallUpdate,
  ExportSettings,
  FetchSubscription,
  GetAppVersion,
  GetConnections,
  GetHistory,
  GetKillSwitchState,
  GetSettings,
  GetSubscriptions,
  ImportClipboard,
  ImportSettings,
  NotifyWindowHidden,
  NotifyWindowShown,
  OpenLogsFolder,
  PingServer,
  RebuildTrayProfiles,
  RequestAdmin,
  RestartXray,
  SaveHistory,
  SessionTraffic,
  SaveLogs,
  SaveSettings,
  SaveSubscriptions,
  SetGlobalHotkeys,
  StartXray,
  StopXray,
  UpdateSubscriptionNow,
  DNSResolverOwners,
  ValidateDNS,
} from './wailsjs/go/service/AppService';

import { EventsOn } from './wailsjs/runtime/runtime';

import type {
  AppSettings,
  ConnectionsSnapshot,
  HistoryEntry,
  KillSwitchState,
  NeoBoxApi,
  PingResult,
  StoredSettings,
  Subscription,
  UpdateInfo,
  XrayResult,
} from './modules/api';

// Обработчики, которые вызываются напрямую, а не через шину событий Wails: в
// варианте на Electron это были IPC-события, здесь достаточно вызова.
let pingCallback: ((result: PingResult) => void) | null = null;
let subResultCallback: ((links: string[]) => void) | null = null;

// Аннотация типом NeoBoxApi — это то, ради чего затевался перевод на TS: она
// сверяет реализацию с контрактом, который видят все остальные модули.
const api: NeoBoxApi = {
  // Commands
  bringToFront: () => BringToFront(),
  checkTunStatus: () => CheckTunStatus(),
  getKillSwitchState: () => GetKillSwitchState() as Promise<KillSwitchState>,
  // Отказ возвращается как есть, включая admin_required. Раньше мост сам звал
  // requestAdmin — то есть поднимал запрос UAC независимо от того, нажимал ли
  // человек хоть что-нибудь. Решать это отсюда нельзя: мост не знает, чем
  // вызвано подключение. См. handleConnectFailure в renderer.ts.
  startXray: async (link, useSystemProxy) => {
    const settings = await api.getSettings();
    return (await StartXray(link, JSON.stringify(settings), useSystemProxy)) as XrayResult;
  },
  restartXray: async (link, useSystemProxy) => {
    const settings = await api.getSettings();
    return (await RestartXray(link, JSON.stringify(settings), useSystemProxy)) as XrayResult;
  },
  stopXray: () => StopXray() as Promise<XrayResult>,
  pingServer: async (link) => {
    const latency = await PingServer(link);
    // Ping result was sent as an IPC event in Electron. We invoke the callback directly!
    if (pingCallback) {
      pingCallback({ link, latency });
    }
  },

  // Живые соединения. Обёртки тонкие намеренно: Go уже отдаёт готовые к
  // отрисовке строки — обрезанные, отсортированные и с посчитанным правилом, —
  // так что раскладывать тут нечего.
  getConnections: () => GetConnections() as Promise<ConnectionsSnapshot>,
  closeConnection: (id) => CloseConnection(id),

  // Version reported by the Go backend — the single source of truth for the
  // number shown in the title bar.
  getAppVersion: () => GetAppVersion(),

  // Settings & Subscriptions
  getSettings: async (): Promise<StoredSettings> => {
    try {
      const s = await GetSettings();
      return JSON.parse(s) as StoredSettings;
    } catch (e) {
      console.error('getSettings parse error:', e);
      return {};
    }
  },
  saveSettings: (settings: AppSettings) => SaveSettings(JSON.stringify(settings)),
  // Возвращают путь к файлу; пустая строка — пользователь закрыл диалог, и это
  // не ошибка.
  exportSettings: () => ExportSettings(),
  importSettings: () => ImportSettings(),
  validateDns: (value) => ValidateDNS(value),
  dnsResolverOwners: () => DNSResolverOwners(),
  getSubscriptions: async (): Promise<Subscription[]> => {
    try {
      const s = await GetSubscriptions();
      return JSON.parse(s) as Subscription[];
    } catch (e) {
      console.error('getSubscriptions parse error:', e);
      return [];
    }
  },
  saveSubscriptions: async (subs) => {
    return SaveSubscriptions(JSON.stringify(subs));
  },
  getHistory: async (): Promise<HistoryEntry[]> => {
    try {
      const raw = await GetHistory();
      const parsed = JSON.parse(raw);
      // Бэкенд отдаёт "[]" на всё, что не разобралось, но проверка стоит
      // копейки, а вкладка «История» перебирает результат без оглядки.
      return Array.isArray(parsed) ? (parsed as HistoryEntry[]) : [];
    } catch (e) {
      console.error('getHistory parse error:', e);
      return [];
    }
  },
  saveHistory: async (history) => {
    return SaveHistory(JSON.stringify(history));
  },
  sessionTraffic: async () => {
    const raw = await SessionTraffic();
    return { up: raw.up || 0, down: raw.down || 0 };
  },
  fetchSubscription: (url) => FetchSubscription(url),
  updateSubscriptionNow: (id) => UpdateSubscriptionNow(id),
  importFromClipboard: async () => {
    try {
      const text = await navigator.clipboard.readText();
      const links = await ImportClipboard(text);
      if (subResultCallback) {
        subResultCallback(links);
      }
    } catch (e) {
      console.error('Clipboard access failed:', e);
    }
  },

  // Base64 helper
  decodeBase64: (str) => {
    try {
      const b64 = str.replace(/\s/g, '').replace(/-/g, '+').replace(/_/g, '/');
      return atob(b64);
    } catch (e) {
      return '';
    }
  },

  // Event bindings (mocked or bound to Wails runtime EventsOn)
  // The backend batches log lines (see backend/service/logstream.go): the
  // callback receives an array, not a single line.
  onLog: (callback) => EventsOn('xray-log-batch', callback),
  onStarted: (callback) => EventsOn('xray-started', callback),
  onStopped: (callback) => EventsOn('xray-stopped', callback),
  onSubscriptionResult: (callback) => {
    subResultCallback = callback;
  },
  onPingResult: (callback) => {
    pingCallback = callback;
  },
  onTrayToggleConnection: (callback) => EventsOn('tray-toggle-connection', callback),
  onSubscriptionsUpdated: (callback) => EventsOn('subscriptions-updated', callback),
  onTrayServerSelected: (callback) => EventsOn('tray-server-selected', callback),
  onTrayStartReconnect: (callback) => EventsOn('tray-start-reconnect', callback),
  onTrayRestart: (callback) => EventsOn('tray-restart', callback),

  // Auto update
  checkUpdates: () => CheckUpdates() as Promise<UpdateInfo>,
  downloadAndInstallUpdate: (downloadURL, signatureHex) =>
    DownloadAndInstallUpdate(downloadURL, signatureHex),
  openUpdateLink: (url) => {
    window.open(url, '_blank');
  },
  onUpdateProgress: (callback) => EventsOn('update-progress', callback),
  onUpdateComplete: (callback) => EventsOn('update-complete', callback),
  onUpdateError: (callback) => EventsOn('update-error', callback),

  // Logs
  saveLogs: (content) => SaveLogs(content),
  openLogsFolder: () => OpenLogsFolder(),

  // Watchdog events
  onWatchdogReconnecting: (cb) => EventsOn('watchdog-reconnecting', cb),
  onWatchdogReconnected: (cb) => EventsOn('watchdog-reconnected', cb),
  onWatchdogFailed: (cb) => EventsOn('watchdog-failed', cb),
  // Emitted while the VPN server is unreachable: the watchdog deliberately holds
  // off restarting the core until connectivity comes back.
  onWatchdogWaiting: (cb) => EventsOn('watchdog-waiting', cb),
  onWatchdogSwitched: (cb) => EventsOn('watchdog-switched', cb),
  onWatchdogServerDead: (cb) => EventsOn('watchdog-server-dead', cb),

  // Системные горячие клавиши
  setGlobalHotkeys: (enable, toggleCombo, showCombo) => SetGlobalHotkeys(enable, toggleCombo, showCombo),
  onHotkeyToggleConnection: (cb) => EventsOn('hotkey-toggle-connection', cb),
  onHotkeyShowWindow: (cb) => EventsOn('hotkey-show-window', cb),

  // Быстрые переключатели в трее. Меню только сообщает, что по галке щёлкнули,
  // — ставит её и применяет фронтенд, как и профиль: применение TUN или Kill
  // Switch это пересборка конфигурации и переподключение.
  onTrayToggleSetting: (cb) => EventsOn('tray-toggle-setting', cb),

  // Профили
  rebuildTrayProfiles: () => RebuildTrayProfiles(),
  onTrayProfileSelected: (cb) => EventsOn('tray-profile-selected', cb),
  onTraffic: (cb) => EventsOn('traffic-stats', cb),

  // Admin rights
  checkAdmin: () => CheckAdmin(),
  requestAdmin: () => RequestAdmin(),

  // Window controls
  minimize: () => {
    if (window.runtime && window.runtime.WindowMinimise) {
      window.runtime.WindowMinimise();
      // Сообщаем ровно как close(). Без этого windowVisible на стороне Go
      // оставался true после сворачивания, и первое нажатие на пункт трея
      // «Скрыть/Показать» прятало уже свёрнутое окно вместо того, чтобы его
      // вернуть, — разворачивать приходилось со второго раза.
      void NotifyWindowHidden();
    }
  },
  close: () => {
    if (window.runtime && window.runtime.WindowHide) {
      window.runtime.WindowHide();
      void NotifyWindowHidden();
    }
  },
  notifyWindowShown: () => NotifyWindowShown(),
  // Emitted by the backend whenever the window goes to the tray, including the
  // paths the frontend never sees (tray menu toggle, close-to-tray).
  onWindowHidden: (callback) => {
    EventsOn('window-hidden', callback);
  },
  // Только собственные события бэкенда — и ничего больше.
  //
  // Здесь стояли ещё две подписки — на 'wails:window-unminimise' и
  // 'wails:window-restore'. Таких событий у Wails нет: во всей v2.12.0
  // объявлено ровно одно имя с этим префиксом, 'wails:file-drop'. Они не
  // срабатывали никогда, и именно из-за них возврат окна выглядел покрытым.
  //
  // Здесь же висели запасные слушатели DOM — focus и visibilitychange, — и
  // именно они ломали счётчик трафика. Такой же слушатель focus есть в
  // renderer.ts, и только он сообщает бэкенду, что окно вернулось. Этот
  // регистрировался раньше, срабатывал первым и поднимал uiActive; слушатель
  // renderer после этого выходил по собственному guard'у `if (uiActive)
  // return` и до NotifyWindowShown не доходил никогда. Go продолжал считать
  // окно скрытым и переставал слать 'traffic-stats' до конца сессии — цифры
  // застывали, хотя интерфейс выглядел живым.
  //
  // Мост — транспорт. Разворачивание мимо бэкенда (панель задач, Alt+Tab,
  // Win+D) ловит renderer, там же и одно место, где сходятся оба пути.
  //
  // 'wails:window-focus' отсюда тоже убран, и по той же причине: во всей
  // v2.12.0 бэкенд шлёт ровно одно имя с этим префиксом — 'wails:file-drop'.
  onWindowRestored: (callback) => {
    EventsOn('window-restored', callback);
  },
};

// Expose them as window.api to maintain total compatibility with original renderer.js!
window.api = api;

// Зеркала счётчиков трафика на window больше нет. Мост подписывался на
// traffic-stats только ради него, а история читала оттуда — и получала не
// итог сессии, а последнее, что успело прийти до сворачивания окна: пока окно
// в трее, Go событий не шлёт вовсе. Теперь итог спрашивают у Go в момент
// записи (api.sessionTraffic), а при смене сервера сторожем он приезжает
// вместе с событием watchdog-switched, потому что перезапуск ядра обнуляет
// счётчики сразу после него.
//
// Отрисовка спидометра и его сокрытие живут в renderer вместе с остальным
// интерфейсом: мост — транспорт, а не место для работы с DOM.
