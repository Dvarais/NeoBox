// Контракт между фронтендом и Go-бэкендом.
//
// Здесь только типы: реализация живёт в wails-bridge.ts, а сюда смотрят и она,
// и все остальные модули. Разъезд между тем, что фронтенд пишет в SaveSettings,
// и тем, что бэкенд оттуда читает, — не гипотетическая проблема: поле
// systemProxy интерфейс показывал, tray.go читал, а сохранять его не сохранял
// никто, и подключение из трея молча уходило без системного прокси.

import type { Language } from './translations';
import type { ThemeSpec } from './theme';

/**
 * Подписка в том виде, в каком её хранит бэкенд.
 * Зеркалит структуру Subscription в backend/service/subscriptions.go.
 */
export interface Subscription {
  id: string;
  name: string;
  url: string;
  links: string[];
  /** true, пока идёт первая загрузка: во вкладке рисуются песочные часы. */
  loading?: boolean;

  // Состояние автообновления. Отсутствуют у подписок, заведённых прежними
  // сборками, — поэтому необязательные, а не со значением по умолчанию.

  /** Время последней успешной загрузки, unix-миллисекунды. */
  updatedAt?: number;
  /** Причина последней неудачи, уже переведённая бэкендом. */
  lastError?: string;
  /** Свой период обновления в часах. Отсутствие означает «как у всех», 24 часа. */
  intervalHours?: number;
}

/** Пользовательское правило маршрутизации. Зеркалит core.CustomRule. */
export interface CustomRule {
  action: 'direct' | 'proxy' | 'block';
  // 'process' сопоставляется с именем исполняемого файла и, в отличие от
  // остальных типов, работает только в TUN-режиме: вне его ядро не видит
  // владельца сокета. GenerateConfig такое правило просто не выпускает в
  // конфиг — см. backend/core/config.go.
  type: 'domain' | 'domain_suffix' | 'domain_keyword' | 'ip_cidr' | 'process';
  value: string;
}

/**
 * Полный объект настроек — ровно то, что собирает collectAndSaveSettings.
 *
 * Поля обязательные намеренно. SaveSettings заменяет сохранённые настройки
 * целиком, а не сливает с прежними (см. комментарий к SaveSettings в
 * backend/service/settings.go), поэтому частичное сохранение стирает у
 * пользователя избранное. Тип не даёт собрать такой объект по частям.
 */
export interface AppSettings {
  language: Language;
  dns: string;
  bypassRu: boolean;
  tunMode: boolean;
  systemProxy: boolean;
  autoConnect: boolean;
  autoUpdateSubs: boolean;
  rememberServer: boolean;
  openAtLogin: boolean;
  startMinimized: boolean;
  /**
   * Системные горячие клавиши. Выключены по умолчанию: RegisterHotKey забирает
   * сочетание у всей системы, и включать это без спроса нельзя.
   */
  globalHotkeys: boolean;
  /**
   * Сами сочетания, в виде «Ctrl+Shift+V». Пустая строка означает значение по
   * умолчанию; разбирает их Go (parseHotkey в backend/service/hotkey.go),
   * потому что только он и знает, что RegisterHotKey примет.
   */
  hotkeyToggle: string;
  hotkeyShow: string;
  killSwitch: boolean;
  dnsLeak: boolean;
  ipv6Leak: boolean;
  fakeDns: boolean;
  verboseLogging: boolean;
  lastSelectedServer: string | null;
  processMode: 'blacklist' | 'whitelist';
  processListBlacklist: string[];
  processListWhitelist: string[];
  favoriteLinks: string[];
  /**
   * Именованные наборы настроек. Лежат в зашифрованной половине: профиль может
   * нести ссылку на сервер. Тип объявлен ниже — Profile ссылается на
   * AppSettings, поэтому объявить его выше нельзя.
   */
  profiles: Profile[];
  customRules: CustomRule[];
  /** Вид вкладки «Соединения»: сгруппированный по умолчанию, плоский для отладки. */
  connectionsView: 'grouped' | 'flat';
  /**
   * Тема интерфейса: акцент и фон. Всё остальное выводится из них движком
   * (modules/theme.ts), поэтому храним только выбор человека, а не результат —
   * иначе правка движка не догнала бы уже сохранённые темы.
   */
  theme: ThemeSpec;
}

/**
 * Часть настроек, которую переключает профиль.
 *
 * Сюда вошло то, что различается между «дома» и «на работе»: куда идёт трафик,
 * какой DNS, какие программы мимо туннеля, какая защита включена. Не вошло то,
 * что относится к приложению, а не к сценарию, — язык, тема, автозапуск,
 * горячие клавиши, вид вкладки «Соединения». Профиль, который переключает язык
 * интерфейса, был бы неприятной неожиданностью.
 */
