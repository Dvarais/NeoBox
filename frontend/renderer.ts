import { el, optionalEl, query } from './modules/dom';
import { translations } from './modules/translations';
import type { Language, Translations } from './modules/translations';
import type { AppSettings, CustomRule, UpdateInfo } from './modules/api';
import { fetchIP, showConfirm, showAlert } from './modules/ui-utils';
import {
    allSubscriptions,
    currentActiveSubId,
    loadSubscriptions as loadSubsBase,
    renderSubTabs,
    setActiveSubId,
    setSubscriptions
} from './modules/subscription-manager';
import {
    renderCards,
    parseBasicInfo,
    pingData,
    currentSortMode,
    setSortMode,
    setPingData
} from './modules/server-manager';
import type { SortMode } from './modules/server-manager';

// Элементы DOM
const powerBtn = el('powerBtn');
const restartBtn = el('restartBtn');
const disconnectBtn = el('disconnectBtn');
const statusDot = el('statusDot');
const statusText = el('statusText');
const currentIp = el('currentIp');
const fullLogOutput = el('fullLogOutput');
const clearLogsBtn = el('clearLogsBtn');
const activeServerName = el('activeServerName');
const activeServerDetails = el('activeServerDetails');
const serversGrid = el('serversGrid');
const subTabsContainer = el('subscription-tabs');
const processListBlacklistEl = el<HTMLTextAreaElement>('processListBlacklist');
const processListWhitelistEl = el<HTMLTextAreaElement>('processListWhitelist');

/** Состояние соединения, каким его показывает интерфейс. */
type AppState = 'off' | 'connecting' | 'on';

// Состояние
let activeServerLink: string | null = null;
let currentLanguage: Language = 'RU';
let isRestarting = false;
let appState: AppState = 'off';
let tunStatusInterval: ReturnType<typeof setInterval> | null = null;

// Сессионный счётчик трафика (привязаны к window для совместимости с wails-bridge.js)
window.sessionBytesDown = 0;
window.sessionBytesUp = 0;
let sessionConnectedAt: number | null = null; // timestamp начала сессии
let sessionTimerInterval: ReturnType<typeof setInterval> | null = null;
let sessionTimerStart = 0; // отсчитываем от метки, а не от числа тиков
// false, пока окно свёрнуто в трей: там UI никто не видит, а каждая его
// «живая» деталь держит WebView2 в работе. См. setUiActive.
let uiActive = true;

// Favorites
let favoriteLinks = new Set<string>();
let customRules: CustomRule[] = [];

// Search query for server filter
let serverSearchQuery = '';

function onToggleFavorite(link: string) {
  if (favoriteLinks.has(link)) {
    favoriteLinks.delete(link);
  } else {
    favoriteLinks.add(link);
  }
  updateCards();
  collectAndSaveSettings();
}

// Навигация
const navItems = document.querySelectorAll<HTMLElement>('.nav-item');
const views = document.querySelectorAll<HTMLElement>('.view');

// Инициализируем активную вкладку для кастомных стилей
document.body.setAttribute('data-active-tab', 'view-home');

navItems.forEach(item => {
  item.addEventListener('click', () => {
    navItems.forEach(i => i.classList.remove('active'));
    views.forEach(v => v.classList.remove('active'));
    item.classList.add('active');
    const targetId = item.getAttribute('data-target') ?? '';
    const targetView = optionalEl(targetId);
    if (targetView) targetView.classList.add('active');
    
    // Устанавливаем атрибут активной вкладки для кастомных стилей и виджета скроллбара
    document.body.setAttribute('data-active-tab', targetId);
    if (targetId === 'view-servers') {
      setTimeout(updateCustomScroll, 50);
    }
    // Lines that arrived while this view was closed were kept out of the DOM;
    // rebuild the panel now that it is visible.
    if (targetId === 'view-logs' && logsDomStale) renderLogs();
  });
});

function updateCards() {
    let servers: string[] = [];
    if (currentActiveSubId === 'all') {
        allSubscriptions.forEach(s => servers.push(...s.links));
    } else if (currentActiveSubId === 'favorites') {
        servers = Array.from(favoriteLinks);
    } else {
        const sub = allSubscriptions.find(s => s.id === currentActiveSubId);
        if (sub) servers = sub.links;
    }
    renderCards(serversGrid, servers, activeServerLink, pingData, currentSortMode, (link, name, type, address) => {
        const isNewServer = activeServerLink !== link;
        activeServerLink = link;
        activeServerName.textContent = name;
        activeServerDetails.textContent = `${type} • ${address}`;
        updateCards();
        collectAndSaveSettings();
        if (isNewServer && (appState === 'on' || appState === 'connecting')) {
            restartBtn.click();
        }
    }, serverSearchQuery, favoriteLinks, onToggleFavorite, translations[currentLanguage]);
}

async function loadSubscriptions() {
    await loadSubsBase(() => {
        renderSubTabs(subTabsContainer, translations, currentLanguage, () => {
            updateCards();
        }, loadSubscriptions);
        updateCards();
    });
}

// translate достаёт подпись по ключу, который собирается на лету из
// data-атрибута разметки (sortName, logWarn и подобные). Проверить такой ключ на
// этапе компиляции нельзя, поэтому послабление типов признаётся здесь один раз,
// а не приведением к any в каждой точке вызова. Неизвестный ключ возвращается
// как есть — это заметно на экране и не роняет отрисовку.
function translate(t: Translations, key: string): string {
  return (t as unknown as Record<string, string>)[key] ?? key;
}

// applyLanguage touches a lot of nodes; these guarded helpers keep it readable and
// tolerate elements that only exist in some states of the UI.
const setText = (id: string, value: string) => { const node = optionalEl(id); if (node) node.textContent = value; };
const setTitle = (id: string, value: string) => { const node = optionalEl(id); if (node) node.title = value; };
const setPlaceholder = (id: string, value: string) => { const node = optionalEl<HTMLInputElement>(id); if (node) node.placeholder = value; };

function applyLanguage() {
  const t = translations[currentLanguage];

  document.querySelectorAll<HTMLElement>('.nav-item').forEach(item => {
    const target = item.getAttribute('data-target');
    if (target === 'view-home') item.title = t.home;
    if (target === 'view-servers') item.title = t.servers;
    if (target === 'view-history') item.title = t.history;
    if (target === 'view-routes') item.title = t.routes;
    if (target === 'view-settings') item.title = t.settings;
    if (target === 'view-logs') item.title = t.logs;
  });
  el('langToggle').textContent = currentLanguage;

  // Window controls and the servers-tab scrollbar widget are icon-only, so their
  // tooltip is the only text they ever show.
  setTitle('minimizeBtn', t.minimizeBtnTitle);
  setTitle('closeBtn', t.closeBtnTitle);
  setTitle('scrollUpBtn', t.scrollUpTitle);
  setTitle('scrollDownBtn', t.scrollDownTitle);

  updateAppInterface(appState);
  
  if (['Определяется...', 'Determining...', 'Обновление...', 'Определяю...'].includes(currentIp.textContent)) {
    currentIp.textContent = t.ipDetermining;
  }
  if (['Ошибка сети', 'Network Error'].includes(currentIp.textContent)) {
    currentIp.textContent = t.ipError;
  }
  
  if (activeServerName.textContent === 'Сервер не выбран' || activeServerName.textContent === 'No Server Selected') {
    activeServerName.textContent = t.noServerSelected;
  }
  if (activeServerDetails.textContent === 'Выберите локацию во вкладке Серверы' || activeServerDetails.textContent === 'Select a location in the Servers tab') {
    activeServerDetails.textContent = t.selectLocation;
  }
  
  el('restartBtnText').textContent = t.restartBtn;
  el('disconnectBtnText').textContent = t.disconnectBtn;
  el('speedDownloadLabel').textContent = t.downloadLabel;
  el('speedUploadLabel').textContent = t.uploadLabel;
  const speedTotalLabel = optionalEl('speedTotalLabel');
  if (speedTotalLabel) speedTotalLabel.textContent = t.totalTrafficLabel;

  // Переводы для игрового спидометра
  const trafficGameTitle = optionalEl('trafficGameTitle');
  if (trafficGameTitle) trafficGameTitle.textContent = t.trafficGameTitle;
  const trafficGameDownLabel = optionalEl('trafficGameDownLabel');
  if (trafficGameDownLabel) trafficGameDownLabel.textContent = t.trafficGameDownLabel;
  const trafficGameUpLabel = optionalEl('trafficGameUpLabel');
  if (trafficGameUpLabel) trafficGameUpLabel.textContent = t.trafficGameUpLabel;
  const trafficGameLimitLabel = optionalEl('trafficGameLimitLabel');
  if (trafficGameLimitLabel) trafficGameLimitLabel.textContent = t.trafficGameLimitLabel;

  el('importQrBtn').textContent = t.importQrBtn;
  el('qrModalTitle').textContent = t.qrModalTitle;
  el('qrStartCameraBtn').textContent = t.qrStartCameraBtn;
  el('qrUploadFileBtn').textContent = t.qrUploadFileBtn;
  el('qrPlaceholderText').textContent = t.qrPlaceholderText;
  el('qrModalClose').textContent = t.errorDialogClose;

  el('subManagementTitle').textContent = t.subManagement;
  el<HTMLInputElement>('subName').placeholder = t.subNamePlaceholder;
  el<HTMLInputElement>('subUrl').placeholder = t.subUrlPlaceholder;
  el('addSubBtn').textContent = t.addBtn;
  el('updateSubBtn').textContent = t.updateCurrentBtn;
  setTitle('updateSubBtn', t.updateCurrentBtnTitle);
  el('importClipboardBtn').textContent = t.importClipboardBtn;
  el('myLocationsTitle').textContent = t.myLocations;
  el('pingAllBtn').textContent = t.pingAllBtn;
  el('sortBtnText').textContent = t.sortBtn;
  setPlaceholder('serverSearchInput', t.serverSearchPlaceholder);
  setTitle('bestServerBtn', t.bestServerBtnTitle);
  setText('bestServerBtnText', t.bestServerBtn);

  document.querySelectorAll<HTMLElement>('.sort-item').forEach(item => {
    const mode = item.dataset.sort ?? '';
    item.textContent = translate(t, `sort${mode.charAt(0).toUpperCase() + mode.slice(1)}`);
  });

  el('routeSettingsTitle').textContent = t.routeSettings;
  el('directDomainsLabel').textContent = t.directDomainsLabel;
  el('bypassRuLabel').textContent = t.bypassRuLabel;
  el('splitTunnelingTitle').textContent = t.splitTunnelingTitle;
  el('splitTunnelingDesc').innerHTML = t.splitTunnelingDesc.replace('chrome.exe', '<b>chrome.exe</b>');
  
  document.querySelectorAll<HTMLElement>('.process-tab').forEach(tab => {
    const mode = tab.dataset.mode;
    tab.textContent = mode === 'blacklist' ? t.blacklistTab : t.whitelistTab;
  });
  el<HTMLTextAreaElement>('processListBlacklist').placeholder = t.blacklistPlaceholder;
  el<HTMLTextAreaElement>('processListWhitelist').placeholder = t.whitelistPlaceholder;

  const customRoutesTitle = optionalEl('customRoutesTitle');
  if (customRoutesTitle) customRoutesTitle.textContent = t.customRoutesTitle;
  const customRoutesDesc = optionalEl('customRoutesDesc');
  if (customRoutesDesc) customRoutesDesc.textContent = t.customRoutesDesc;
  const addCustomRuleBtn = optionalEl('addCustomRuleBtn');
  if (addCustomRuleBtn) addCustomRuleBtn.textContent = t.addCustomRuleBtn;
  const saveRoutesBtn2 = optionalEl('saveRoutesBtn2');
  if (saveRoutesBtn2) saveRoutesBtn2.textContent = t.saveRoutesBtn2;
  const routesStatus2 = optionalEl('routesStatus2');
  if (routesStatus2) routesStatus2.textContent = t.statusDone;
  
  const optActionDirect = optionalEl('optActionDirect');
  if (optActionDirect) optActionDirect.textContent = t.actionDirectOption;
  const optActionProxy = optionalEl('optActionProxy');
  if (optActionProxy) optActionProxy.textContent = t.actionProxyOption;
  const optActionBlock = optionalEl('optActionBlock');
  if (optActionBlock) optActionBlock.textContent = t.actionBlockOption;
  
  const optTypeSuffix = optionalEl('optTypeSuffix');
  if (optTypeSuffix) optTypeSuffix.textContent = t.typeSuffixOption;
  const optTypeDomain = optionalEl('optTypeDomain');
  if (optTypeDomain) optTypeDomain.textContent = t.typeDomainOption;
  const optTypeKeyword = optionalEl('optTypeKeyword');
  if (optTypeKeyword) optTypeKeyword.textContent = t.typeKeywordOption;
  const optTypeIp = optionalEl('optTypeIp');
  if (optTypeIp) optTypeIp.textContent = t.typeIpOption;
  
  const newRuleValue = optionalEl<HTMLInputElement>('newRuleValue');
  if (newRuleValue) newRuleValue.placeholder = t.ruleValuePlaceholder;
  
  renderCustomRules();

  el('appSettingsTitle').textContent = t.appSettingsTitle;
  el('dnsServerLabel').textContent = t.dnsServerLabel;
  query('#dnsSelect option[value="custom"]').textContent = t.placeholderDns;
  el('tunModeLabel').textContent = t.tunModeLabel;
  el('tunModeDesc').textContent = t.tunModeDesc;
  el('systemProxyLabel').textContent = t.systemProxyLabel;
  el('autoConnectLabel').textContent = t.autoConnectLabel;
  el('autoUpdateSubsLabel').textContent = t.autoUpdateSubsLabel;
  el('rememberServerLabel').textContent = t.rememberServerLabel;
  el('openAtLoginLabel').textContent = t.openAtLoginLabel;
  el('startMinimizedLabel').textContent = t.startMinimizedLabel;
  el('securityTitle').textContent = t.securityTitle;
  el('killSwitchLabel').textContent = t.killSwitchLabel;
  el('killSwitchDesc').textContent = t.killSwitchDesc;
  el('dnsLeakLabel').textContent = t.dnsLeakLabel;
  el('ipv6LeakLabel').textContent = t.ipv6LeakLabel;
  el('fakeDnsLabel').textContent = t.fakeDnsLabel;
  el('fakeDnsDesc').textContent = t.fakeDnsDesc;
  setText('verboseLoggingLabel', t.verboseLoggingLabel);
  setText('verboseLoggingDesc', t.verboseLoggingDesc);
  el('saveRoutesBtn').textContent = t.saveRoutesBtn;
  el('routesStatus').textContent = t.statusDone;
  el('saveAppsBtn').textContent = t.saveAppsBtn;
  el('appsStatus').textContent = t.statusDone;
  el('saveSettingsBtn').textContent = t.saveAllBtn;
  el('settingsStatus').textContent = t.statusDone;
  el('logsTitle').textContent = t.logsTitle;
  el('clearLogsBtn').textContent = t.logsClearBtn;
  setText('saveLogsBtnText', t.saveLogsBtn);
  setTitle('saveLogsBtn', t.saveLogsBtnTitle);
  document.querySelectorAll<HTMLElement>('.log-tab').forEach(tab => {
    const filter = tab.dataset.filter ?? '';
    tab.textContent = translate(t, `log${filter.charAt(0) + filter.slice(1).toLowerCase()}`);
  });

  // History tab. The cards themselves are rebuilt by renderHistoryTab() below,
  // which picks up the language on its own.
  setText('historyTitle', t.historyTitle);
  setText('clearHistoryBtnText', t.historyClearBtn);
  setText('historyStatSessionsLabel', t.historyStatSessionsLabel);
  setText('historyStatTimeLabel', t.historyStatTimeLabel);
  setText('historyStatDownLabel', t.historyStatDownLabel);
  setText('historyStatUpLabel', t.historyStatUpLabel);
  setText('historyEmptyText', t.historyEmptyText);
  renderHistoryTab();

  // Shared prompt/confirm modal — its buttons are reused by every dialog.
  setText('modalCancel', t.modalCancel);
  setText('modalConfirm', t.modalConfirm);

  // DNS leak test. Only the static chrome is set here; the verdict line is
  // written by runDnsLeakTest() when a test actually runs.
  setText('dnsLeakBtnText', t.dnsLeakBtn);
  setText('dnsLeakModalTitle', t.dnsLeakModalTitle);
  setText('dnsLeakModalSubtitle', t.dnsLeakModalSubtitle);
  setText('dnsLeakLoadingText', t.dnsLeakLoadingText);
  setText('dnsLeakIpLabel', t.dnsLeakIpLabel);
  setText('dnsLeakDnsLabel', t.dnsLeakDnsLabel);
  setText('dnsLeakRetryBtnText', t.dnsLeakRetryBtn);

  // Update Modal translations
  const updateModalTitleEl = optionalEl('updateModalTitle');
  if (updateModalTitleEl) updateModalTitleEl.textContent = t.updateModalTitle;
  
  const updateModalChangelogTitleEl = document.querySelector<HTMLElement>('.update-changelog-title');
  if (updateModalChangelogTitleEl) updateModalChangelogTitleEl.textContent = t.updateModalChangelogTitle;
  
  const updateModalCancelEl = optionalEl('updateModalCancel');
  if (updateModalCancelEl) updateModalCancelEl.textContent = t.updateModalCancel;
  
  const updateModalConfirmEl = optionalEl('updateModalConfirm');
  if (updateModalConfirmEl) updateModalConfirmEl.textContent = t.updateModalConfirm;

  const tunStatusTitle = optionalEl('tunStatusTitle');
  if (tunStatusTitle) tunStatusTitle.textContent = t.tunStatusTitle;
  const restoreTunBtnText = optionalEl('restoreTunBtnText');
  if (restoreTunBtnText) restoreTunBtnText.textContent = t.tunStatusRestoreBtn;
  // Placeholder until the async probe below reports back.
  setText('tunStatusText', t.tunStatusChecking);
  checkAndUpdateTunStatus();

  renderSubTabs(subTabsContainer, translations, currentLanguage, updateCards, loadSubscriptions);
  updateCards();
}

