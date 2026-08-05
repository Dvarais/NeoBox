// Контракт между фронтендом и Go-бэкендом.
//
// Здесь только типы: реализация живёт в wails-bridge.ts, а сюда смотрят и она,
// и все остальные модули. Разъезд между тем, что фронтенд пишет в SaveSettings,
// и тем, что бэкенд оттуда читает, — не гипотетическая проблема: поле
// systemProxy интерфейс показывал, tray.go читал, а сохранять его не сохранял
// никто, и подключение из трея молча уходило без системного прокси.

import type { Language } from './translations';

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
}

/** Пользовательское правило маршрутизации. Зеркалит core.CustomRule. */
export interface CustomRule {
  action: 'direct' | 'proxy' | 'block';
  type: 'domain' | 'domain_suffix' | 'domain_keyword' | 'ip_cidr';
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
  killSwitch: boolean;
  dnsLeak: boolean;
  ipv6Leak: boolean;
  fakeDns: boolean;
  verboseLogging: boolean;
  lastSelectedServer: string | null;
  customDirect: string[];
  processMode: 'blacklist' | 'whitelist';
  processListBlacklist: string[];
  processListWhitelist: string[];
  favoriteLinks: string[];
  customRules: CustomRule[];
}

/**
 * Настройки, прочитанные с диска. На свежей установке файла нет вовсе, и
 * GetSettings возвращает пустой объект — поэтому здесь всё необязательно и
 * каждое поле требует значения по умолчанию на стороне читателя.
 */
export type StoredSettings = Partial<AppSettings>;

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
  pingServer(link: string): Promise<void>;

  // Настройки и подписки
  getAppVersion(): Promise<string>;
  getSettings(): Promise<StoredSettings>;
  saveSettings(settings: AppSettings): Promise<boolean>;
  getSubscriptions(): Promise<Subscription[]>;
  saveSubscriptions(subs: Subscription[]): Promise<boolean>;
  fetchSubscription(url: string): Promise<string[]>;
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
}