export type ProfileSettings = Pick<
  AppSettings,
  | 'dns'
  | 'bypassRu'
  | 'tunMode'
  | 'systemProxy'
  | 'processMode'
  | 'processListBlacklist'
  | 'processListWhitelist'
  | 'customRules'
  | 'killSwitch'
  | 'dnsLeak'
  | 'ipv6Leak'
  | 'fakeDns'
>;

/**
 * Именованный набор настроек.
 *
 * Хранится в зашифрованной половине (secretSettingKeys в
 * backend/service/settings.go), потому что server — это полная ссылка на прокси.
 * По той же причине профили не попадают в переносимый файл настроек.
 */
export interface Profile {
  id: string;
  name: string;
  /**
   * Ссылка сервера либо null. null — законное значение и означает «профиль
   * задаёт только маршрутизацию»: переключение на него оставит текущий сервер
   * как есть. Это самый частый случай, поэтому сервер запоминается по галке, а
   * не всегда.
   */
  server: string | null;
  settings: ProfileSettings;
}

/**
 * Настройки, прочитанные с диска. На свежей установке файла нет вовсе, и
 * GetSettings возвращает пустой объект — поэтому здесь всё необязательно и
 * каждое поле требует значения по умолчанию на стороне читателя.
 */
export type StoredSettings = Partial<AppSettings>;

/** Итоги трафика сессии, в байтах. */
export interface SessionTraffic {
  up: number;
  down: number;
}

/** Ответ StartXray / RestartXray / StopXray. */
export interface XrayResult {
  success: boolean;
  /** Уже переведённый бэкендом текст ошибки, либо маркер 'admin_required'. */
  error?: string;
}

/** Ответ CheckUpdates. */
export interface UpdateInfo {
  available: boolean;
  version?: string;
  url?: string;
  body?: string;
  /** Отсутствует, если у релиза нет проверяемой подписи — тогда установка в приложении не предлагается. */
  downloadUrl?: string;
  assetName?: string;
  signatureHex?: string;
  signatureMissing?: boolean;
}

/** Результат замера задержки до сервера; -1 означает «не удалось измерить». */
export interface PingResult {
  link: string;
  latency: number;
}

/** Данные события tray-start-reconnect. */
export interface TrayReconnectRequest {
  link: string;
  useSystemProxy: boolean;
}

/** Одна выборка счётчиков трафика из Clash API. */
export interface TrafficStats {
  up: number;
  down: number;
  totalUp: number;
  totalDown: number;
}

/**
 * Держит ли NeoBox сейчас правила брандмауэра. Нужно, чтобы «интернета нет»
 * не выглядело как поломка провайдера.
 */
export interface KillSwitchState {
  /** Правила установлены. */
  active: boolean;
  /**
   * Правила остались от прошлого запуска и снять их не удалось: обычно
   * упавший сеанс с правами администратора и следующий запуск без них.
   * Машина при этом без сети.
   */
  stuck: boolean;
}

/**
 * Одно живое соединение ядра. Зеркалит connectionRow в
 * backend/service/connections.go — там же выбрано, какие поля из Clash API
 * доезжают сюда, а какие отбрасываются, не покидая Go.
 */
export interface ConnectionRow {
  /** UUID соединения: ключ строки в таблице и аргумент closeConnection. */
  id: string;
  host: string;
  destIP: string;
  destPort: string;
  network: string;
  /** Имя файла процесса без пути, либо пусто, если ядро его не определило. */
  process: string;
  /** Правило, по которому ушло соединение; литерал 'final', если не совпало ничего. */
  rule: string;
  outbound: string;
  upload: number;
  download: number;
  /** Unix-миллисекунды: возраст строки пересчитывается на каждом тике. */
  startMs: number;
  /**
   * Правило, которым может стать эта строка. Пусто, когда безопасного правила
   * не существует (приватный адрес, диапазон FakeIP) — в такой строке кнопки
   * маршрутизации должны быть отключены. См. core.SuggestRuleTarget.
   */
  ruleType: '' | CustomRule['type'];
  ruleValue: string;
}

/** Снимок соединений, как его отдаёт GetConnections. */
export interface ConnectionsSnapshot {
  /** false, когда ядро не запущено: список пуст, опрашивать нечего. */
  running: boolean;
  connections: ConnectionRow[];
  /** Сколько соединений у ядра всего — строк приходит не больше 200. */
  total: number;
}