el('langToggle').onclick = () => {
  currentLanguage = currentLanguage === 'RU' ? 'EN' : 'RU';
  applyLanguage();
  collectAndSaveSettings();
};

// Сортировка
const sortDropdown = query('.sort-dropdown');
const sortMenu = query('.sort-menu');
const sortItems = document.querySelectorAll<HTMLElement>('.sort-item');
let sortMenuTimeout: ReturnType<typeof setTimeout>;

sortDropdown.addEventListener('mouseenter', () => {
  clearTimeout(sortMenuTimeout);
  sortMenu.classList.add('show');
});

sortDropdown.addEventListener('mouseleave', () => {
  sortMenuTimeout = setTimeout(() => sortMenu.classList.remove('show'), 550);
});

sortItems.forEach(item => {
  item.addEventListener('click', () => {
    sortItems.forEach(i => i.classList.remove('active'));
    item.classList.add('active');
    setSortMode(item.dataset.sort as SortMode);
    updateCards();
    sortMenu.classList.remove('show');
  });
});

// Split Tunneling
const processTabs = document.querySelectorAll<HTMLElement>('.process-tab');
const processModeHidden = el<HTMLInputElement>('processModeHidden');

processTabs.forEach(tab => {
  tab.addEventListener('click', (e) => {
    processTabs.forEach(t => {
      t.classList.remove('active');
      t.style.background = 'transparent';
      t.style.color = 'var(--text-main)';
      t.style.fontWeight = 'normal';
    });
    const target = e.currentTarget as HTMLElement;
    target.classList.add('active');
    target.style.background = 'var(--accent-color)';
    target.style.color = '#000';
    target.style.fontWeight = 'bold';
    processModeHidden.value = target.getAttribute('data-mode') ?? 'blacklist';

    if (processModeHidden.value === 'blacklist') {
      processListBlacklistEl.style.display = 'block';
      processListWhitelistEl.style.display = 'none';
    } else {
      processListBlacklistEl.style.display = 'none';
      processListWhitelistEl.style.display = 'block';
    }
  });
});

// updateAppInterface перерисовывает интерфейс под состояние соединения.
//
// Функция вызывается в двух совершенно разных случаях: при фактической смене
// состояния и при простой перерисовке — applyLanguage() зовёт её с текущим
// состоянием, чтобы подписи сменили язык. Поэтому побочные эффекты входа в
// состояние (начало отсчёта сессии, запрос IP) выполняются только когда
// состояние действительно изменилось. Раньше они выполнялись всегда, и
// переключение RU/EN на подключённом VPN обнуляло и таймер соединения, и
// sessionConnectedAt — то есть портило длительность записи в истории.
function updateAppInterface(state: AppState) {
  const entered = appState !== state;
  appState = state;
  const t = translations[currentLanguage];
  const timerBadge = el('sessionTimerBadge');
  const timerText = el('sessionTimerText');

  if (state === 'on') {
    powerBtn.classList.add('on', 'pulse-animation');
    statusDot.className = 'status-dot on';
    statusText.textContent = t.statusOn;
    statusText.style.color = 'var(--success)';
    restartBtn.style.display = 'flex';
    disconnectBtn.style.display = 'flex';
    if (entered) {
      isRestarting = false;
      setTimeout(() => fetchIP(currentIp, t), 2000);
      startSessionTracking();
      sessionTimerStart = Date.now();
    }
    // Start live connection timer
    if (timerBadge) timerBadge.style.display = 'flex';
    // Идемпотентно (сам снимает прошлые интервалы), поэтому вызывается и при
    // перерисовке: иначе смена языка оставила бы таймер стоять.
    startLiveTimers();
  } else if (state === 'connecting') {
    powerBtn.classList.add('on');
    powerBtn.classList.remove('pulse-animation');
    statusDot.className = 'status-dot connecting';
    statusText.textContent = t.statusConnecting;
    statusText.style.color = 'var(--accent-color)';
    currentIp.textContent = t.ipDetermining;
    restartBtn.style.display = 'none';
    disconnectBtn.style.display = 'flex';

    clearInterval(tunStatusInterval ?? undefined);
    tunStatusInterval = null;
    const tunStatusContainer = optionalEl('tunStatusContainer');
    if (tunStatusContainer) tunStatusContainer.style.display = 'none';
  } else {
    powerBtn.classList.remove('on', 'pulse-animation');
    statusDot.className = 'status-dot';
    statusText.textContent = t.statusOff;
    statusText.style.color = 'var(--text-dim)';
    restartBtn.style.display = 'none';
    disconnectBtn.style.display = 'none';
    currentIp.textContent = '—';
    // Сюда приходит и неудавшийся перезапуск. Флаг обязан сброситься именно
    // здесь: иначе он оставался бы поднятым навсегда, и следующий самопроизвольный
    // обрыв туннеля прошёл бы мимо onStopped — интерфейс остался бы «подключено»,
    // а сессия не попала бы в историю.
    isRestarting = false;
    stopLiveTimers();
    sessionTimerStart = 0;
    if (timerBadge) timerBadge.style.display = 'none';
    if (timerText) timerText.textContent = '00:00:00';

    const tunStatusContainer = optionalEl('tunStatusContainer');
    if (tunStatusContainer) tunStatusContainer.style.display = 'none';
  }
}

// Таймер считается от sessionTimerStart, а не накоплением тиков, — иначе после
// паузы в трее он отстал бы ровно на время, что окно было скрыто.
function renderSessionTimer() {
  const timerText = el('sessionTimerText');
  if (!timerText || !sessionTimerStart) return;
  const elapsed = Math.floor((Date.now() - sessionTimerStart) / 1000);
  const h = String(Math.floor(elapsed / 3600)).padStart(2, '0');
  const m = String(Math.floor((elapsed % 3600) / 60)).padStart(2, '0');
  const s = String(elapsed % 60).padStart(2, '0');
  timerText.textContent = `${h}:${m}:${s}`;
}

// startLiveTimers запускает опрос, который имеет смысл только при видимом окне.
function startLiveTimers() {
  stopLiveTimers();
  if (!uiActive || appState !== 'on') return;

  renderSessionTimer();
  sessionTimerInterval = setInterval(renderSessionTimer, 1000);

  checkAndUpdateTunStatus();
  tunStatusInterval = setInterval(checkAndUpdateTunStatus, 3000);
}

function stopLiveTimers() {
  clearInterval(sessionTimerInterval ?? undefined);
  sessionTimerInterval = null;
  clearInterval(tunStatusInterval ?? undefined);
  tunStatusInterval = null;
}

// setUiActive переводит интерфейс в простой, когда окно уходит в трей: гасит
// опросы и через класс app-idle останавливает бесконечные CSS-анимации, которые
// иначе держали бы композитор WebView2 занятым круглосуточно.
function setUiActive(active: boolean) {
  if (uiActive === active) return;
  uiActive = active;
  document.body.classList.toggle('app-idle', !active);
  if (active) {
    startLiveTimers();
  } else {
    stopLiveTimers();
  }
}