/**
 * Одна завершившаяся сессия во вкладке «История».
 *
 * Хранится у бэкенда в зашифрованном history.json, а не в localStorage: поле
 * link — это полная ссылка на прокси, с UUID и паролем, то есть ровно то, ради
 * чего шифруются подписки. Заодно история перестала пропадать вместе с
 * профилем WebView2 при переустановке. Подробности в
 * backend/service/history.go.
 */
export interface HistoryEntry {
  id: string;
  server: string;
  protocol: string;
  address: string;
  /** Ссылка для повторного подключения одним нажатием. */
  link: string | null;
  connectedAt: number;
  disconnectedAt: number;
  durationSec: number;
  bytesDown: number;
  bytesUp: number;
}

/** Функция отписки, которую возвращает Wails EventsOn. */
export type Unsubscribe = () => void;

/**
 * Поверхность, доступная интерфейсу как window.api.
 *
 * Обёртка над сгенерированными Wails биндингами: та принимает и отдаёт строки
 * JSON, а здесь уже разобранные объекты, и вызовы называются так же, как в
 * старом Electron-варианте.
 */
export interface NeoBoxApi {
  // Соединение
  startXray(link: string, useSystemProxy: boolean): Promise<XrayResult>;
  restartXray(link: string, useSystemProxy: boolean): Promise<XrayResult>;
  stopXray(): Promise<XrayResult>;
  checkTunStatus(): Promise<boolean>;
  getKillSwitchState(): Promise<KillSwitchState>;
  pingServer(link: string): Promise<void>;

  // Живые соединения.
  //
  // Здесь опрос, а не подписка на события: sing-box отдаёт /connections потоком
  // только через websocket, а обычный запрос возвращает один снимок. Раз запрос
  // на выборку всё равно неизбежен, таймер держит сам экран — он и перестаёт
  // спрашивать, когда его закрывают. Подробности в backend/service/connections.go.
  getConnections(): Promise<ConnectionsSnapshot>;
  closeConnection(id: string): Promise<boolean>;

  // Настройки и подписки
  getAppVersion(): Promise<string>;
  getSettings(): Promise<StoredSettings>;
  saveSettings(settings: AppSettings): Promise<boolean>;
  /**
   * Перенос настроек между машинами. Обе открывают системный диалог и
   * возвращают путь к файлу; пустая строка означает, что пользователь закрыл
   * диалог, — это не ошибка.
   *
   * Переносится только открытая половина настроек. Подписки и избранное не
   * экспортируются: это ссылки с учётными данными, они шифруются и привязаны
   * к машине.
   */
  exportSettings(): Promise<string>;
  importSettings(): Promise<string>;
  /**
   * Проверяет значение поля «свой DNS». Пустая строка — годится.
   * Проверяет тот же код, что собирает конфиг, поэтому поле не может принять
   * значение, на котором потом упадёт подключение.
   */
  validateDns(value: string): Promise<string>;
  /**
   * Карта «адрес встроенного резолвера — владелец его сети», та же, по которой
   * собирается конфиг. Нужна проверке утечки: она узнаёт, кто на самом деле
   * обслужил запрос, и сверяет с выбранным. Приходит с бэкенда, а не лежит
   * копией здесь, — копия разошлась бы при первом добавленном провайдере.
   */
  dnsResolverOwners(): Promise<Record<string, string>>;
  getSubscriptions(): Promise<Subscription[]>;
  saveSubscriptions(subs: Subscription[]): Promise<boolean>;
  /**
   * История сессий. Лежит у бэкенда зашифрованной и в переносимый файл
   * настроек не попадает — по той же причине, что и подписки.
   */
  getHistory(): Promise<HistoryEntry[]>;
  saveHistory(history: HistoryEntry[]): Promise<boolean>;
  /**
   * Итоги трафика текущей сессии, в байтах. Считает их Go, и спросить его —
   * единственный способ узнать правду: событий traffic-stats нет, пока окно в
   * трее, а отключаются чаще всего именно оттуда.
   */
  sessionTraffic(): Promise<SessionTraffic>;
  fetchSubscription(url: string): Promise<string[]>;
  /**
   * Загружает одну подписку, не дожидаясь её срока, и записывает исход —
   * включая неудачу. По завершении бэкенд шлёт 'subscriptions-updated'.
   */
  updateSubscriptionNow(id: string): Promise<void>;
  importFromClipboard(): Promise<void>;
  decodeBase64(str: string): string;