async function checkAndUpdateTunStatus() {
  const isTunChecked = el<HTMLInputElement>('tunModeCheckbox').checked;
  const tunStatusContainer = optionalEl('tunStatusContainer');
  if (!tunStatusContainer) return;
  
  if (appState === 'on' && isTunChecked) {
    tunStatusContainer.style.display = 'flex';
    try {
      const isActive = await window.api.checkTunStatus();
      const t = translations[currentLanguage];
      const statusTextEl = optionalEl('tunStatusText');
      const statusIconEl = el('tunStatusIcon');
      
      if (statusTextEl && statusIconEl) {
        if (isActive) {
          statusTextEl.textContent = t.tunStatusActive;
          statusTextEl.style.color = 'var(--success)';
          statusIconEl.style.background = 'rgba(74, 222, 128, 0.1)';
        } else {
          statusTextEl.textContent = t.tunStatusError;
          statusTextEl.style.color = 'var(--danger)';
          statusIconEl.style.background = 'rgba(239, 68, 68, 0.1)';
        }
      }
    } catch (e) {
      console.error('Failed to check TUN status:', e);
    }
  } else {
    tunStatusContainer.style.display = 'none';
  }
}

const restoreTunBtn = optionalEl('restoreTunBtn');
if (restoreTunBtn) {
  restoreTunBtn.onclick = () => {
    if (restartBtn && restartBtn.style.display !== 'none') {
      restartBtn.click();
    }
  };
}

// Логгер

/** Уровень строки лога, как его определяет parseLogLine. */
type LogLevel = 'INFO' | 'WARN' | 'ERROR' | 'DEBUG';

/** Активный фильтр вкладки «Логи»; ALL означает «показывать всё». */
type LogFilter = LogLevel | 'ALL';

/** Строка лога в буфере: только сырой текст и уровень — разметка строится при показе. */
interface LogEntry {
  level: LogLevel;
  text: string;
}

const MAX_LOG_ENTRIES = 500;
const logsArray: LogEntry[] = [];
let currentLogFilter: LogFilter = 'ALL';
let isScrolledToBottom = true;
// True when lines arrived while the Logs view was closed, so the panel needs a
// full rebuild before it is shown again.
let logsDomStale = false;

fullLogOutput.addEventListener('scroll', () => {
    isScrolledToBottom = Math.abs(fullLogOutput.scrollHeight - fullLogOutput.clientHeight - fullLogOutput.scrollTop) < 5;
});

document.querySelectorAll<HTMLElement>('.log-tab').forEach(tab => {
  tab.addEventListener('click', (e) => {
      document.querySelectorAll<HTMLElement>('.log-tab').forEach(t => t.classList.remove('active'));
      const tabEl = e.currentTarget as HTMLElement;
      tabEl.classList.add('active');
      currentLogFilter = tabEl.dataset.filter as LogFilter;
      renderLogs();
  });
});

function stripAnsi(str: string) {
  return str.replace(/[\u001b\u009b][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]/g, '');
}

// Prevent XSS: escape HTML special chars before injecting into innerHTML.
// Log messages contain domain names and server addresses from the network.
//
// Non-string input is coerced rather than allowed to throw: callers pass values
// parsed out of proxy links and stored history, where a field can legitimately
// be undefined, and a TypeError here would take out the whole render.
function escapeHtml(str: unknown) {
  return String(str ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

// normalizeLevel сводит слово, которым ядро пометило строку, к одному из
// четырёх уровней, которые понимают и вкладки фильтра, и стили значков.
//
// Раньше сюда приводилось только WARNING → WARN, а всё остальное попадало в
// разметку как есть: строка уровня FATAL получала класс badge-fatal, которого
// нет в стилях, и не совпадала ни с одной вкладкой фильтра — то есть исчезала
// отовсюду, кроме «ВСЕ». Для фатальной ошибки это ровно противоположно нужному.
function normalizeLevel(raw: string): LogLevel {
  switch (raw.toUpperCase()) {
    case 'WARN':
    case 'WARNING':
      return 'WARN';
    case 'ERROR':
    case 'FATAL':
    case 'PANIC':
      return 'ERROR';
    case 'DEBUG':
    case 'TRACE':
      return 'DEBUG';
    default:
      return 'INFO';
  }
}

// errorText вытаскивает читаемый текст из того, что прилетело в catch.
//
// Wails отклоняет вызов биндинга то объектом Error, то голой строкой, а под
// strict пойманное значение имеет тип unknown — то есть обратиться к .message
// напрямую нельзя. Разбор собран здесь один раз вместо приведения типа в каждом
// catch.
function errorText(e: unknown): string {
  if (!e) return '';
  if (typeof e === 'string') return e;
  if (e instanceof Error) return e.message;
  return String(e);
}

// parseLogLine splits an already ANSI-stripped line into its parts. It runs on
// every line that arrives, so it deliberately does no escaping or highlighting
// — that work lives in renderLogHtml and only runs for lines actually going on
// screen.
function parseLogLine(clean: string): {
  level: LogLevel;
  timeStr: string;
  contextStr: string;
  messageStr: string;
  isSuccess: boolean;
} {
  let level: LogLevel = 'INFO';
  let timeStr = '';
  let contextStr = '';
  let messageStr = clean;

  const match = clean.match(/^([A-Z]+)\[([0-9]+)\]\s+(?:\[([0-9\s.a-zA-Z]+)\]\s+)?(.*)$/);
  if (match) {
    level = normalizeLevel(match[1]);
    timeStr = match[2];
    contextStr = match[3] || '';
    messageStr = match[4];
  } else {
    // Fallback parsing
    const upper = clean.toUpperCase();
    if (upper.includes('ERROR') || upper.includes('FATAL') || upper.includes('FAILED')) {
      level = 'ERROR';
    } else if (upper.includes('WARN') || upper.includes('WARNING')) {
      level = 'WARN';
    } else if (upper.includes('DEBUG')) {
      level = 'DEBUG';
    }
  }

  // Fix the NOERROR -> ERROR bug
  let isSuccess = false;
  const upperMsg = messageStr.toUpperCase();
  if (upperMsg.includes('NOERROR') || upperMsg.includes('SUCCESS') || upperMsg.includes('OPENED') || upperMsg.includes('STARTED')) {
    isSuccess = true;
    if (level === 'ERROR') {
      level = 'INFO';
    }
  }

  return { level, timeStr, contextStr, messageStr, isSuccess };
}

// renderLogHtml turns a parsed line into markup. Only called for lines being
// put into the DOM, which is why the log panel costs nothing while it is closed.
function renderLogHtml({ level, timeStr, contextStr, messageStr, isSuccess }: ReturnType<typeof parseLogLine>) {
  let html = '';

  // 1. Time badge
  if (timeStr) {
    let formattedTime = timeStr;
    if (timeStr.length === 4) {
      formattedTime = timeStr.slice(0, 2) + ':' + timeStr.slice(2);
    }
    html += `<span class="log-time">[${formattedTime}]</span> `;
  }

  // 2. Level badge
  let badgeClass = `badge-${level.toLowerCase()}`;
  let displayLevel: string = level;
  if (isSuccess && level === 'INFO') {
    badgeClass = 'badge-success';
    displayLevel = 'SUCCESS';
  }
  html += `<span class="log-badge ${badgeClass}">${displayLevel}</span> `;

  // 3. Context badge
  if (contextStr) {
    const cleanedContext = escapeHtml(contextStr.trim().replace(/\s+/, ' • '));
    html += `<span class="log-context">[${cleanedContext}]</span> `;
  }

  // 4. Message formatting — escape HTML first, then apply safe highlight patterns
  let formattedMessage = escapeHtml(messageStr);
  
  // Highlight elements
  if (formattedMessage.includes('dns: exchanged')) {
    formattedMessage = formattedMessage.replace(/exchanged\s+([a-zA-Z0-9.-]+)/g, 'exchanged <span class="log-domain">$1</span>');
    formattedMessage = formattedMessage.replace(/NOERROR/g, '<span class="log-status-success">NOERROR</span>');
    formattedMessage = formattedMessage.replace(/NXDOMAIN/g, '<span class="log-status-warn">NXDOMAIN</span>');
    formattedMessage = formattedMessage.replace(/SERVFAIL/g, '<span class="log-status-error">SERVFAIL</span>');
  }

  formattedMessage = formattedMessage.replace(/(outbound\/[a-zA-Z0-9-]+\[[a-zA-Z0-9-]+\])/g, '<span class="log-component">$1</span>');
  formattedMessage = formattedMessage.replace(/(inbound\/[a-zA-Z0-9-]+\[[a-zA-Z0-9-]+\])/g, '<span class="log-component">$1</span>');
  formattedMessage = formattedMessage.replace(/connection opened/g, '<span class="log-status-success">connection opened</span>');
  formattedMessage = formattedMessage.replace(/connection closed/g, '<span class="log-status-dim">connection closed</span>');

  html += `<span class="log-msg">${formattedMessage}</span>`;

  return html;
}

function isLogsViewOpen() {
  return document.body.getAttribute('data-active-tab') === 'view-logs';
}

function makeLogElement(entry: LogEntry) {
  const node = document.createElement('div');
  node.className = `log-line log-${entry.level.toLowerCase()}`;
  node.innerHTML = renderLogHtml(parseLogLine(entry.text));
  return node;
}

function renderLogs() {
  fullLogOutput.innerHTML = '';
  const fragment = document.createDocumentFragment();
  logsArray.forEach(entry => {
      if (currentLogFilter !== 'ALL' && entry.level !== currentLogFilter) return;
      fragment.appendChild(makeLogElement(entry));
  });
  fullLogOutput.appendChild(fragment);
  fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
  logsDomStale = false;
}

// addLogEntries takes a whole batch from the backend and touches the DOM once.
// Entries keep only the raw line and its level: the markup is rebuilt on demand
// in makeLogElement, which halves what a full buffer retains.
function addLogEntries(lines: string[]) {
  if (!Array.isArray(lines) || lines.length === 0) return;

  const wasAtBottom = isScrolledToBottom;
  // Nothing is on screen while the Logs view is closed, so skip the DOM work
  // entirely and rebuild from the array if and when the user opens it.
  const viewOpen = isLogsViewOpen();
  const fragment = viewOpen ? document.createDocumentFragment() : null;
  let appended = 0;

  for (const raw of lines) {
    const text = stripAnsi(String(raw));

    // Фильтруем системный "шум" Windows, который не является ошибкой приложения
    if (text.includes('wsasend: An established connection was aborted')) continue;
    if (text.includes('connection was aborted by the software in your host machine')) continue;

    const entry: LogEntry = { level: parseLogLine(text).level, text };
    logsArray.push(entry);

    if (fragment && (currentLogFilter === 'ALL' || currentLogFilter === entry.level)) {
      fragment.appendChild(makeLogElement(entry));
      appended++;
    }
  }

  if (logsArray.length > MAX_LOG_ENTRIES) {
    logsArray.splice(0, logsArray.length - MAX_LOG_ENTRIES);
  }

  if (!fragment) {
    logsDomStale = true;
  } else if (appended > 0) {
    fullLogOutput.appendChild(fragment);
    const excess = fullLogOutput.childNodes.length - MAX_LOG_ENTRIES;
    for (let i = 0; i < excess && fullLogOutput.firstChild; i++) fullLogOutput.removeChild(fullLogOutput.firstChild);
    if (wasAtBottom) fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
  }
}

// События API
window.api.onLog(addLogEntries);

// The backend signals this once the core is actually up. Previously the UI
// watched the log stream for "sing-box started", which meant the connected
// state silently depended on the core's log level being verbose enough.
window.api.onStarted(() => {
  if (appState !== 'on') updateAppInterface('on');
});

clearLogsBtn.onclick = () => {
  logsArray.length = 0;
  fullLogOutput.innerHTML = '';
  logsDomStale = false;
};

window.api.onStopped(() => {
  if (!isRestarting) {
    finishSessionHistory(); // сохраняем сессию если VPN упал сам
    updateAppInterface('off');
  }
});

window.api.onPingResult((data) => {
  setPingData(data.link, data.latency);
  updateCards();
});

window.api.onTrayToggleConnection(() => powerBtn.click());
window.api.onTrayRestart(() => restartBtn.click());

window.api.onTrayServerSelected((link: string) => {
  activeServerLink = link;
  const info = parseBasicInfo(link);
  activeServerName.textContent = info.name;
  updateCards();
  collectAndSaveSettings();
});

window.api.onTrayStartReconnect((data) => {
  activeServerLink = data.link;
  const info = parseBasicInfo(data.link);
  activeServerName.textContent = info.name;
  updateCards();
  collectAndSaveSettings();
  updateAppInterface('connecting');
  (async () => {
    try {
      // Read useSystemProxy from saved settings to ensure it's current,
      // not stale from the moment the tray menu was built.
      const freshSettings = await window.api.getSettings();
      const useSystemProxy = freshSettings && freshSettings.systemProxy != null
        ? !!freshSettings.systemProxy
        : !!data.useSystemProxy;
      const res = await window.api.startXray(data.link, useSystemProxy);
      if (res && !res.success) {
        showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  })();
});

// Управление соединением
powerBtn.onclick = () => {
  if (appState === 'off') {
    if (!activeServerLink) return showAlert(translations[currentLanguage].alertDialogTitle, translations[currentLanguage].selectServerAlert, false, translations[currentLanguage]);
    updateAppInterface('connecting');
    (async () => {
      try {
        const res = await window.api.startXray(activeServerLink, el<HTMLInputElement>('systemProxyCheckbox').checked);
        if (res && !res.success) {
          showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
          updateAppInterface('off');
        }
      } catch (e) {
        showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
        updateAppInterface('off');
      }
    })();
  } else {
    disconnectBtn.click();
  }
};

disconnectBtn.onclick = () => {
  finishSessionHistory(); // записываем сессию в историю
  updateAppInterface('off');
  window.api.stopXray();
};

// Сброс счётчика трафика и старт записи истории
function startSessionTracking() {
  window.sessionBytesDown = 0;
  window.sessionBytesUp = 0;
  sessionConnectedAt = Date.now();
  el('sessionTotalDown').textContent = '0 B';
  el('sessionTotalUp').textContent = '0 B';
  const speedTotal = optionalEl('speedTotal');
  if (speedTotal) speedTotal.textContent = '0 B';
  const speedometerTotalContainer = optionalEl('speedometerTotalContainer');
  if (speedometerTotalContainer) speedometerTotalContainer.style.display = 'none';
  el('sessionTotalContainer').style.display = 'none'; // покажем после первого обновления

  // Сброс элементов игрового спидометра
  const ratioEl = optionalEl('trafficGameRatio');
  const fillEl = optionalEl('trafficGameProgressFill');
  const totalLimitEl = optionalEl('trafficGameTotalAndLimit');
  if (ratioEl) ratioEl.textContent = '0%';
  if (fillEl) fillEl.style.width = '0%';
  if (totalLimitEl) totalLimitEl.textContent = '0 B / 100 MB';
}

// Форматирование байт в читаемый вид
function formatBytes(bytes: number) {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return (bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 2) + ' ' + sizes[i];
}

// Формат длительности сессии в человекоческий вид
function formatDuration(seconds: number) {
  const t = translations[currentLanguage];
  if (seconds < 60) return `${seconds}${t.durationSec}`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  if (m < 60) return `${m}${t.durationMin} ${s}${t.durationSec}`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  return `${h}${t.durationHour} ${rm}${t.durationMin}`;
}

/**
 * Одна завершившаяся сессия во вкладке «История». Хранится в localStorage, а не
 * у бэкенда: это чисто интерфейсная сводка, credentials в ней нет — кроме link,
 * который нужен для быстрого повторного подключения.
 */
interface HistoryEntry {
  id: string;
  server: string;
  protocol: string;
  address: string;
  link: string | null;
  connectedAt: number;
  disconnectedAt: number;
  durationSec: number;
  bytesDown: number;
  bytesUp: number;
}

// Запись сессии в историю
function finishSessionHistory() {
  if (!sessionConnectedAt) return;
  const now = Date.now();
  const durationSec = Math.round((now - sessionConnectedAt) / 1000);
  if (durationSec < 2) { sessionConnectedAt = null; return; } // игнорируем мгновенные сессии

  const info = activeServerLink ? parseBasicInfo(activeServerLink) : { name: '?', type: '?', address: '?' };
  const entry = {
    id: now.toString(),
    server: info.name,
    protocol: info.type,
    address: info.address,
    link: activeServerLink, // Сохраняем ссылку для быстрого переподключения
    connectedAt: sessionConnectedAt,
    disconnectedAt: now,
    durationSec,
    bytesDown: window.sessionBytesDown,
    bytesUp: window.sessionBytesUp
  };

  const history = loadHistory();
  history.unshift(entry);
  if (history.length > 100) history.pop(); // храним не более 100 записей
  saveHistory(history);

  sessionConnectedAt = null;
  renderHistoryTab();
}

function loadHistory(): HistoryEntry[] {
  try { return JSON.parse(localStorage.getItem('neobox-connection-history') || '[]'); }
  catch { return []; }
}

function saveHistory(history: HistoryEntry[]) {
  localStorage.setItem('neobox-connection-history', JSON.stringify(history));
}

// Поиск ссылки сервера в подписках по метаданным (для обратной совместимости)
function findServerLink(name: string, address: string, protocol: string) {
  if (!allSubscriptions) return null;
  for (const sub of allSubscriptions) {
    if (sub.links) {
      for (const link of sub.links) {
        const info = parseBasicInfo(link);
        if (info && info.address === address && info.type.toLowerCase() === protocol.toLowerCase()) {
          return link;
        }
      }
    }
  }
  return null;
}

// Установка активного сервера и запуск подключения
async function selectAndConnectServer(link: string) {
  if (!link) return;
  const info = parseBasicInfo(link);
  if (!info) return;

  const isNewServer = activeServerLink !== link;
  activeServerLink = link;
  activeServerName.textContent = info.name;
  activeServerDetails.textContent = `${info.type} • ${info.address}`;

  updateCards();
  collectAndSaveSettings();

  // Если VPN выключен — подключаемся, если включен/подключается — перезапускаем
  if (appState === 'off') {
    updateAppInterface('connecting');
    try {
      const res = await window.api.startXray(activeServerLink, el<HTMLInputElement>('systemProxyCheckbox').checked);
      if (res && !res.success) {
        showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  } else {
    restartBtn.click();
  }

  // Переключаем вкладку на Главную
  const homeTab = document.querySelector<HTMLElement>('.nav-item[data-target="view-home"]');
  if (homeTab) {
    homeTab.click();
  }
}

// Статичная разметка иконок. Это константы без интерполяции, поэтому их можно
// безопасно присваивать через innerHTML, в отличие от данных из подписок.
const PLAY_ICON_SVG = `
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
    <polygon points="6 3 20 12 6 21 6 3" fill="currentColor"/>
  </svg>`;

const YOUTUBE_ICON_SVG = `
  <svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M21.58 7.18a3.001 3.001 0 0 0-2.12-2.12C17.59 4.5 12 4.5 12 4.5s-5.59 0-7.46.56A3.001 3.001 0 0 0 2.42 7.18 31.248 31.248 0 0 0 1.8 12c0 1.6.2 3.22.62 4.82a3.001 3.001 0 0 0 2.12 2.12c1.87.56 7.46.56 7.46.56s5.59 0 7.46-.56a3.001 3.001 0 0 0 2.12-2.12c.42-1.6.62-3.22.62-4.82a31.248 31.248 0 0 0-.62-4.82z" fill="#FF0000"/>
    <polygon points="10 9 10 15 15 12" fill="#FFFFFF"/>
  </svg>`;

// Рендер вкладки История
function renderHistoryTab() {
  const t = translations[currentLanguage];
  const history = loadHistory();
  const list = optionalEl('historyList');
  const empty = el('historyEmpty');
  const statsRow = el('historyStatsRow');

  if (!list) return;

  if (history.length === 0) {
    list.innerHTML = '';
    empty.style.display = 'flex';
    statsRow.style.display = 'none';
    return;
  }

  empty.style.display = 'none';
  statsRow.style.display = 'grid';

  // Сводная статистика
  const totalDuration = history.reduce((sum, e) => sum + e.durationSec, 0);
  const totalDown = history.reduce((sum, e) => sum + (e.bytesDown || 0), 0);
  const totalUp = history.reduce((sum, e) => sum + (e.bytesUp || 0), 0);
  el('historyStatSessions').textContent = String(history.length);
  el('historyStatTime').textContent = formatDuration(totalDuration);
  el('historyStatDown').textContent = formatBytes(totalDown);
  el('historyStatUp').textContent = formatBytes(totalUp);

  // Карточки.
  //
  // БЕЗОПАСНОСТЬ: имя сервера, протокол и адрес приходят из ссылки подписки
  // (fragment после #), то есть полностью подконтрольны тому, кто эту подписку
  // раздаёт. Карточки собираются через DOM API с textContent, а не шаблонной
  // строкой в innerHTML: раньше имя вставлялось сырым и в тело, и в атрибут
  // title, где кавычка в имени позволяла дописать произвольные атрибуты.
  const protocolEmoji = { vless: '🟢', vmess: '🟡', trojan: '🔷', ss: '💜', tuic: '🟤', hysteria2: '🔵', hy2: '🔵', hysteria: '🔵', anytls: '🟠', wireguard: '⚪', wg: '⚪', socks: '⚫', socks5: '⚫', http: '⚫' };
  // Ключ ищем через hasOwnProperty: он тоже из ссылки, и protocolEmoji['constructor']
  // иначе вернул бы функцию из прототипа Object вместо эмодзи.
  const emojiFor = (protocol: string) => {
    const key = (protocol || '').toLowerCase();
    return Object.prototype.hasOwnProperty.call(protocolEmoji, key)
      ? (protocolEmoji as Record<string, string>)[key]
      : '🌐';
  };

  list.innerHTML = '';
  const fragment = document.createDocumentFragment();

  history.forEach((entry: HistoryEntry) => {
    const date = new Date(entry.connectedAt);
    const dateStr = date.toLocaleDateString(t.dateLocale, { day: '2-digit', month: '2-digit' });
    const timeStr = date.toLocaleTimeString(t.dateLocale, { hour: '2-digit', minute: '2-digit' });
    const dur = formatDuration(entry.durationSec);
    const down = formatBytes(entry.bytesDown || 0);
    const up = formatBytes(entry.bytesUp || 0);
    const proto = (entry.protocol || '?').toUpperCase();
    const serverName = entry.server || '';

    // Пасхалка: YouTube иконка для серверов с youtube/yt/goog/dpi или случайно ~10% записей
    const isStableEgg = entry.server?.toLowerCase().includes('youtube') ||
                        entry.server?.toLowerCase().includes('yt') ||
                        entry.server?.toLowerCase().includes('goog') ||
                        entry.server?.toLowerCase().includes('dpi') ||
                        (entry.id && entry.id.charCodeAt(0) % 10 === 0);

    const card = document.createElement('div');
    card.className = 'history-card';

    // Иконка запуска
    const icon = document.createElement('div');
    icon.className = isStableEgg ? 'history-card-icon youtube-easter-egg' : 'history-card-icon';
    icon.dataset.id = entry.id;
    icon.title = t.historyConnectTitle;

    const emojiSpan = document.createElement('span');
    emojiSpan.className = 'history-icon-emoji';
    emojiSpan.textContent = emojiFor(entry.protocol);

    const playGeneric = document.createElement('span');
    playGeneric.className = 'history-icon-play generic';
    playGeneric.innerHTML = PLAY_ICON_SVG;

    const playYoutube = document.createElement('span');
    playYoutube.className = 'history-icon-play youtube';
    playYoutube.innerHTML = YOUTUBE_ICON_SVG;

    icon.appendChild(emojiSpan);
    icon.appendChild(playGeneric);
    icon.appendChild(playYoutube);

    // Тело карточки
    const body = document.createElement('div');
    body.className = 'history-card-body';

    const server = document.createElement('div');
    server.className = 'history-card-server';
    // Оба присваивания — свойства элемента, а не разметка: браузер не парсит их как HTML.
    server.title = serverName;
    server.textContent = serverName;

    const meta = document.createElement('div');
    meta.className = 'history-card-meta';
    [`📡 ${proto}`, `⏱ ${dur}`, `📅 ${dateStr} ${timeStr}`].forEach(text => {
      const item = document.createElement('span');
      item.className = 'history-meta-item';
      item.textContent = text;
      meta.appendChild(item);
    });

    body.appendChild(server);
    body.appendChild(meta);

    // Трафик
    const traffic = document.createElement('div');
    traffic.className = 'history-card-traffic';

    const downSpan = document.createElement('span');
    downSpan.className = 'history-traffic-down';
    downSpan.textContent = `↓ ${down}`;

    const upSpan = document.createElement('span');
    upSpan.className = 'history-traffic-up';
    upSpan.textContent = `↑ ${up}`;

    traffic.appendChild(downSpan);
    traffic.appendChild(upSpan);

    card.appendChild(icon);
    card.appendChild(body);
    card.appendChild(traffic);
    fragment.appendChild(card);
  });

  list.appendChild(fragment);

  // Обработчик клика по иконке запуска (через делегирование)
  list.onclick = (e) => {
    const iconBtn = (e.target as HTMLElement).closest('.history-card-icon');
    if (iconBtn) {
      const entryId = iconBtn.getAttribute('data-id');
      const hist = loadHistory();
      const entry = hist.find(item => item.id === entryId);
      if (entry) {
        const link = entry.link || findServerLink(entry.server, entry.address, entry.protocol);
        if (link) {
          selectAndConnectServer(link);
        } else {
          showAlert(translations[currentLanguage].alertDialogTitle, translations[currentLanguage].historyServerNotFound, false, translations[currentLanguage]);
        }
      }
    }
  };

  // Динамический Shift-пасхалка при движении мыши (для мгновенной реакции на зажатый Shift)
  list.onmousemove = (e) => {
    const iconBtn = (e.target as HTMLElement).closest('.history-card-icon');
    if (iconBtn) {
      const entryId = iconBtn.getAttribute('data-id');
      const hist = loadHistory();
      const entry = hist.find(item => item.id === entryId);
      if (entry) {
        const isStableEgg = entry.server?.toLowerCase().includes('youtube') || 
                            entry.server?.toLowerCase().includes('yt') || 
                            entry.server?.toLowerCase().includes('goog') || 
                            entry.server?.toLowerCase().includes('dpi') ||
                            (entry.id && entry.id.charCodeAt(0) % 10 === 0);
        if (e.shiftKey) {
          iconBtn.classList.add('youtube-easter-egg');
        } else if (!isStableEgg) {
          iconBtn.classList.remove('youtube-easter-egg');
        }
      }
    }
  };
}

el('clearHistoryBtn').onclick = () => {
  saveHistory([]);
  renderHistoryTab();
};

// ── DNS Leak Test ────────────────────────────────────────────────────────────
el('dnsLeakBtn').onclick = () => runDnsLeakTest();
el('dnsLeakCloseBtn').onclick = () => {
  el('dnsLeakModalOverlay').style.display = 'none';
};
el('dnsLeakRetryBtn').onclick = () => runDnsLeakTest();

async function runDnsLeakTest() {
  const t = translations[currentLanguage];
  const overlay = el('dnsLeakModalOverlay');
  const loading = el('dnsLeakLoading');
  const result  = el('dnsLeakResult');
  const iconWrap = el('dnsLeakIconWrap');
  const banner   = el('dnsLeakStatusBanner');
  const statusTxt = el('dnsLeakStatusText');
  const statusIcon = el('dnsLeakStatusIcon');
  const ipEl     = el('dnsLeakIp');
  const dnsList  = el('dnsLeakDnsList');
  const retryBtn = el('dnsLeakRetryBtn');

  // Показываем модалку, сбрасываем состояние
  overlay.style.display = 'flex';
  loading.style.display = 'flex';
  result.style.display  = 'none';
  retryBtn.style.display = 'none';
  iconWrap.className = 'dns-leak-icon-wrap';
  banner.className = 'dns-leak-status-banner';

  try {
    // 1. Получаем внешний IP через ipify
    const ipRes = await fetch('https://api.ipify.org?format=json').then(r => r.json());
    const myIp = ipRes.ip || '?';

    // 2. Запрашиваем DNS-сервер через DoH (Cloudflare) — whoami.cloudflare.com возвращает IP DNS-резолвера
    const dohRes = await fetch(
      'https://cloudflare-dns.com/dns-query?name=whoami.cloudflare.com&type=TXT',
      { headers: { 'Accept': 'application/dns-json' } }
    ).then(r => r.json());

    const dnsServers: string[] = [];
    let remoteIp = '';
    let asn = '';
    let country = '';

    if (dohRes.Answer) {
      dohRes.Answer.forEach((ans: { data?: string }) => {
        const val = ans.data?.replace(/"/g, '').trim();
        if (!val) return;

        if (val.includes(':')) {
          const parts = val.split(':');
          const key = parts[0].trim().toLowerCase();
          const value = parts.slice(1).join(':').trim();
          if (key === 'ip' || key === 'remote_ip') {
            remoteIp = value;
          } else if (key === 'asn') {
            asn = value;
          } else if (key === 'country' || key === 'country_code') {
            country = value;
          }
        } else {
          if (!dnsServers.includes(val)) {
            dnsServers.push(val);
          }
        }
      });
    }

    if (remoteIp && !dnsServers.includes(remoteIp)) {
      dnsServers.push(remoteIp);
    }

    // 3. Leak detection: if DNS is going through our VPN (which uses Cloudflare DoH 1.1.1.1),
    //    then the whoami resolver IP should be a Cloudflare anycast address, or the ASN should belong to Cloudflare.
    //    Known Cloudflare resolver prefixes: 1.1.1., 1.0.0., 162.159., 172.64., 108.162., 2606:4700:
    //    If none of the detected resolvers are Cloudflare IPs and the ASN is not Cloudflare's, DNS is leaking.
    const isCloudflareDns = (ip: string) =>
      ip.startsWith('1.1.1.') ||
      ip.startsWith('1.0.0.') ||
      ip.startsWith('162.159.') ||
      ip.startsWith('172.64.') ||
      ip.startsWith('108.162.') ||
      ip.startsWith('2606:4700') ||
      (asn && (asn === '13335' || asn.includes('13335')));
    
    const isLeaking = dnsServers.length > 0 && !dnsServers.every(isCloudflareDns);

    // Отображаем результат
    loading.style.display = 'none';
    result.style.display  = 'block';
    retryBtn.style.display = 'flex';
    ipEl.textContent = myIp;

    // Use textContent to safely render IP addresses (avoid XSS from crafted DNS responses)
    dnsList.innerHTML = '';
    if (dnsServers.length > 0) {
      dnsServers.forEach(ip => {
        const div = document.createElement('div');
        div.className = 'dns-leak-dns-entry';
        
        let displayVal = ip;
        if (ip === remoteIp) {
          const meta = [];
          if (asn) meta.push(`asn: ${asn}`);
          if (country) meta.push(`country_code: ${country}`);
          if (meta.length > 0) {
            displayVal += ` (${meta.join(', ')})`;
          }
        }
        div.textContent = displayVal;
        dnsList.appendChild(div);
      });
    } else {
      const div = document.createElement('div');
      div.className = 'dns-leak-dns-entry';
      div.style.color = 'var(--text-dim)';
      div.textContent = t.dnsLeakUnknown;
      dnsList.appendChild(div);
    }

    if (isLeaking) {
      iconWrap.className = 'dns-leak-icon-wrap leak';
      banner.className   = 'dns-leak-status-banner leak';
      statusIcon.textContent = '⚠️';
      statusTxt.textContent  = t.dnsLeakDetected;
    } else {
      iconWrap.className = 'dns-leak-icon-wrap safe';
      banner.className   = 'dns-leak-status-banner';
      statusIcon.textContent = '✅';
      statusTxt.textContent  = t.dnsLeakSafe;
    }
  } catch (err) {
    loading.style.display = 'none';
    result.style.display  = 'block';
    retryBtn.style.display = 'flex';
    banner.className = 'dns-leak-status-banner leak';
    statusIcon.textContent = '❌';
    statusTxt.textContent  = t.dnsLeakTestError;
    ipEl.textContent = '—';
    // Сообщение об ошибке может содержать данные ответа DoH — рендерим как текст.
    dnsList.innerHTML = '';
    const errDiv = document.createElement('div');
    errDiv.className = 'dns-leak-dns-entry';
    errDiv.style.color = 'var(--danger)';
    errDiv.textContent = errorText(err);
    dnsList.appendChild(errDiv);
  }
}

restartBtn.onclick = () => {
  if (!activeServerLink) return;
  isRestarting = true;
  updateAppInterface('connecting');
  (async () => {
    try {
      const res = await window.api.restartXray(activeServerLink, el<HTMLInputElement>('systemProxyCheckbox').checked);
      if (res && !res.success) {
        showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  })();
};

// Настройки
function collectAndSaveSettings() {
  const settings: AppSettings = {
    language: currentLanguage,
    dns: el<HTMLSelectElement>('dnsSelect').value === 'custom' ? el<HTMLInputElement>('customDnsInput').value : el<HTMLSelectElement>('dnsSelect').value,
    bypassRu: el<HTMLInputElement>('bypassRuCheckbox').checked,
    tunMode: el<HTMLInputElement>('tunModeCheckbox').checked,
    // Обязательно сохранять: на это поле рассчитывают и подключение из трея
    // (tray.go читает settings["systemProxy"]), и авто-выбор лучшего сервера.
    // Пока его здесь не было, оба всегда получали undefined и подключались без
    // системного прокси — при том что галка в интерфейсе стояла.
    systemProxy: el<HTMLInputElement>('systemProxyCheckbox').checked,
    autoConnect: el<HTMLInputElement>('autoConnectCheckbox').checked,
    autoUpdateSubs: el<HTMLInputElement>('autoUpdateSubsCheckbox').checked,
    rememberServer: el<HTMLInputElement>('rememberServerCheckbox').checked,
    openAtLogin: el<HTMLInputElement>('openAtLoginCheckbox').checked,
    startMinimized: el<HTMLInputElement>('startMinimizedCheckbox').checked,
    killSwitch: el<HTMLInputElement>('killSwitchCheckbox').checked,
    dnsLeak: el<HTMLInputElement>('dnsLeakCheckbox').checked,
    ipv6Leak: el<HTMLInputElement>('ipv6LeakCheckbox').checked,
    fakeDns: el<HTMLInputElement>('fakeDnsCheckbox').checked,
    verboseLogging: el<HTMLInputElement>('verboseLoggingCheckbox').checked,
    lastSelectedServer: activeServerLink,
    customDirect: el<HTMLTextAreaElement>('customDirect').value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    processMode: processModeHidden.value as AppSettings['processMode'],
    processListBlacklist: processListBlacklistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    processListWhitelist: processListWhitelistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    favoriteLinks: Array.from(favoriteLinks),
    customRules: customRules
  };
  return window.api.saveSettings(settings);
}

el('saveRoutesBtn').onclick = async () => {
  await collectAndSaveSettings();
  const status = el('routesStatus');
  status.style.display = 'inline';
  setTimeout(() => status.style.display = 'none', 2000);
};

el('saveAppsBtn').onclick = async () => {
  await collectAndSaveSettings();
  const status = el('appsStatus');
  status.style.display = 'inline';
  setTimeout(() => status.style.display = 'none', 2000);
};

el('saveSettingsBtn').onclick = () => {
  collectAndSaveSettings();
  const status = el('settingsStatus');
  status.style.display = 'inline';
  setTimeout(() => status.style.display = 'none', 2000);
};

// Управление окном
async function animateAndAction(action: () => void) {
  document.body.classList.add('window-hidden');
  await new Promise(res => setTimeout(res, 250));
  action();
  // Только после того, как окно действительно ушло: до этого анимация
  // затухания ещё должна проигрываться.
  setUiActive(false);
}

el('minimizeBtn').onclick = () => animateAndAction(() => window.api.minimize());
el('closeBtn').onclick = () => animateAndAction(() => window.api.close());

window.api.onWindowHidden(() => setUiActive(false));

window.api.onWindowRestored(() => {
  document.body.classList.remove('window-hidden');
  setUiActive(true);
});

function showUpdateModal(update: UpdateInfo): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    const overlay = el('updateModalOverlay');
    const versionEl = el('updateModalVersion');
    const changelogEl = el('updateModalChangelog');
    const cancelBtn = el<HTMLButtonElement>('updateModalCancel');
    const confirmBtn = el<HTMLButtonElement>('updateModalConfirm');
    const progressSection = el('updateProgressSection');
    const progressStatus = el('updateProgressStatus');
    const progressPercent = el('updateProgressPercent');
    const progressBar = el('updateProgressBar');
    const actions = el('updateModalActions');
    const t = translations[currentLanguage];

    // Set text contents
    versionEl.textContent = `v${update.version}`;
    changelogEl.textContent = update.body || (currentLanguage === 'RU' ? 'Описание изменений отсутствует.' : 'No changelog description provided.');

    // Reset styles
    progressSection.style.display = 'none';
    progressBar.style.width = '0%';
    progressPercent.textContent = '0%';
    progressStatus.textContent = t.updateProgressStatusDownloading || 'Downloading update...';
    progressStatus.style.color = 'var(--text-dim)';
    
    // Reset buttons visibility and states
    actions.style.display = 'flex';
    cancelBtn.style.display = 'block';
    cancelBtn.disabled = false;
    confirmBtn.style.display = 'block';
    confirmBtn.disabled = false;
    confirmBtn.textContent = t.updateModalConfirm || 'Update Now';

    overlay.style.display = 'flex';

    cancelBtn.onclick = () => {
      overlay.style.display = 'none';
      resolve(false);
    };

    confirmBtn.onclick = async () => {
      // If we don't have downloadUrl for some reason (e.g. GitHub API didn't return a build), fall back to opening webpage
      if (!update.downloadUrl) {
        // Страницу релиза открываем, только если она вообще пришла в ответе:
        // без downloadUrl и без url открывать нечего, но модалку всё равно надо
        // закрыть, иначе она останется висеть на нерабочей кнопке.
        if (update.url) window.api.openUpdateLink(update.url);
        overlay.style.display = 'none';
        resolve(true);
        return;
      }

      // Transition UI to download state
      cancelBtn.style.display = 'none';
      confirmBtn.disabled = true;
      confirmBtn.textContent = currentLanguage === 'RU' ? 'Загрузка...' : 'Downloading...';
      progressSection.style.display = 'block';

      const cleanupEvents: Array<() => void> = [];

      const onProgress = (percent: number) => {
        progressBar.style.width = `${percent}%`;
        progressPercent.textContent = `${percent}%`;
      };

      const onComplete = () => {
        progressBar.style.width = '100%';
        progressPercent.textContent = '100%';
        progressStatus.textContent = t.updateProgressStatusComplete || 'Installing...';
        progressStatus.style.color = 'var(--success)';
        
        cleanupEvents.forEach(dereg => dereg());
        setTimeout(() => {
          overlay.style.display = 'none';
          resolve(true);
        }, 1500);
      };

      const onError = (errMsg: string) => {
        progressStatus.textContent = `${t.updateProgressStatusError || 'Update failed'}: ${errMsg}`;
        progressStatus.style.color = 'var(--danger)';
        
        // Show cancel button again to allow closing or retrying
        cancelBtn.style.display = 'block';
        cancelBtn.disabled = false;
        confirmBtn.style.display = 'none';
        
        cleanupEvents.forEach(dereg => dereg());
      };

      // Subscribe to Wails events via our bridge api
      const unsubProgress = window.api.onUpdateProgress(onProgress);
      const unsubComplete = window.api.onUpdateComplete(onComplete);
      const unsubError = window.api.onUpdateError(onError);
      cleanupEvents.push(unsubProgress, unsubComplete, unsubError);

      try {
        // Trigger download process in background Go service
        await window.api.downloadAndInstallUpdate(update.downloadUrl, update.signatureHex || "");
      } catch (err) {
        onError(errorText(err));
      }
    };
  });
}

// checkForUpdates спрашивает бэкенд о новой версии и, если она есть, показывает
// модальное окно.
//
// Запускается в фоне и намеренно НЕ ожидается инициализацией. Раньше init()
// ждал и сам запрос (до ~10 секунд сетевых таймаутов: GitHub API плюс загрузка
// подписи релиза), и закрытия модалки пользователем — а всё остальное, язык,
// настройки, список серверов, история, автоподключение, стояло за ней в
// очереди. Пользователь видел интерфейс на языке по умолчанию с пустым списком
// серверов, пока не закроет окно обновления.
async function checkForUpdates() {
  try {
    const update = await window.api.checkUpdates();
    if (update && update.available) {
      await showUpdateModal(update);
    }
  } catch (e) { console.error('Update check failed:', e); }
}

// Инициализация
async function init() {
  // The title bar reads the version from the backend rather than carrying its own
  // copy, so it always matches the release the update check compares against.
  try {
    const version = await window.api.getAppVersion();
    if (version) setText('appVersion', `v${version}`);
  } catch (e) { console.error('Version lookup failed:', e); }

  const settings = await window.api.getSettings();
  if (settings) {
    if (settings.language) currentLanguage = settings.language;
    applyLanguage();

    const rememberServer = settings.rememberServer !== undefined ? !!settings.rememberServer : true;
    if (rememberServer && settings.lastSelectedServer) {
       activeServerLink = settings.lastSelectedServer;
       const info = parseBasicInfo(activeServerLink);
       activeServerName.textContent = info.name;
       activeServerDetails.textContent = `${info.type.toUpperCase()} • ${info.address}`;
    }
    
    el<HTMLInputElement>('bypassRuCheckbox').checked = !!settings.bypassRu;
    
    // Load and select the correct DNS server option on startup
    if (settings.dns) {
      const select = optionalEl<HTMLSelectElement>('dnsSelect');
      if (select) {
        let found = false;
        for (let i = 0; i < select.options.length; i++) {
          if (select.options[i].value === settings.dns) {
            select.value = settings.dns;
            found = true;
            break;
          }
        }
        if (!found && settings.dns !== "") {
          select.value = 'custom';
          const customDnsInput = optionalEl<HTMLInputElement>('customDnsInput');
          if (customDnsInput) {
            customDnsInput.value = settings.dns;
            customDnsInput.style.display = 'block';
          }
        }
      }
    }
    el<HTMLInputElement>('tunModeCheckbox').checked = !!settings.tunMode;
    // По умолчанию включён — так же, как атрибут checked в index.html.
    el<HTMLInputElement>('systemProxyCheckbox').checked =
      settings.systemProxy !== undefined ? !!settings.systemProxy : true;
    el<HTMLInputElement>('autoConnectCheckbox').checked = !!settings.autoConnect;
    el<HTMLInputElement>('autoUpdateSubsCheckbox').checked = !!settings.autoUpdateSubs;
    el<HTMLInputElement>('rememberServerCheckbox').checked = rememberServer;
    el<HTMLInputElement>('openAtLoginCheckbox').checked = !!settings.openAtLogin;
    el<HTMLInputElement>('startMinimizedCheckbox').checked = !!settings.startMinimized;
    el<HTMLInputElement>('killSwitchCheckbox').checked = !!settings.killSwitch;
    el<HTMLInputElement>('dnsLeakCheckbox').checked = settings.dnsLeak !== undefined ? !!settings.dnsLeak : true;
    el<HTMLInputElement>('ipv6LeakCheckbox').checked = settings.ipv6Leak !== undefined ? !!settings.ipv6Leak : true;
    el<HTMLInputElement>('fakeDnsCheckbox').checked = settings.fakeDns !== undefined ? !!settings.fakeDns : true;
    el<HTMLInputElement>('verboseLoggingCheckbox').checked = !!settings.verboseLogging;
    if (settings.customDirect) el<HTMLTextAreaElement>('customDirect').value = settings.customDirect.join('\n');
    
    if (settings.processListBlacklist) processListBlacklistEl.value = settings.processListBlacklist.join('\n');
    if (settings.processListWhitelist) processListWhitelistEl.value = settings.processListWhitelist.join('\n');
    
    if (settings.processMode) {
      processModeHidden.value = settings.processMode;
      const targetTab = document.querySelector<HTMLElement>(`.process-tab[data-mode="${settings.processMode}"]`);
      if (targetTab) targetTab.click();
    }
    
    if (settings.autoConnect && activeServerLink) powerBtn.click();

    if (settings.favoriteLinks) {
      favoriteLinks = new Set(settings.favoriteLinks);
    }
    if (settings.customRules) {
      customRules = settings.customRules;
    }
    renderCustomRules();
  } else {
    applyLanguage();
  }
  await loadSubscriptions();
  renderHistoryTab(); // загрузить историю из localStorage при старте

  // Listen to background auto-update events to hot-reload the UI server cards
  window.api.onSubscriptionsUpdated(() => {
    loadSubscriptions();
  });

  // Listen to clipboard import result
  window.api.onSubscriptionResult(async (links) => {
    if (!links || links.length === 0) {
      showAlert(
        translations[currentLanguage].alertDialogTitle,
        currentLanguage === 'RU' ? 'В буфере обмена не найдено подходящих ссылок!' : 'No valid proxy links found in clipboard!',
        false,
        translations[currentLanguage]
      );
      return;
    }
    
    // Find if a clipboard subscription already exists
    let clipSub = allSubscriptions.find(s => s.url === 'clipboard');
    if (clipSub) {
      // Merge links, avoiding duplicates
      const existing = new Set(clipSub.links);
      links.forEach(l => existing.add(l));
      clipSub.links = Array.from(existing);
    } else {
      const name = currentLanguage === 'RU' ? 'Буфер обмена' : 'Clipboard';
      clipSub = {
        id: Date.now().toString(),
        name: name,
        url: 'clipboard',
        links: links
      };
      allSubscriptions.push(clipSub);
    }
    
    await window.api.saveSubscriptions(allSubscriptions);
    await loadSubscriptions();
  });

  // Convert standard select dropdowns to custom glassmorphic dropdowns to prevent WebView2 transparency rendering bugs.
  makeSelectCustom('dnsSelect');
  makeSelectCustom('newRuleAction');
  makeSelectCustom('newRuleType');

  // Последним и без await: интерфейс уже полностью собран и переведён, а окно
  // обновления появится, когда придёт ответ от GitHub, ничего не задерживая.
  void checkForUpdates();
}

// ── CUSTOM ROUTING RULES UI ──────────────────────────────────────────────────
function renderCustomRules() {
  const container = optionalEl('customRulesList');
  if (!container) return;
  container.innerHTML = '';
  
  if (customRules.length === 0) {
    const emptyDiv = document.createElement('div');
    emptyDiv.style.cssText = 'color: var(--text-dim); font-size: 13px; font-style: italic; padding: 8px; text-align: center; border: 1px dashed var(--glass-border); border-radius: 8px;';
    emptyDiv.textContent = currentLanguage === 'RU' ? 'Кастомные правила отсутствуют.' : 'No custom rules added yet.';
    container.appendChild(emptyDiv);
    return;
  }
  
  customRules.forEach((rule, idx) => {
    const row = document.createElement('div');
    row.style.cssText = 'display: flex; justify-content: space-between; align-items: center; background: rgba(255, 255, 255, 0.03); border: 1px solid var(--glass-border); padding: 8px 12px; border-radius: 8px; gap: 8px;';
    
    const infoSpan = document.createElement('span');
    infoSpan.style.cssText = 'font-size: 13px; display: flex; align-items: center; gap: 6px;';
    
    let actionBadge = '';
    if (rule.action === 'direct') actionBadge = '<span style="color:var(--success); font-weight:bold;">🟢 Direct</span>';
    else if (rule.action === 'proxy') actionBadge = '<span style="color:var(--accent-color); font-weight:bold;">🔵 Proxy</span>';
    else if (rule.action === 'block') actionBadge = '<span style="color:var(--danger); font-weight:bold;">🔴 Block</span>';
    
    let typeName: string = rule.type;
    if (rule.type === 'domain_suffix') typeName = 'Suffix';
    else if (rule.type === 'domain') typeName = 'Domain';
    else if (rule.type === 'domain_keyword') typeName = 'Keyword';
    else if (rule.type === 'ip_cidr') typeName = 'IP/CIDR';
    
    // typeName падает обратно на сырой rule.type для неизвестных значений, а
    // settings.json правится вручную — экранируем и его, не только value.
    infoSpan.innerHTML = `${actionBadge} <span style="color:var(--text-dim); font-size:11px;">[${escapeHtml(typeName)}]</span> <strong>${escapeHtml(rule.value)}</strong>`;
    
    const delBtn = document.createElement('button');
    delBtn.className = 'btn-glass';
    delBtn.style.cssText = 'padding: 4px 8px; font-size: 11px; color: var(--danger); border-color: rgba(239, 68, 68, 0.2);';
    delBtn.textContent = currentLanguage === 'RU' ? 'Удалить' : 'Delete';
    
    delBtn.onclick = () => {
      customRules.splice(idx, 1);
      renderCustomRules();
      collectAndSaveSettings();
    };
    
    row.appendChild(infoSpan);
    row.appendChild(delBtn);
    container.appendChild(row);
  });
}

const addCustomRuleBtn = optionalEl('addCustomRuleBtn');
if (addCustomRuleBtn) {
  addCustomRuleBtn.onclick = () => {
    const action = el<HTMLSelectElement>('newRuleAction').value as CustomRule['action'];
    const type = el<HTMLSelectElement>('newRuleType').value as CustomRule['type'];
    const valInput = el<HTMLInputElement>('newRuleValue');
    const value = valInput.value.trim();
    
    if (!value) return;
    
    customRules.push({ action, type, value });
    valInput.value = '';
    renderCustomRules();
    collectAndSaveSettings();
  };
}

const saveRoutesBtn2 = optionalEl('saveRoutesBtn2');
if (saveRoutesBtn2) {
  saveRoutesBtn2.onclick = async () => {
    await collectAndSaveSettings();
    const status = optionalEl('routesStatus2');
    if (status) {
      status.style.display = 'inline';
      setTimeout(() => status.style.display = 'none', 2000);
    }
  };
}

// ── SEARCH LIVE FILTER ───────────────────────────────────────────────────────
const serverSearchInput = optionalEl<HTMLInputElement>('serverSearchInput');
if (serverSearchInput) {
  serverSearchInput.addEventListener('input', (e) => {
    serverSearchQuery = (e.target as HTMLInputElement).value;
    updateCards();
  });
}

// ── AUTO-BEST SERVER ──────────────────────────────────────────────────────────
const bestServerBtn = optionalEl<HTMLButtonElement>('bestServerBtn');
if (bestServerBtn) {
  bestServerBtn.onclick = async () => {
    const links: string[] = [];
    allSubscriptions.forEach(s => links.push(...s.links));
    const uniqueLinks = Array.from(new Set(links));
    if (uniqueLinks.length === 0) return;
    
    // Show pinging status
    uniqueLinks.forEach(l => {
      setPingData(l, 'pinging');
      window.api.pingServer(l);
    });
    updateCards();
    
    // Only the label span is swapped — writing to the button itself would drop
    // the lightning icon that sits next to it.
    const bestServerBtnText = el('bestServerBtnText');
    bestServerBtn.disabled = true;
    const originalText = bestServerBtnText.textContent;
    bestServerBtnText.textContent = currentLanguage === 'RU' ? 'Поиск...' : 'Finding...';

    setTimeout(async () => {
      bestServerBtn.disabled = false;
      bestServerBtnText.textContent = originalText;

      let bestLink = null;
      let minPing = Infinity;
      uniqueLinks.forEach(l => {
        const ping = pingData[l];
        if (typeof ping === 'number' && ping > 0 && ping < minPing) {
          minPing = ping;
          bestLink = l;
        }
      });
      
      if (bestLink) {
        const isNewServer = activeServerLink !== bestLink;
        const wasActive = (appState === 'on' || appState === 'connecting');

        if (wasActive && isNewServer) {
          const info = parseBasicInfo(bestLink);
          const confirmMsg = currentLanguage === 'RU'
            ? `Найден более быстрый сервер: ${info.name}.\nХотите переключиться на него?`
            : `A faster server was found: ${info.name}.\nDo you want to switch to it?`;
          
          const confirmed = await showConfirm(confirmMsg);
          if (!confirmed) {
            return;
          }
        } else if (wasActive && !isNewServer) {
          showAlert(
            translations[currentLanguage].alertDialogTitle,
            currentLanguage === 'RU' 
              ? 'Вы уже подключены к самому быстрому серверу!' 
              : 'You are already connected to the fastest server!',
            false,
            translations[currentLanguage]
          );
          return;
        }

        activeServerLink = bestLink;
        const info = parseBasicInfo(bestLink);
        activeServerName.textContent = info.name;
        activeServerDetails.textContent = `${info.type.toUpperCase()} • ${info.address}`;
        updateCards();
        collectAndSaveSettings();
        
        updateAppInterface('connecting');
        try {
          const freshSettings = await window.api.getSettings();
          const useSystemProxy = freshSettings && freshSettings.systemProxy != null
            ? !!freshSettings.systemProxy
            : el<HTMLInputElement>('systemProxyCheckbox').checked;
          
          let res;
          if (wasActive) {
            res = await window.api.restartXray(bestLink, useSystemProxy);
          } else {
            res = await window.api.startXray(bestLink, useSystemProxy);
          }

          if (res && !res.success) {
            showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
            updateAppInterface('off');
          }
        } catch (e) {
          showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
          updateAppInterface('off');
        }
      } else {
        showAlert(translations[currentLanguage].alertDialogTitle, currentLanguage === 'RU' ? 'Не удалось определить самый быстрый сервер!' : 'Could not determine the fastest server!', false, translations[currentLanguage]);
      }
    }, 2000);
  };
}

// ── SAVE LOGS ────────────────────────────────────────────────────────────────
const saveLogsBtn = optionalEl('saveLogsBtn');
if (saveLogsBtn) {
  saveLogsBtn.onclick = async () => {
    const rawLogs = logsArray.map(l => l.text).join('\n');
    if (!rawLogs.trim()) {
      showAlert(translations[currentLanguage].alertDialogTitle, currentLanguage === 'RU' ? 'Логи пусты!' : 'Logs are empty!', false, translations[currentLanguage]);
      return;
    }
    const path = await window.api.saveLogs(rawLogs);
    if (path) {
      const confirmed = await showConfirm(currentLanguage === 'RU' ? `Логи успешно сохранены в:\n${path}\n\nОткрыть папку с логами в Проводнике?` : `Logs successfully saved to:\n${path}\n\nOpen logs folder in Explorer?`);
      if (confirmed) {
        window.api.openLogsFolder();
      }
    } else {
      showAlert(translations[currentLanguage].errorDialogTitle, currentLanguage === 'RU' ? 'Не удалось сохранить файлы логов!' : 'Failed to save log files!', true, translations[currentLanguage]);
    }
  };
}

// ── WATCHDOG EVENT LISTENERS ──────────────────────────────────────────────────
if (window.api.onWatchdogReconnecting) {
  window.api.onWatchdogReconnecting(() => {
    statusText.textContent = currentLanguage === 'RU' ? 'Авто-переподключение...' : 'Auto-reconnecting...';
    statusText.style.color = 'var(--accent-color)';
    statusDot.className = 'status-dot connecting';
  });
}
if (window.api.onWatchdogReconnected) {
  window.api.onWatchdogReconnected(() => {
    statusText.textContent = currentLanguage === 'RU' ? 'Подключено' : 'Connected';
    statusText.style.color = 'var(--success)';
    statusDot.className = 'status-dot on';
  });
}
if (window.api.onWatchdogWaiting) {
  window.api.onWatchdogWaiting(() => {
    statusText.textContent = currentLanguage === 'RU' ? 'Нет сети — ожидание...' : 'No network — waiting...';
    statusText.style.color = 'var(--accent-color)';
    statusDot.className = 'status-dot connecting';
  });
}
if (window.api.onWatchdogFailed) {
  window.api.onWatchdogFailed((err) => {
    statusText.textContent = currentLanguage === 'RU' ? 'Сбой' : 'Watchdog failed';
    statusText.style.color = 'var(--danger)';
    statusDot.className = 'status-dot error';
    showAlert(translations[currentLanguage].errorDialogTitle, err || 'Watchdog reconnect failed', true, translations[currentLanguage]);
  });
}

// Bring the window to the front when the user clicks anywhere in the application
document.addEventListener('mousedown', () => {
  if (window.api && window.api.bringToFront) {
    window.api.bringToFront();
  }
});

init();

// --- Логика QR-сканера ---
const importQrBtn = el('importQrBtn');
const qrModalOverlay = el('qrModalOverlay');
const qrModalClose = el('qrModalClose');
const qrStartCameraBtn = el('qrStartCameraBtn');
const qrUploadFileBtn = el('qrUploadFileBtn');
const qrFileInput = el<HTMLInputElement>('qrFileInput');
const qrVideo = el<HTMLVideoElement>('qrVideo');
const qrCanvas = el<HTMLCanvasElement>('qrCanvas');
const qrPlaceholder = el('qrScannerPlaceholder');
const qrPlaceholderText = el('qrPlaceholderText');
const qrReticle = el('qrScannerReticle');

let qrStream: MediaStream | null = null;
let qrAnimationId: number | null = null;

function stopQrCamera() {
  if (qrAnimationId) {
    cancelAnimationFrame(qrAnimationId);
    qrAnimationId = null;
  }
  if (qrStream) {
    qrStream.getTracks().forEach(track => track.stop());
    qrStream = null;
  }
  qrVideo.pause();
  qrVideo.srcObject = null;
  qrVideo.style.display = 'none';
  qrReticle.style.display = 'none';
  qrPlaceholder.style.display = 'flex';
}

function closeQrModal() {
  stopQrCamera();
  qrModalOverlay.style.display = 'none';
}

importQrBtn.onclick = () => {
  const t = translations[currentLanguage];
  qrPlaceholderText.textContent = t.qrPlaceholderText;
  qrModalOverlay.style.display = 'flex';
};

qrModalClose.onclick = closeQrModal;

qrUploadFileBtn.onclick = () => {
  stopQrCamera();
  qrFileInput.click();
};

qrFileInput.onchange = (e) => {
  const input = e.target as HTMLInputElement;
  const file = input.files?.[0];
  if (!file) return;

  const t = translations[currentLanguage];
  const reader = new FileReader();
  reader.onload = (event) => {
    const img = new Image();
    img.onload = async () => {
      const tempCanvas = document.createElement('canvas');
      const ctx = tempCanvas.getContext('2d')!;
      tempCanvas.width = img.width;
      tempCanvas.height = img.height;
      ctx.drawImage(img, 0, 0);

      const imageData = ctx.getImageData(0, 0, tempCanvas.width, tempCanvas.height);
      if (typeof jsQR !== 'undefined') {
        const code = jsQR(imageData.data, imageData.width, imageData.height);
        if (code && code.data) {
          await handleQrImport(code.data);
        } else {
          showAlert(t.errorDialogTitle, t.qrNoCodeError, true, t);
        }
      } else {
        console.error("jsQR is not loaded yet");
      }
    };
    img.src = String(event.target?.result ?? '');
  };
  reader.readAsDataURL(file);
  input.value = ''; // Reset file input
};

qrStartCameraBtn.onclick = async () => {
  stopQrCamera();
  const t = translations[currentLanguage];

  try {
    qrStream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: 'environment' }
    });
    qrVideo.srcObject = qrStream;
    qrVideo.setAttribute('playsinline', 'true');
    qrVideo.style.display = 'block';
    qrReticle.style.display = 'block';
    qrPlaceholder.style.display = 'none';
    await qrVideo.play();
    
    qrAnimationId = requestAnimationFrame(scanQrFrame);
  } catch (err) {
    console.error("Camera access failed:", err);
    showAlert(t.errorDialogTitle, t.qrCameraError, true, t);
  }
};

function scanQrFrame() {
  if (qrVideo.readyState === qrVideo.HAVE_ENOUGH_DATA) {
    const canvasCtx = qrCanvas.getContext('2d')!;
    qrCanvas.width = qrVideo.videoWidth;
    qrCanvas.height = qrVideo.videoHeight;
    canvasCtx.drawImage(qrVideo, 0, 0, qrCanvas.width, qrCanvas.height);

    const imageData = canvasCtx.getImageData(0, 0, qrCanvas.width, qrCanvas.height);
    if (typeof jsQR !== 'undefined') {
      const code = jsQR(imageData.data, imageData.width, imageData.height);
      if (code && code.data) {
        handleQrImport(code.data);
        return;
      }
    }
  }
  if (qrStream) {
    qrAnimationId = requestAnimationFrame(scanQrFrame);
  }
}

async function handleQrImport(link: string) {
  const t = translations[currentLanguage];
  const trimmed = link.trim();
  
  // Kept in step with proxySchemes in backend/core/protocol.go. "http" is left
  // out on purpose: a QR code holding a web address must not import as a
  // server, and only the backend can tell the two shapes apart.
  const qrSchemes = [
    'vless', 'vmess', 'ss', 'trojan', 'tuic',
    'hysteria', 'hysteria2', 'hy2', 'anytls',
    'socks', 'socks5', 'wireguard', 'wg',
  ];

  if (qrSchemes.some(scheme => trimmed.toLowerCase().startsWith(`${scheme}://`))) {

    let qrSub = allSubscriptions.find(s => s.url === 'qrcode');
    if (qrSub) {
      const existing = new Set(qrSub.links);
      existing.add(trimmed);
      qrSub.links = Array.from(existing);
    } else {
      const name = currentLanguage === 'RU' ? 'Сканированные QR' : 'Scanned QR';
      qrSub = {
        id: Date.now().toString(),
        name: name,
        url: 'qrcode',
        links: [trimmed]
      };
      allSubscriptions.push(qrSub);
    }

    await window.api.saveSubscriptions(allSubscriptions);
    closeQrModal();
    await loadSubscriptions();
    showAlert(t.alertDialogTitle, t.qrSuccessImport, false, t);
    return;
  }

  // Most QR codes handed out by a provider hold the subscription address, not a
  // single node. Rejecting those was the whole reason scanning "did not work":
  // the code read perfectly and was then thrown away.
  if (/^https?:\/\//i.test(trimmed)) {
    closeQrModal();
    // Name it after the host, which is all the code carries and still tells one
    // provider from another in the tab strip.
    let name = trimmed;
    try {
      name = new URL(trimmed).hostname || trimmed;
    } catch (e) {
      // Not a URL the browser can parse; the raw text still names the tab.
    }
    await addSubscriptionByUrl(name, trimmed);
    return;
  }

  showAlert(t.errorDialogTitle, t.qrUnsupportedContent.replace('{content}', trimmed.slice(0, 120)), true, t);
}

el('pingAllBtn').onclick = () => {
  const links: string[] = [];
  allSubscriptions.forEach(s => links.push(...s.links));
  Array.from(new Set(links)).forEach(l => {
    setPingData(l, 'pinging');
    window.api.pingServer(l);
  });
  updateCards();
};

el<HTMLSelectElement>('dnsSelect').onchange = (e) => {
    const selected = (e.target as HTMLSelectElement).value;
    el<HTMLInputElement>('customDnsInput').style.display = selected === 'custom' ? 'block' : 'none';
};

el<HTMLInputElement>('tunModeCheckbox').onchange = async (e) => {
  if ((e.target as HTMLInputElement).checked) {
    const isAdmin = await window.api.checkAdmin();
    if (!isAdmin) {
      // Save settings with tunMode: true first, so the elevated instance loads it checked
      await collectAndSaveSettings();
      window.api.requestAdmin();
    } else {
      // Auto-save on checking if we are already admin
      collectAndSaveSettings();
    }
  } else {
    // Auto-save on unchecking
    collectAndSaveSettings();
  }
};

// Wails rejects a bound call with the text of the Go error, sometimes wrapped
// in an Error and sometimes as a bare string. The backend already translates
// the message, so it goes straight to the user.
function subscriptionErrorText(e: unknown): string {
  return errorText(e);
}

// Adds a subscription and fetches it in the background, showing the tab with a
// spinner straight away. Shared by the "add" button and the QR import, since a
// QR code carrying a subscription address has to end up in exactly the same
// state as one typed by hand — auto-update included.
async function addSubscriptionByUrl(name: string, url: string) {
  const newSubId = Date.now().toString();
  const newSub = { id: newSubId, name, url, links: [], loading: true };
  allSubscriptions.push(newSub);

  await window.api.saveSubscriptions(allSubscriptions);
  await loadSubscriptions(); // Renders the tab instantly with hourglass spinner

  // Background fetch
  (async () => {
    try {
      const links = await window.api.fetchSubscription(url);
      const sub = allSubscriptions.find(s => s.id === newSubId);
      if (sub) {
        sub.links = links || [];
        sub.loading = false;
        await window.api.saveSubscriptions(allSubscriptions);
        await loadSubscriptions(); // Refresh tabs to remove hourglass spinner
        if (currentActiveSubId === newSubId || currentActiveSubId === 'all') {
          updateCards();
        }
      }
    } catch (e) {
      console.error('Failed to fetch subscription:', e);
      const sub = allSubscriptions.find(s => s.id === newSubId);
      if (sub) {
        sub.loading = false;
        await window.api.saveSubscriptions(allSubscriptions);
        await loadSubscriptions();
      }
      const t = translations[currentLanguage];
      showAlert(t.errorDialogTitle, subscriptionErrorText(e), true, t);
    }
  })();
}

el('addSubBtn').onclick = async () => {
  const nameInput = el<HTMLInputElement>('subName');
  const urlInput = el<HTMLInputElement>('subUrl');
  const name = nameInput.value.trim();
  const url = urlInput.value.trim();
  if (!name || !url) return;

  nameInput.value = '';
  urlInput.value = '';

  await addSubscriptionByUrl(name, url);
};

el('importClipboardBtn').onclick = () => {
  window.api.importFromClipboard();
};

el('updateSubBtn').onclick = async () => {
  const t = translations[currentLanguage];
  const originalText = el('updateSubBtn').textContent;
  el('updateSubBtn').textContent = currentLanguage === 'RU' ? 'Обновление...' : 'Updating...';
  
  if (currentActiveSubId === 'all') {
    // Update all subscriptions. Failures are collected rather than reported one
    // by one, so a dead subscription cannot bury the user in dialogs.
    const failures = [];
    for (const sub of allSubscriptions) {
      try {
        const links = await window.api.fetchSubscription(sub.url);
        if (links && links.length > 0) {
          sub.links = links;
        }
      } catch (e) {
        console.error('Failed to update subscription:', sub.name, e);
        failures.push(`${sub.name}: ${subscriptionErrorText(e)}`);
      }
    }
    await window.api.saveSubscriptions(allSubscriptions);
    await loadSubscriptions();
    if (failures.length > 0) {
      showAlert(t.errorDialogTitle, failures.join('\n\n'), true, t);
    }
  } else {
    // Update the selected active subscription
    const sub = allSubscriptions.find(s => s.id === currentActiveSubId);
    if (sub) {
      try {
        const links = await window.api.fetchSubscription(sub.url);
        if (links && links.length > 0) {
          sub.links = links;
          await window.api.saveSubscriptions(allSubscriptions);
          await loadSubscriptions();
        }
      } catch (e) {
        console.error('Failed to update subscription:', sub.name, e);
        showAlert(t.errorDialogTitle, subscriptionErrorText(e), true, t);
      }
    }
  }
  el('updateSubBtn').textContent = originalText;
};

// --- Вспомогательная функция дебаунса для автосохранения ---
function debounce<A extends unknown[]>(func: (...args: A) => void, wait: number) {
  let timeout: ReturnType<typeof setTimeout>;
  return function executedFunction(...args: A) {
    const later = () => {
      clearTimeout(timeout);
      func(...args);
    };
    clearTimeout(timeout);
    timeout = setTimeout(later, wait);
  };
}

// --- Автосохранение при вводе в текстовые поля на лету ---
el<HTMLTextAreaElement>('customDirect').oninput = debounce(() => {
  collectAndSaveSettings();
}, 500);

el<HTMLInputElement>('bypassRuCheckbox').onchange = () => {
  collectAndSaveSettings();
};

el<HTMLInputElement>('systemProxyCheckbox').onchange = () => {
  collectAndSaveSettings();
};

processListBlacklistEl.oninput = debounce(() => {
  collectAndSaveSettings();
}, 500);

processListWhitelistEl.oninput = debounce(() => {
  collectAndSaveSettings();
}, 500);

// --- Специальный интерактивный виджет прокрутки ---
let isDraggingScroll = false;
let startScrollY = 0;
let startScrollTop = 0;

const scrollWidget = el('serversScrollWidget');
const scrollTrack = el('scrollTrack');
const scrollThumb = el('scrollThumb');
const scrollUpBtn = el('scrollUpBtn');
const scrollDownBtn = el('scrollDownBtn');
const mainContainer = query('main');

function updateCustomScroll() {
  if (!scrollWidget || !mainContainer || !scrollTrack || !scrollThumb) return;

  const scrollHeight = mainContainer.scrollHeight;
  const clientHeight = mainContainer.clientHeight;
  const scrollTop = mainContainer.scrollTop;

  // Если весь контент помещается на экране, скрываем бегунок и трек, но оставляем область наведения активной
  if (scrollHeight <= clientHeight) {
    scrollThumb.style.display = 'none';
    scrollTrack.style.opacity = '0';
    return;
  }

  scrollThumb.style.display = 'block';
  scrollTrack.style.opacity = '';

  const trackHeight = scrollTrack.clientHeight;
  // Высота бегунка пропорциональна видимой области
  const thumbHeight = Math.max(40, Math.min(150, (clientHeight / scrollHeight) * trackHeight));
  scrollThumb.style.height = `${thumbHeight}px`;

  // Положение бегунка на треке
  const maxScrollTop = scrollHeight - clientHeight;
  const scrollRatio = scrollTop / maxScrollTop;
  const maxThumbTop = trackHeight - thumbHeight;
  const thumbTop = scrollRatio * maxThumbTop;

  scrollThumb.style.transform = `translateY(${thumbTop}px)`;
}

// Слушатель события прокрутки основного контейнера
mainContainer.addEventListener('scroll', updateCustomScroll);
window.addEventListener('resize', updateCustomScroll);

// Перетаскивание бегунка
scrollThumb.addEventListener('mousedown', (e) => {
  isDraggingScroll = true;
  startScrollY = e.clientY;
  startScrollTop = mainContainer.scrollTop;
  scrollThumb.style.transition = 'none'; // Отключаем переходы во время перетаскивания
  document.body.style.cursor = 'grabbing';
  document.body.style.userSelect = 'none'; // Предотвращаем выделение текста
  e.preventDefault();
});

document.addEventListener('mousemove', (e) => {
  if (!isDraggingScroll) return;

  const trackHeight = scrollTrack.clientHeight;
  const thumbHeight = scrollThumb.clientHeight;
  const maxThumbTop = trackHeight - thumbHeight;

  const deltaY = e.clientY - startScrollY;
  const scrollHeight = mainContainer.scrollHeight;
  const clientHeight = mainContainer.clientHeight;
  const maxScrollTop = scrollHeight - clientHeight;

  // Рассчитываем новое положение прокрутки на основе дельты мыши
  const scrollDelta = (deltaY / maxThumbTop) * maxScrollTop;
  mainContainer.scrollTop = Math.max(0, Math.min(maxScrollTop, startScrollTop + scrollDelta));
});

document.addEventListener('mouseup', () => {
  if (isDraggingScroll) {
    isDraggingScroll = false;
    scrollThumb.style.transition = '';
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
  }
});

// Клик по треку скролла (быстрый переход к позиции)
scrollTrack.addEventListener('click', (e) => {
  if (e.target === scrollThumb) return; // Игнорируем клик по самому бегунку

  const trackRect = scrollTrack.getBoundingClientRect();
  const clickY = e.clientY - trackRect.top;
  const thumbHeight = scrollThumb.clientHeight;
  const trackHeight = scrollTrack.clientHeight;

  // Центрируем бегунок по клику
  const targetRatio = (clickY - thumbHeight / 2) / (trackHeight - thumbHeight);
  const scrollHeight = mainContainer.scrollHeight;
  const clientHeight = mainContainer.clientHeight;
  const maxScrollTop = scrollHeight - clientHeight;

  mainContainer.scrollTo({
    top: Math.max(0, Math.min(maxScrollTop, targetRatio * maxScrollTop)),
    behavior: 'smooth'
  });
});

// Стрелочка ВВЕРХ
scrollUpBtn.addEventListener('click', () => {
  mainContainer.scrollBy({ top: -200, behavior: 'smooth' });
});

// Стрелочка ВНИЗ
scrollDownBtn.addEventListener('click', () => {
  mainContainer.scrollBy({ top: 200, behavior: 'smooth' });
});

// Дополнительно: обновляем при изменении сетки серверов
if (serversGrid) {
  const observer = new MutationObserver(() => {
    setTimeout(updateCustomScroll, 50);
  });
  observer.observe(serversGrid, { childList: true, subtree: true });
}

// Function to convert native <select> elements to custom glassmorphic dropdowns
function makeSelectCustom(selectId: string) {
  const select = optionalEl<HTMLSelectElement>(selectId);
  if (!select) return;

  // If already customized, skip but update trigger text
  if (select.nextElementSibling && select.nextElementSibling.classList.contains('custom-select-wrapper')) {
    const wrapper = select.nextElementSibling;
    const trigger = wrapper.querySelector('.custom-select-trigger');
    const selectedOption = select.options[select.selectedIndex];
    if (trigger && selectedOption) {
      trigger.textContent = selectedOption.textContent;
    }
    return;
  }

  // Create wrapper
  const wrapper = document.createElement('div');
  wrapper.className = 'custom-select-wrapper';

  // Hide the original select
  select.style.display = 'none';
  select.parentNode?.insertBefore(wrapper, select.nextSibling);

  // Create trigger
  const trigger = document.createElement('div');
  trigger.className = 'custom-select-trigger';
  const selectedOption = select.options[select.selectedIndex];
  trigger.textContent = selectedOption ? selectedOption.textContent : '';
  wrapper.appendChild(trigger);

  // Create options container
  const optionsContainer = document.createElement('div');
  optionsContainer.className = 'custom-select-options';
  wrapper.appendChild(optionsContainer);

  // Function to build options list dynamically
  const buildOptions = () => {
    optionsContainer.innerHTML = '';
    Array.from(select.options).forEach((opt, idx) => {
      const optDiv = document.createElement('div');
      optDiv.className = 'custom-select-option';
      if (opt.value === select.value) {
        optDiv.classList.add('selected');
      }
      optDiv.textContent = opt.textContent;
      optDiv.onclick = (e) => {
        e.stopPropagation();
        select.value = opt.value;
        trigger.textContent = opt.textContent;
        // Trigger native change event
        select.dispatchEvent(new Event('change'));
        wrapper.classList.remove('open');
        buildOptions(); // Rebuild to update selected class styling
      };
      optionsContainer.appendChild(optDiv);
    });
  };

  buildOptions();

  // Toggle dropdown visibility
  trigger.onclick = (e) => {
    e.stopPropagation();
    // Close all other custom selects first to prevent overlapping menus
    document.querySelectorAll('.custom-select-wrapper').forEach(w => {
      if (w !== wrapper) w.classList.remove('open');
    });
    wrapper.classList.toggle('open');
  };

  // Close dropdown if user clicks anywhere else in the document
  document.addEventListener('click', () => {
    wrapper.classList.remove('open');
  });

  // Re-sync values when the original select is changed externally (e.g. settings loaded)
  select.addEventListener('change', () => {
    const selected = select.options[select.selectedIndex];
    if (selected) {
      trigger.textContent = selected.textContent;
    }
    // Update selected class styling
    Array.from(optionsContainer.children).forEach((child, idx) => {
      if (select.options[idx] && select.options[idx].value === select.value) {
        child.classList.add('selected');
      } else {
        child.classList.remove('selected');
      }
    });
  });
}