  // Обновления
  checkUpdates(): Promise<UpdateInfo>;
  downloadAndInstallUpdate(downloadURL: string, signatureHex: string): Promise<void>;
  openUpdateLink(url: string): void;
  onUpdateProgress(cb: (percent: number) => void): Unsubscribe;
  onUpdateComplete(cb: () => void): Unsubscribe;
  onUpdateError(cb: (message: string) => void): Unsubscribe;

  // Логи
  saveLogs(content: string): Promise<string>;
  openLogsFolder(): Promise<void>;

  // Права администратора
  checkAdmin(): Promise<boolean>;
  requestAdmin(): Promise<void>;

  // Управление окном
  bringToFront(): Promise<void>;
  minimize(): void;
  close(): void;
  onWindowHidden(cb: () => void): void;
  onWindowRestored(cb: () => void): void;
  /** Сообщает бэкенду, что окно снова на экране (разворот мимо трея). */
  notifyWindowShown(): Promise<void>;

  // События ядра
  onLog(cb: (lines: string[]) => void): Unsubscribe;
  onStarted(cb: () => void): Unsubscribe;
  onStopped(cb: () => void): Unsubscribe;
  onPingResult(cb: (result: PingResult) => void): void;
  onSubscriptionResult(cb: (links: string[]) => void): void;
  onSubscriptionsUpdated(cb: () => void): Unsubscribe;

  // События трея
  onTrayToggleConnection(cb: () => void): Unsubscribe;
  onTrayRestart(cb: () => void): Unsubscribe;
  onTrayServerSelected(cb: (link: string) => void): Unsubscribe;
  onTrayStartReconnect(cb: (request: TrayReconnectRequest) => void): Unsubscribe;

  // События watchdog
  onWatchdogReconnecting(cb: () => void): Unsubscribe;
  onWatchdogReconnected(cb: () => void): Unsubscribe;
  onWatchdogFailed(cb: (message: string) => void): Unsubscribe;
  onWatchdogWaiting(cb: () => void): Unsubscribe;
  /**
   * Watchdog переключился на другой сервер: прежний перестал отвечать, а этот
   * ответил. Аргумент — ссылка нового сервера.
   */
  /**
   * Смена сервера сторожем. Вторым аргументом приходят итоги закрываемой
   * сессии: перезапуск ядра обнулит счётчики сразу после события, и спросить
   * их потом уже нельзя.
   */
  onWatchdogSwitched(cb: (link: string, traffic: SessionTraffic) => void): Unsubscribe;
  /**
   * Сервер принимает соединения, но трафик через него не идёт, а заменить его
   * нечем. Отличается от onWatchdogWaiting тем, что сеть у машины есть:
   * пользователю надо менять сервер, а не ждать провайдера.
   */
  onWatchdogServerDead(cb: () => void): Unsubscribe;

  /**
   * Системные горячие клавиши: одна переключает подключение, вторая
   * показывает окно. Сочетания задаёт пользователь; пустая строка означает
   * значение по умолчанию (Ctrl+Shift+V и Ctrl+Shift+B).
   *
   * Возвращает пустую строку при успехе и причину отказа, если сочетание уже
   * занято другим приложением: RegisterHotKey забирает его у всей системы, и
   * второму желающему Windows отказывает. Галка в «Настройках» на такой ответ
   * снимается обратно — стоящая, но не работающая, она была бы хуже
   * отсутствующей.
   */
  setGlobalHotkeys(enable: boolean, toggleCombo: string, showCombo: string): Promise<string>;
  onHotkeyToggleConnection(cb: () => void): Unsubscribe;
  onHotkeyShowWindow(cb: () => void): Unsubscribe;
  /**
   * Щелчок по быстрой галке в меню трея. Аргумент — имя поля настроек:
   * killSwitch, tunMode или systemProxy.
   */
  onTrayToggleSetting(cb: (key: 'killSwitch' | 'tunMode' | 'systemProxy') => void): Unsubscribe;

  /**
   * Перестраивает подменю «Профили» в трее. Зовётся после каждого
   * сохранения или удаления: список живёт на диске, и собирает меню заново
   * Go, прочитав профили оттуда.
   */
  rebuildTrayProfiles(): Promise<void>;
  /** Пользователь выбрал профиль в трее. Аргумент — идентификатор профиля. */
  onTrayProfileSelected(cb: (id: string) => void): Unsubscribe;

  /**
   * Выборка счётчиков трафика раз в секунду, пока ядро запущено и окно видно.
   * В трее события не приходят: их некому смотреть, а каждое — отдельный
   * ExecuteScript в WebView2.
   */
  onTraffic(cb: (stats: TrafficStats) => void): Unsubscribe;
}
