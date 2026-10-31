import { el, optionalEl, query, setText, setLabel, setAria } from './modules/dom';
import { translations } from './modules/translations';
import type { Language, Translations } from './modules/translations';
import type {
  AppSettings,
  SessionTraffic,
  UpdateInfo,
} from './modules/api';
import { fetchIP, isDialogOpen, showConfirm, showAlert, trapFocus, errorText } from './modules/ui-utils';
import { allSubscriptions } from './modules/subscription-manager';
import { parseBasicInfo, pingData, pingDisplay } from './modules/server-manager';
import {
  initKillSwitchView,
  refreshKillSwitchStatus,
  refreshKillSwitchBadge,
  updateInterceptionGuidance,
  isKillSwitchArmed,
} from './views/kill-switch.view';
import {
  initServersView,
  applyServersLanguage,
  updateCards,
  getFavoriteLinks,
  setFavoriteLinks,
  handlePingResult,
} from './views/servers.view';
import {
  initSubscriptionsView,
  applySubscriptionsLanguage,
  loadSubscriptions,
} from './views/subscriptions.view';
import { initScrollWidget, updateCustomScroll } from './modules/scroll-widget';
import { makeSelectCustom, syncCustomSelectLabels } from './modules/dom';
import {
  DEFAULT_THEME,
  applyTheme,
  parseTheme,
} from './modules/theme';
import type { ThemeSpec } from './modules/theme';
import {
  initRoutingView,
  applyRoutingLanguage,
  getCustomRules,
  setCustomRules,
  upsertCustomRule,
  markRulesPending,
  clearRulesPending,
  renderRulesPending,
  getProcessMode,
  setProcessMode,
  getProcessListBlacklist,
  setProcessListBlacklist,
  getProcessListWhitelist,
  setProcessListWhitelist,
} from './views/routing.view';
import {
  initDnsLeakView,
  applyDnsLeakLanguage,
} from './views/dns-leak.view';
import {
  initHistoryView,
  applyHistoryLanguage,
  initHistory,
  renderHistoryTab,
  addSessionHistory,
} from './views/history.view';
import {
  initProfilesView,
  applyProfilesLanguage,
  getProfiles,
  setProfiles,
} from './views/profiles.view';
import {
  initSettingsView,
  applySettingsLanguage,
  setTheme,
  getCurrentTheme,
  renderThemeControls,
  renderThemeReport,
  currentDnsSetting,
  setDnsSetting,
  getAppliedDns,
  setAppliedDns,
  readMirroredTheme,
} from './views/settings.view';
import { initLogsView, onLogsTabActivated, applyLogsLanguage } from './views/logs.view';
import {
  initConnectionsView,
  syncConnectionsPolling,
  applyConnectionsLanguage,
  setConnectionsView,
  getConnectionsView,
  flashConnectionsStatus,
} from './views/connections.view';

// ── ТЕМА: раннее применение ──────────────────────────────────────────────────
//
// Здесь, до всего остального, потому что настройки приезжают из Go асинхронно,
// а первый кадр рисуется раньше ответа. Без этой строки человек, выбравший свою
// тему, каждый запуск видел бы вспышку стандартной палитры.
//
// Зеркало в localStorage — только ради этой синхронности. Источник истины
// остаётся в settings.json: он переживает очистку данных WebView2 и уезжает с
// экспортом настроек, чего про localStorage сказать нельзя.
let currentTheme: ThemeSpec = readMirroredTheme() ?? DEFAULT_THEME;
applyTheme(currentTheme);

// Элементы DOM
const powerBtn = el('powerBtn');
const restartBtn = el('restartBtn');
const statusDot = el('statusDot');
const statusText = el('statusText');
const currentIp = el('currentIp');
const activeServerName = el('activeServerName');
const activeServerDetails = el('activeServerDetails');

/** Состояние соединения, каким его показывает интерфейс. */
type AppState = 'off' | 'connecting' | 'on';

// Состояние
let activeServerLink: string | null = null;
let currentLanguage: Language = 'RU';
let isRestarting = false;
let appState: AppState = 'off';
let tunStatusInterval: ReturnType<typeof setInterval> | null = null;

function updateActiveServerPing(): void {
  const pingEl = optionalEl('activeServerPing');
  if (!pingEl) return;
  if (!activeServerLink) {
    pingEl.style.display = 'none';
    return;
  }
  const pingVal = pingData[activeServerLink];
  if (pingVal === undefined || pingVal === null) {
    pingEl.style.display = 'none';
    return;
  }
  const { text, color } = pingDisplay(pingVal);
  pingEl.style.display = 'inline-flex';
  pingEl.textContent = text;
  pingEl.style.color = color;
}

const activeServerCard = optionalEl('activeServerCard');
if (activeServerCard) {
  const openServersTab = () => {
    const serversTab = document.querySelector<HTMLElement>('.nav-item[data-target="view-servers"]');
    if (serversTab) serversTab.click();
  };
  activeServerCard.addEventListener('click', openServersTab);
  activeServerCard.addEventListener('keydown', (e) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      openServersTab();
    }
  });
}

let sessionConnectedAt: number | null = null; // timestamp начала сессии
let sessionTimerInterval: ReturnType<typeof setInterval> | null = null;
let sessionTimerStart = 0; // отсчитываем от метки, а не от числа тиков
// false, пока окно свёрнуто в трей: там UI никто не видит, а каждая его
// «живая» деталь держит WebView2 в работе. См. setUiActive.
let uiActive = true;

// Что о видимости окна думает Go — вопрос отдельный от uiActive, и раньше на
// оба отвечала одна переменная. Расплата: от windowVisible на стороне Go
// зависит, шлёт ли он вообще события 'traffic-stats' и строки журнала
// (backend/service/vpn.go). Стоило этому представлению разойтись с
// действительностью — счётчик трафика замирал навсегда, а интерфейс при этом
// оставался живым, и связать одно с другим было невозможно.
//
// Начальное значение берётся из hasFocus, а не ставится константой: окно могло
// стартовать свёрнутым (main.go зовёт SetWindowVisible(false) при
// startMinimized), и тогда первый же фокус обязан бэкенд разбудить. Если же
// окно открылось нормально и уже с фокусом, бэкенд и так прав — будить некого.
let backendThinksHidden = !document.hasFocus();

// Навигация
//
// Выборка идёт по [role="tab"], а не по .nav-item. Разница не косметическая:
// класс .nav-item носит и переключатель языка, поэтому обработчик ниже
// срабатывал и на нём — снимал .active со всех вкладок и со всех разделов, а
// восстановить их было некому. Клик по RU/EN гасил содержимое окна целиком.
const navTabs = Array.from(document.querySelectorAll<HTMLElement>('[role="tab"]'));
const views = document.querySelectorAll<HTMLElement>('.view');

// Инициализируем активную вкладку для кастомных стилей
document.body.setAttribute('data-active-tab', 'view-home');

function selectTab(item: HTMLElement, moveFocus = false): void {
  // Роving tabindex: в цепочку Tab попадает только выбранная вкладка, между
  // остальными переходят стрелками. Так рейл занимает одну остановку Tab, а не
  // семь подряд.
  navTabs.forEach(i => {
    i.classList.remove('active');
    i.setAttribute('aria-selected', 'false');
    i.tabIndex = -1;
  });
  views.forEach(v => v.classList.remove('active'));

  item.classList.add('active');
  item.setAttribute('aria-selected', 'true');
  item.tabIndex = 0;
  if (moveFocus) item.focus();

  const targetId = item.getAttribute('data-target') ?? '';
  const targetView = optionalEl(targetId);
  if (targetView) targetView.classList.add('active');

  // Устанавливаем атрибут активной вкладки для кастомных стилей и виджета скроллбара
  document.body.setAttribute('data-active-tab', targetId);
  if (targetId === 'view-servers') {
    setTimeout(updateCustomScroll, 50);
  }
  if (targetId === 'view-logs') onLogsTabActivated();

  // Опрос соединений идёт только пока открыт их экран: это единственное, что
  // делает его бесплатным для всех остальных вкладок.
  connectionsViewActive = targetId === 'view-connections';
  syncConnectionsPolling();
}

navTabs.forEach((item, index) => {
  item.addEventListener('click', () => selectTab(item));

  item.addEventListener('keydown', (e: KeyboardEvent) => {
    let next: number | null = null;
    if (e.key === 'ArrowDown' || e.key === 'ArrowRight') next = (index + 1) % navTabs.length;
    else if (e.key === 'ArrowUp' || e.key === 'ArrowLeft') next = (index - 1 + navTabs.length) % navTabs.length;
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = navTabs.length - 1;
    if (next === null) return;
    e.preventDefault();
    selectTab(navTabs[next], true);
  });
});

// ── Горячие клавиши приложения ───────────────────────────────────────────────
//
// Приложение живёт в трее и открывается ради одного действия, поэтому путь
// «мышь до иконки в рейле» — самая частая трата движений, которая тут вообще
// бывает. Ctrl+1..7 повторяют порядок иконок сверху вниз, тот же, что в
// PRODUCT.md: Главная, Серверы, История, Маршруты, Соединения, Настройки, Логи.
//
// Ctrl, а не Alt: Alt в Windows принадлежит меню окна, а голые цифры отобрали
// бы ввод у полей — в «Маршрутах» и «Настройках» их достаточно.
//
// Сочетания намеренно не перехватываются, когда:
//   • открыт модальный диалог — он и есть весь интерфейс, пока висит;
//   • фокус в поле ввода и нажато Ctrl+K — иначе из поиска нельзя было бы
//     выделить всё; впрочем, Ctrl+K в полях Windows ничего не значит, поэтому
//     ограничение снято, а вот Ctrl+Enter в textarea осмысленно и сохранён за
//     полем;
//   • нажат repeat — удержание не должно листать вкладки.
function handleAppShortcut(e: KeyboardEvent): void {
  if (!e.ctrlKey || e.altKey || e.metaKey || e.repeat) return;
  if (isDialogOpen()) return;

  // Ctrl+1..7 — вкладки в порядке рейла.
  if (e.key >= '1' && e.key <= '7') {
    const target = navTabs[Number(e.key) - 1];
    if (!target) return;
    e.preventDefault();
    selectTab(target);
    return;
  }

  const key = e.key.toLowerCase();

  // Ctrl+K — поиск по серверам. Вкладка переключается заодно: искать сервер,
  // находясь на «Логах», — это всё равно намерение уйти на «Серверы».
  if (key === 'k') {
    e.preventDefault();
    const serversTab = navTabs.find(t => t.getAttribute('data-target') === 'view-servers');
    if (serversTab) selectTab(serversTab);
    const search = optionalEl<HTMLInputElement>('serverSearchInput');
    search?.focus();
    search?.select();
    return;
  }

  // Ctrl+Enter — подключиться или отключиться. Через click(), а не вызовом
  // startConnection: у кнопки на обработчике висит вся логика состояний, и
  // второй вход в неё разошёлся бы с ней при первой же правке.
  if (e.key === 'Enter') {
    const active = document.activeElement;
    // В многострочном поле Ctrl+Enter — привычный «применить», и отбирать его
    // у «Маршрутов» ради подключения было бы обменом не в пользу пользователя.
    if (active instanceof HTMLTextAreaElement) return;
    e.preventDefault();
    powerBtn.click();
  }
}

document.addEventListener('keydown', handleAppShortcut);

// ── Системные горячие клавиши ────────────────────────────────────────────────
//
// В отличие от сочетаний выше эти работают, когда окно закрыто и фокус в чужом
// приложении, — ради того и заведены: приложение живёт в трее, и путь «найти
// иконку и попасть по ней мышью» дороже самого подключения.
//
// Регистрирует их Windows, а не страница, поэтому включение может не удаться:
// сочетание — общесистемный ресурс, и второму желающему отказывают. Отказ
// показывается и галка снимается обратно. Стоящая, но не работающая галка —
// худший из возможных исходов: человек будет уверен, что клавиши есть.
function syncHotkeysDisabled(): void {
  const checkbox = optionalEl<HTMLInputElement>('globalHotkeysCheckbox');
  if (!checkbox) return;
  const isEnabled = checkbox.checked;
  const row = query('.hotkey-row');
  const toggleInput = optionalEl<HTMLInputElement>('hotkeyToggleInput');
  const showInput = optionalEl<HTMLInputElement>('hotkeyShowInput');
  if (row) row.classList.toggle('hotkey-row--disabled', !isEnabled);
  if (toggleInput) toggleInput.disabled = !isEnabled;
  if (showInput) showInput.disabled = !isEnabled;
}

async function applyGlobalHotkeys(enable: boolean, announce: boolean): Promise<void> {
  const failure = await window.api.setGlobalHotkeys(
    enable,
    el<HTMLInputElement>('hotkeyToggleInput').value,
    el<HTMLInputElement>('hotkeyShowInput').value,
  );
  if (!failure) {
    syncHotkeysDisabled();
    return;
  }

  const t = translations[currentLanguage];
  el<HTMLInputElement>('globalHotkeysCheckbox').checked = false;
  syncHotkeysDisabled();
  // При восстановлении настройки на старте показывать диалог поверх ещё не
  // собранного интерфейса незачем — там достаточно снятой галки.
  if (announce) {
    showAlert(t.errorDialogTitle, t.globalHotkeysFailed.replace('{reason}', failure), false, t);
  }
  collectAndSaveSettings();
}

el<HTMLInputElement>('globalHotkeysCheckbox').addEventListener('change', (e) => {
  syncHotkeysDisabled();
  collectAndSaveSettings();
  void applyGlobalHotkeys((e.target as HTMLInputElement).checked, true);
});

// Сочетание не печатают, его нажимают: поле ловит keydown и записывает то, что
// было нажато. Голые модификаторы пропускаются — Ctrl сам по себе сочетанием не
// является, а без них его не примет уже бэкенд.
//
// Проверять состав здесь второй раз незачем: parseHotkey в
// backend/service/hotkey.go — та же проверка, и именно её ответ решает, будет
// работать клавиша или нет. Своя копия правил разошлась бы с ней при первой же
// правке.
const modifierKeys = new Set(['Control', 'Shift', 'Alt', 'Meta']);

function bindHotkeyInput(id: string) {
  const input = el<HTMLInputElement>(id);
  input.addEventListener('keydown', (e: KeyboardEvent) => {
    if (modifierKeys.has(e.key)) return;

    const parts: string[] = [];
    if (e.ctrlKey) parts.push('Ctrl');
    if (e.shiftKey) parts.push('Shift');
    if (e.altKey) parts.push('Alt');
    if (e.metaKey) parts.push('Win');
    // e.code, а не e.key: на кириллической раскладке e.key для той же клавиши
    // придёт как «В», а RegisterHotKey адресуется физической клавишей.
    const key = /^Key[A-Z]$/.test(e.code) ? e.code.slice(3)
      : /^Digit[0-9]$/.test(e.code) ? e.code.slice(5)
      : /^F([1-9]|1[0-2])$/.test(e.code) ? e.code
      : '';
    // preventDefault только здесь, когда сочетание принято. Раньше он стоял
    // первой строкой обработчика, до разбора, и гасил в том числе Tab и
    // Escape: клавиатурный пользователь входил в поле и не мог из него выйти
    // ничем, кроме мыши. Это ловушка по WCAG 2.1.2, а продукт обязался
    // держать AA.
    if (!key) return;
    e.preventDefault();

    parts.push(key);
    input.value = parts.join('+');
    input.blur();
    collectAndSaveSettings();
    // Перерегистрация нужна сразу же — иначе останется работать прежнее
    // сочетание, а в поле будет написано новое.
    void applyGlobalHotkeys(el<HTMLInputElement>('globalHotkeysCheckbox').checked, true);
  });
}

bindHotkeyInput('hotkeyToggleInput');
bindHotkeyInput('hotkeyShowInput');

window.api.onHotkeyToggleConnection(() => powerBtn.click());
window.api.onHotkeyShowWindow(() => void window.api.bringToFront());

// Subscription panel and card rendering extracted to views/subscriptions.view.ts and views/servers.view.ts

// applyLanguage touches a lot of nodes; these guarded helpers keep it readable and
// tolerate elements that only exist in some states of the UI.
// DOM helpers (setText, setTitle, setPlaceholder, setAria, setLabel, escapeHtml) imported from ./modules/dom

function applyLanguage() {
  const t = translations[currentLanguage];

  // Язык документа обязан следовать за переключателем: иначе синтезатор речи
  // читает английские подписи по русским правилам произношения (WCAG 3.1.1).
  document.documentElement.lang = currentLanguage.toLowerCase();

  const navNames: Record<string, string> = {
    'view-home': t.home,
    'view-servers': t.servers,
    'view-history': t.history,
    'view-routes': t.routes,
    'view-connections': t.connections,
    'view-settings': t.settings,
    'view-logs': t.logs,
  };
  document.querySelectorAll<HTMLElement>('[role="tab"]').forEach((item, index) => {
    const name = navNames[item.getAttribute('data-target') ?? ''];
    if (!name) return;
    // Сочетание — во всплывающей подсказке, а не в доступном имени: сокращение
    // «Ctrl+3» рядом с названием вкладки экранный диктор произносил бы при
    // каждом переходе, а пользы от этого нет — клавиши всё равно нажимает тот,
    // кто их уже знает.
    item.title = `${name} (Ctrl+${index + 1})`;
    item.setAttribute('aria-label', name);
  });
  const navRail = document.querySelector('.nav-rail');
  if (navRail) navRail.setAttribute('aria-label', t.navLandmark);

  el('langToggle').textContent = currentLanguage;
  setLabel('langToggle', t.langToggleLabel);

  // Window controls and the servers-tab scrollbar widget are icon-only, so their
  // tooltip is the only text they ever show.
  setLabel('minimizeBtn', t.minimizeBtnTitle);
  setLabel('closeBtn', t.closeBtnTitle);
  setLabel('scrollUpBtn', t.scrollUpTitle);
  setLabel('scrollDownBtn', t.scrollDownTitle);

  // Скрытый заголовок «Главной»: на экране его роль играет панель состояния,
  // но без него уровни заголовков идут с разрывом.
  setText('homeViewHeading', t.homeViewHeading);

  setAria('serversGrid', t.serverListLabel);
  setAria('sortMenu', t.sortMenuLabel);
  const processTabsGroup = document.querySelector('.process-mode-tabs');
  if (processTabsGroup) processTabsGroup.setAttribute('aria-label', t.processModeLabel);

  updateAppInterface(appState);
  
  // Заглушки переводятся, а настоящие значения — нет.
  //
  // Поле IP держит либо адрес, либо одну из заглушек; имя сервера — либо
  // выбранный сервер, либо «не выбран». При смене языка заглушку надо
  // перевести, а адрес и имя сервера трогать нельзя. Отличить одно от другого
  // можно только сравнив с тем, что сейчас на экране.
  //
  // Раньше сравнение шло со списком литералов на обоих языках, выписанным
  // прямо здесь. Список успел устареть: 'Определяю...' не встречается больше
  // нигде, а 'Обновление...' в поле IP не появлялось никогда. Теперь сравнение
  // идёт с самой таблицей, поэтому разойтись со строками оно не может.
  const showsPlaceholder = (node: HTMLElement, key: keyof Translations) =>
    node.textContent === translations.RU[key] || node.textContent === translations.EN[key];

  if (showsPlaceholder(currentIp, 'ipDetermining')) currentIp.textContent = t.ipDetermining;
  if (showsPlaceholder(currentIp, 'ipError')) currentIp.textContent = t.ipError;
  if (showsPlaceholder(activeServerName, 'noServerSelected')) activeServerName.textContent = t.noServerSelected;
  if (showsPlaceholder(activeServerDetails, 'selectLocation')) activeServerDetails.textContent = t.selectLocation;


  el('restartBtnText').textContent = t.restartBtn;

    applySubscriptionsLanguage(t);
  applyServersLanguage(t);

  applyRoutingLanguage(t);

  applyConnectionsLanguage(t);
  applyLogsLanguage(t);

  setText('trafficColDown', t.speedDownLabel);
  setText('trafficColUp', t.speedUpLabel);
  setText('trafficRowNow', t.trafficRowNow);
  setText('trafficRowSession', t.trafficRowSession);
  setText('trafficCardTitle', t.speedMeterLabel);

  applySettingsLanguage(t);

  // Плашка Kill Switch: подпись зависит и от языка, и от состояния, поэтому
  // перечитываем состояние, а не подставляем строку вслепую.
  void refreshKillSwitchBadge();
  renderRulesPending();

  // System integration, network, hotkeys, security, and appearance labels handled by applySettingsLanguage(t)
  applyProfilesLanguage(t);
  updateInterceptionGuidance();
  void refreshKillSwitchStatus();

  applyHistoryLanguage(t);

  // Shared prompt/confirm modal — its buttons are reused by every dialog.
  setText('modalCancel', t.modalCancel);
  setText('modalConfirm', t.modalConfirm);

  // DNS leak test. Only the static chrome is set here; the verdict line is
  // written by runDnsLeakTest() when a test actually runs.
  applyDnsLeakLanguage(t);

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

  // Последним, когда все <option> уже переписаны выше. Три <select> скрыты, а
  // вместо них нарисован свой стеклянный список — обход бага прозрачности в
  // WebView2. Переписать нативные <option> недостаточно: список снял с них
  // копию, когда собирался, и до этого вызова смена языка до него не доезжала
  // вовсе — в английском интерфейсе на «Маршрутах» так и висели «Напрямую» и
  // «.суффикс домена».
  //
  // Порядок здесь важен: у dnsSelect опция «свой сервер» переводится отдельной
  // строкой ниже по функции, а не в общем блоке, поэтому синхронизация обязана
  // идти после неё, а не рядом с остальными <option>.
  //
  // Функция сама возвращает false, если список ещё не построен: при первом
  // проходе applyLanguage идёт раньше makeSelectCustom, и собирать виджеты
  // здесь — не её дело.
  syncCustomSelectLabels('newRuleAction');
  syncCustomSelectLabels('newRuleType');
  syncCustomSelectLabels('dnsSelect');
  updateCards();
}

el('langToggle').onclick = () => {
  currentLanguage = currentLanguage === 'RU' ? 'EN' : 'RU';
  applyLanguage();
  collectAndSaveSettings();
};

// Sort dropdown interactions extracted to views/servers.view.ts

// Split tunneling process tabs extracted to views/routing.view.ts

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

  // Кнопка круглая и без текста, поэтому её имя — единственное, что скажет
  // скринридеру, что произойдёт по нажатию. Оно обязано меняться вместе с
  // состоянием, иначе «Подключиться» останется висеть на подключённом VPN.
  const powerName =
    state === 'on' ? t.powerDisconnect : state === 'connecting' ? t.powerConnecting : t.powerConnect;
  powerBtn.setAttribute('aria-label', powerName);
  // Сочетание — только во всплывающей подсказке. В доступном имени оно
  // произносилось бы при каждом переходе фокуса на кнопку, а знать его надо
  // ровно один раз; ставится здесь же, чтобы не разъехаться с именем.
  powerBtn.title = `${powerName} (Ctrl+Enter)`;

  if (state === 'on') {
    powerBtn.classList.add('on');
    powerBtn.classList.remove('connecting');
    // Кольцо прибытия проигрывается один раз и только на настоящем переходе.
    // entered здесь обязателен: applyLanguage перерисовывает интерфейс на том
    // же состоянии, и без этой проверки кольцо разлеталось бы при каждом
    // переключении RU/EN.
    if (entered) powerBtn.classList.add('arrived');
    statusDot.className = 'status-dot on';
    statusText.textContent = t.statusOn;
    statusText.style.color = 'var(--success)';
    restartBtn.style.display = 'flex';
    if (entered) {
      isRestarting = false;
      setTimeout(() => fetchIP(currentIp, t), 2000);
      startSessionTracking();
      sessionTimerStart = Date.now();
    }
    // Start live connection timer
    if (timerBadge) timerBadge.style.display = 'flex';
    setSpeedMeterVisible(true);
    // Идемпотентно (сам снимает прошлые интервалы), поэтому вызывается и при
    // перерисовке: иначе смена языка оставила бы таймер стоять.
    startLiveTimers();
  } else if (state === 'connecting') {
    powerBtn.classList.add('on', 'connecting');
    // На «подключении» скорости ещё нет: показывать два нуля значило бы
    // утверждать, что трафик идёт.
    setSpeedMeterVisible(false);
    // Снимаем прошлое прибытие, иначе следующее подключение его не проиграет:
    // добавление уже присутствующего класса анимацию не перезапускает.
    powerBtn.classList.remove('arrived');
    statusDot.className = 'status-dot connecting';
    statusText.textContent = t.statusConnecting;
    statusText.style.color = 'var(--accent-color)';
    currentIp.textContent = t.ipDetermining;
    restartBtn.style.display = 'none';

    clearInterval(tunStatusInterval ?? undefined);
    tunStatusInterval = null;
    const tunStatusContainer = optionalEl('tunStatusContainer');
    if (tunStatusContainer) tunStatusContainer.style.display = 'none';
  } else {
    powerBtn.classList.remove('on', 'connecting', 'arrived');
    statusDot.className = 'status-dot';
    statusText.textContent = t.statusOff;
    statusText.style.color = 'var(--text-dim)';
    restartBtn.style.display = 'none';
    currentIp.textContent = '—';
    // Сюда приходит и неудавшийся перезапуск. Флаг обязан сброситься именно
    // здесь: иначе он оставался бы поднятым навсегда, и следующий самопроизвольный
    // обрыв туннеля прошёл бы мимо onStopped — интерфейс остался бы «подключено»,
    // а сессия не попала бы в историю.
    isRestarting = false;
    stopLiveTimers();
    sessionTimerStart = 0;
    if (timerBadge) timerBadge.style.display = 'none';
    setSpeedMeterVisible(false);
    if (timerText) timerText.textContent = '00:00:00';

    const tunStatusContainer = optionalEl('tunStatusContainer');
    if (tunStatusContainer) tunStatusContainer.style.display = 'none';
  }

  const hintEl = optionalEl('powerStatusHint');
  if (hintEl) {
    if (state === 'on') {
      hintEl.textContent = currentLanguage === 'RU' ? 'Защищённый туннель активен' : 'Secured tunnel active';
    } else if (state === 'connecting') {
      hintEl.textContent = currentLanguage === 'RU' ? 'Установка соединения...' : 'Connecting...';
    } else {
      hintEl.textContent = currentLanguage === 'RU' ? 'Нажмите для подключения' : 'Press to connect';
    }
  }
  updateActiveServerPing();

  // Опрос соединений имеет смысл только при запущенном ядре, а текст плашки
  // отложенных правил зависит от того, есть ли что перезапускать.
  syncConnectionsPolling();
  renderRulesPending();
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
  syncConnectionsPolling();
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
        statusTextEl.textContent = isActive ? t.tunStatusActive : t.tunStatusError;
        statusTextEl.style.color = isActive ? 'var(--success)' : 'var(--danger)';
        // Класс, а не правка background: плитка красит и подложку, и рисунок в
        // ней, а два инлайновых стиля знали только про подложку.
        statusIconEl.classList.toggle('server-icon--ok', isActive);
        statusIconEl.classList.toggle('server-icon--bad', !isActive);
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
    // getComputedStyle, а не restartBtn.style.display: инлайновый атрибут
    // style= из разметки убран ради ужесточения CSP, и начальное «скрыт» теперь
    // задаётся правилом в style.css. Свойство .style видит только то, что
    // выставили скрипты, поэтому до первого показа оно читалось бы как пустая
    // строка — и кнопка «восстановить TUN» нажимала бы скрытую кнопку
    // перезапуска.
    if (restartBtn && getComputedStyle(restartBtn).display !== 'none') {
      restartBtn.click();
    }
  };
}

// Логгер

// Log state and buffer extracted to views/logs.view.ts

// connectErrorText — текст отказа в подключении для показа пользователю.
function connectErrorText(error: string | undefined): string {
  const t = translations[currentLanguage];
  if (error === 'admin_required') return t.adminRequiredError;
  return error || t.unknownError;
}

// handleConnectFailure разбирается с неудачной попыткой подключения.
//
// userInitiated разделяет два пути, которые раньше были одним, и это и есть
// смысл функции. Отказ admin_required лечится перезапуском с правами
// администратора, а перезапуск — это запрос UAC: системное модальное окно и
// замерший интерфейс на время ответа.
//
// По нажатию кнопки такое поведение ожидаемо — человек только что попросил
// подключиться. В автоподключении на старте оно выглядело поломкой: окно
// открывалось, главная страница отрисовывалась полностью, и на ней ничего не
// нажималось, потому что процесс в этот момент уже вызывал ShellExecute и
// собирался завершиться. Автоматическое действие не должно само поднимать
// диалог системы.
//
// Права для TUN теперь запрашиваются в main() до того, как окно вообще
// появится (см. relaunchElevatedIfNeeded). Сюда с admin_required можно попасть
// только если там уже отказали, — и тогда молча спрашивать второй раз тем более
// незачем.
function handleConnectFailure(error: string | undefined, userInitiated: boolean) {
  const t = translations[currentLanguage];
  if (error === 'admin_required' && userInitiated) {
    void window.api.requestAdmin();
    return;
  }
  showAlert(t.errorDialogTitle, connectErrorText(error), true, t);
}

// Log parsing, streaming and search extracted to views/logs.view.ts

// The backend signals this once the core is actually up. Previously the UI
// watched the log stream for "sing-box started", which meant the connected
// state silently depended on the core's log level being verbose enough.
window.api.onStarted(() => {
  if (appState !== 'on') updateAppInterface('on');
  // Ядро собрало конфиг из текущих настроек — значит выбранный DNS теперь и
  // есть действующий.
  setAppliedDns(currentDnsSetting());
  // Ядро поднялось с конфигом, собранным из текущих settings.json, — значит
  // отложенных правил больше нет. Снимается именно здесь, а не по клику на
  // «Применить сейчас»: до этого момента правило ещё не действует.
  clearRulesPending();
});


window.api.onStopped(() => {
  if (!isRestarting) {
    void finishSessionHistory(); // сохраняем сессию если VPN упал сам
    updateAppInterface('off');
  }
});

// Замеры задержки приходят по одному событию на сервер, и «Пинговать все» с
// «Лучший сервер» запускают их для всего списка сразу. Полная перерисовка на
// каждое из них — это список, собранный столько раз, сколько в нём карточек;
// см. pingCells в server-manager.ts. Вписываем результат в одну ячейку.
window.api.onPingResult((data) => {
  handlePingResult(data.link, data.latency);
  if (data.link === activeServerLink) {
    updateActiveServerPing();
  }
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
        // Пункт меню в трее — такое же нажатие, как кнопка в окне.
        handleConnectFailure(res.error, true);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  })();
});

// Управление соединением
// startConnection поднимает соединение на выбранном сервере. Про userInitiated
// см. handleConnectFailure — от него зависит, можно ли в ответ на отказ поднять
// запрос UAC.
function startConnection(userInitiated: boolean) {
  if (!activeServerLink) {
    const t = translations[currentLanguage];
    showAlert(t.alertDialogTitle, t.selectServerAlert, false, t);
    return;
  }
  updateAppInterface('connecting');
  (async () => {
    try {
      const res = await window.api.startXray(activeServerLink, el<HTMLInputElement>('systemProxyCheckbox').checked);
      if (res && !res.success) {
        handleConnectFailure(res.error, userInitiated);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  })();
}

// Отключение раньше жило в обработчике отдельной кнопки, а кнопка питания
// дёргала её через .click(). Кнопки больше нет — логика стала функцией, и
// вызывать её теперь можно прямо.
function disconnect(): void {
  void finishSessionHistory(); // записываем сессию в историю
  updateAppInterface('off');
  window.api.stopXray();
}

powerBtn.onclick = () => {
  if (appState === 'off') {
    startConnection(true);
  } else {
    disconnect();
  }
};

// Сброс счётчика трафика и старт записи истории.
//
// Раньше функция дополнительно обнуляла десяток узлов спидометра и карточки
// «Передача данных». Показать их было некому, так что вся эта работа сводилась
// к записи нулей в элементы, которых никто не видел. Счётчики байтов ниже —
// настоящие: из них складывается запись сессии в «Истории».
function startSessionTracking() {
  sessionConnectedAt = Date.now();
}

// Запись сессии в историю.
//
// Всё, что описывает закрываемую сессию, снимается синхронно, до первого
// await: вызывающая сторона результат не ждёт и сразу же меняет
// activeServerLink — так устроен обработчик watchdog-switched, — и снимок
// после ожидания записал бы время старого сервера на новый.
//
// Итоги трафика приходят от Go. Раньше они брались из счётчиков на window,
// которые заполняет событие traffic-stats, а его нет, пока окно свёрнуто в
// трей: сессия, завершённая из трея (самый частый способ), уезжала в историю
// с цифрами на момент сворачивания. При смене сервера сторожем итог приходит
// вместе с событием — спросить его позже нельзя, перезапуск ядра обнуляет
// счётчики сразу за событием.
async function finishSessionHistory(switchTraffic?: SessionTraffic): Promise<void> {
  if (!sessionConnectedAt) return;
  const now = Date.now();
  const connectedAt = sessionConnectedAt;
  const link = activeServerLink;
  sessionConnectedAt = null;

  const durationSec = Math.round((now - connectedAt) / 1000);
  if (durationSec < 2) return; // игнорируем мгновенные сессии

  const traffic = switchTraffic ?? await window.api.sessionTraffic();

  const info = link ? parseBasicInfo(link) : { name: '?', type: '?', address: '?' };
  const entry = {
    id: now.toString(),
    server: info.name,
    protocol: info.type,
    address: info.address,
    link, // Сохраняем ссылку для быстрого переподключения
    connectedAt,
    disconnectedAt: now,
    durationSec,
    bytesDown: traffic.down,
    bytesUp: traffic.up
  };

  addSessionHistory(entry);
}

// History store and view extracted to views/history.view.ts

// DNS leak test modal and execution extracted to views/dns-leak.view.ts

// restartCore перезапускает ядро на текущем сервере.
//
// Вынесено из обработчика кнопки, потому что путей сюда теперь два: сама кнопка
// и плашка «Применить сейчас» на изменённых правилах маршрутизации. Обе должны
// идти через restartXray — он, в отличие от пары stop+start, сохраняет
// резервную копию системного прокси (см. комментарий к RestartXray в vpn.go).
function restartCore() {
  if (!activeServerLink) return;
  isRestarting = true;
  updateAppInterface('connecting');
  (async () => {
    try {
      const res = await window.api.restartXray(activeServerLink, el<HTMLInputElement>('systemProxyCheckbox').checked);
      if (res && !res.success) {
        // Перезапуск бывает только по действию человека: кнопка «Перезапустить»
        // либо «Применить сейчас» на изменённых правилах.
        handleConnectFailure(res.error, true);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, errorText(e), true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  })();
}

restartBtn.onclick = restartCore;

let settingsSavedTimer: ReturnType<typeof setTimeout> | null = null;
function flashSettingsSaved(): void {
  const badge = optionalEl('settingsAutoSaveBadge');
  if (!badge) return;
  badge.classList.add('visible');
  if (settingsSavedTimer !== null) clearTimeout(settingsSavedTimer);
  settingsSavedTimer = setTimeout(() => {
    badge.classList.remove('visible');
    settingsSavedTimer = null;
  }, 1800);
}

// Настройки
function collectAndSaveSettings(showFeedback = true) {
  if (showFeedback) flashSettingsSaved();
  const settings: AppSettings = {
    language: currentLanguage,
    dns: currentDnsSetting(),
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
    globalHotkeys: el<HTMLInputElement>('globalHotkeysCheckbox').checked,
    hotkeyToggle: el<HTMLInputElement>('hotkeyToggleInput').value,
    hotkeyShow: el<HTMLInputElement>('hotkeyShowInput').value,
    killSwitch: el<HTMLInputElement>('killSwitchCheckbox').checked,
    dnsLeak: el<HTMLInputElement>('dnsLeakCheckbox').checked,
    ipv6Leak: el<HTMLInputElement>('ipv6LeakCheckbox').checked,
    fakeDns: el<HTMLInputElement>('fakeDnsCheckbox').checked,
    verboseLogging: el<HTMLInputElement>('verboseLoggingCheckbox').checked,
    tlsFragment: el<HTMLInputElement>('tlsFragmentCheckbox').checked,
    lastSelectedServer: activeServerLink,
    processMode: getProcessMode(),
    processListBlacklist: getProcessListBlacklist(),
    processListWhitelist: getProcessListWhitelist(),
    favoriteLinks: getFavoriteLinks(),
    profiles: getProfiles(),
    customRules: getCustomRules(),
    connectionsView: getConnectionsView(),
    theme: getCurrentTheme()
  };
  return window.api.saveSettings(settings);
}

// Profiles CRUD and management extracted to views/profiles.view.ts

// Быстрая галка из меню трея. Ставит её здесь по той же причине, по которой
// здесь же применяется профиль: TUN и Kill Switch — это состав конфигурации, и
// на работающем подключении их смена обязана дойти до ядра.
//
// Меню не хранит своего состояния и ничего не переключает само: оно сообщает,
// по какой галке щёлкнули, а обратно состояние приезжает из SaveSettings. Так
// галка в трее и галка в окне физически не могут разойтись.
window.api.onTrayToggleSetting(async (key) => {
  const boxes = {
    killSwitch: 'killSwitchCheckbox',
    tunMode: 'tunModeCheckbox',
    systemProxy: 'systemProxyCheckbox',
  } as const;
  const box = el<HTMLInputElement>(boxes[key]);
  box.checked = !box.checked;
  updateInterceptionGuidance();
  void refreshKillSwitchStatus();
  await collectAndSaveSettings();
  if (appState === 'on') {
    restartCore();
  }
});


el('saveAppsBtn').onclick = async () => {
  await collectAndSaveSettings();
  const status = el('appsStatus');
  status.style.display = 'inline';
  setTimeout(() => status.style.display = 'none', 2000);
};

// DNS-сервер, с которым ядро работает прямо сейчас.
//
// Он попадает в конфиг при запуске ядра — ровно как правила маршрутизации, —
// поэтому смена сервера на подключённом VPN не меняет ничего до перезапуска.
// Без этого сравнения пользователь выбирает другой DNS, жмёт «Сохранить», ждёт
// — и видит прежний, ничем не отличимо от настройки, которая не работает.

// DNS settings functions extracted to views/settings.view.ts


// Управление окном
async function animateAndAction(action: () => void) {
  document.body.classList.add('window-hidden');
  await new Promise(res => setTimeout(res, 250));
  action();
  // Только после того, как окно действительно ушло: до этого анимация
  // затухания ещё должна проигрываться.
  setUiActive(false);

  // Класс снимается сразу, как окно скрылось, а не при его возвращении.
  //
  // window-hidden — это opacity: 0 на всём body, ради затухания он и нужен. Но
  // снимало его только событие window-restored, а бэкенд шлёт его лишь когда
  // окно возвращают из трея. Свёрнутое в панель задач окно Windows
  // разворачивает сам, не спрашивая приложение и ничего ему не сообщая: клик по
  // кнопке в панели возвращал окно, в котором не видно и не нажимается ничего,
  // — и выйти из этого было нельзя вообще никак.
  //
  // Окна в этот момент уже не видно, поэтому возврат непрозрачности никому не
  // заметен. Смысл в том, что состояния, которое надо отменять, просто не
  // остаётся, — вместо того чтобы добавлять ещё один путь его отмены.
  document.body.classList.remove('window-hidden');
}

el('minimizeBtn').onclick = () => animateAndAction(() => window.api.minimize());
el('closeBtn').onclick = () => animateAndAction(() => window.api.close());

// Окно ушло в трей. Событие приходит на всех путях сокрытия — и на своих
// (кнопки окна выше), и на чужих (пункт трея, закрытие в трей), потому что Go
// шлёт его из одного места, после того как сам поставил windowVisible = false.
// Поэтому здесь же взводится и представление о бэкенде.
window.api.onWindowHidden(() => {
  backendThinksHidden = true;
  setUiActive(false);
});

// Окно вернулось — любым способом.
//
// Бэкенд знает только про свои пути: пункт трея и вызов из ядра. Разворачивание
// из панели задач, Alt+Tab и Win+D проходят мимо него целиком, и без слушателей
// DOM ниже интерфейс оставался бы в спячке после setUiActive(false): таймер
// сессии стоял, анимации были на паузе через app-idle, опрос соединений не шёл.
//
// Обе половины обязаны идти порознь. Раньше их связывал общий `if (uiActive)
// return`, и стоило чему-нибудь поднять uiActive раньше — бэкенду не сообщали
// уже никогда. Именно так и случалось: запасной слушатель focus, который вешал
// на себя мост, срабатывал первым. Go до конца сессии считал окно скрытым и не
// слал 'traffic-stats' — счётчик трафика замирал.
function onWindowBackOnScreen() {
  document.body.classList.remove('window-hidden');

  // Идемпотентно: setUiActive выходит сразу, если состояние не меняется, так
  // что обычное переключение окон ничего не стоит.
  setUiActive(true);

  // От этого у Go зависит подпись пункта в трее и то, шлёт ли он события в
  // окно, которое считает скрытым.
  //
  // Флаг здесь больше не сбрасывается. Go теперь сверяет уведомление с
  // Windows и отбрасывает его, если окно на самом деле спрятано, — а сбрось мы
  // флаг заранее, отброшенное уведомление осталось бы незамеченным: страница
  // считала бы, что Go всё знает, и при следующем — настоящем — показе окна
  // сюда бы уже не дошло. Go до конца сессии считал бы окно скрытым и не слал
  // 'traffic-stats', то есть вернулся бы замерший счётчик трафика.
  //
  // Снимает флаг только подтверждение от Go — обработчик onWindowRestored ниже.
  // Пока подтверждения нет, следующее событие фокуса просто попробует снова.
  if (!backendThinksHidden) return;
  void window.api.notifyWindowShown();
}

// Своё событие бэкенда: он уже знает, что окно на экране, — будить его нечем.
window.api.onWindowRestored(() => {
  backendThinksHidden = false;
  document.body.classList.remove('window-hidden');
  setUiActive(true);
});

// focus — то, что приходит всегда: развёрнутое окно получает фокус.
// visibilitychange добирает случаи, когда окно показали без передачи фокуса.
window.addEventListener('focus', onWindowBackOnScreen);
document.addEventListener('visibilitychange', () => {
  if (document.visibilityState === 'visible') onWindowBackOnScreen();
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
    changelogEl.textContent = update.body || t.updateNoChangelog;

    // Reset styles
    progressSection.style.display = 'none';
    // Заполнение задаётся масштабом, а не шириной: прогресс приходит частыми
    // тиками, и каждая правка width пересчитывала бы раскладку.
    progressBar.style.transform = 'scaleX(0)';
    progressPercent.textContent = '0%';
    overlay.querySelector('.update-modal-content')?.classList.remove('downloading');
    progressStatus.textContent = t.updateProgressStatusDownloading;
    progressStatus.style.color = 'var(--text-dim)';
    
    // Reset buttons visibility and states
    actions.style.display = 'flex';
    cancelBtn.style.display = 'block';
    cancelBtn.disabled = false;
    confirmBtn.style.display = 'block';
    confirmBtn.disabled = false;
    confirmBtn.textContent = t.updateModalConfirm;

    overlay.style.display = 'flex';
    // «Позже» — безопасный выбор, поэтому фокус приходит на него, а не на
    // «Обновить сейчас»: случайный Enter не должен запускать установку.
    const release = trapFocus(overlay, cancelBtn, () => cancelBtn.click());

    cancelBtn.onclick = () => {
      overlay.style.display = 'none';
      release();
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
        release();
        resolve(true);
        return;
      }

      // Transition UI to download state
      cancelBtn.style.display = 'none';
      confirmBtn.disabled = true;
      confirmBtn.textContent = t.updateDownloading;
      progressSection.style.display = 'block';
      // Иконка окна вращается только пока идёт загрузка — раньше она крутилась
      // всё время, что окно открыто, в том числе пока оно просто спрашивает.
      overlay.querySelector('.update-modal-content')?.classList.add('downloading');

      const cleanupEvents: Array<() => void> = [];

      const onProgress = (percent: number) => {
        progressBar.style.transform = `scaleX(${percent / 100})`;
        progressPercent.textContent = `${percent}%`;
        // Ширина полосы — чисто визуальная величина; долю нужно сообщить и
        // вспомогательной технологии.
        progressBar.parentElement?.setAttribute('aria-valuenow', String(percent));
      };

      const onComplete = () => {
        progressBar.style.transform = 'scaleX(1)';
        progressPercent.textContent = '100%';
        progressStatus.textContent = t.updateProgressStatusComplete;
        progressStatus.style.color = 'var(--success)';

        // Загрузка кончилась — вращение иконки больше ничего не означает.
        overlay.querySelector('.update-modal-content')?.classList.remove('downloading');
        progressBar.parentElement?.setAttribute('aria-valuenow', '100');
        cleanupEvents.forEach(dereg => dereg());
        setTimeout(() => {
          overlay.style.display = 'none';
          release();
          resolve(true);
        }, 1500);
      };

      const onError = (errMsg: string) => {
        progressStatus.textContent = `${t.updateProgressStatusError}: ${errMsg}`;
        progressStatus.style.color = 'var(--danger)';

        // Загрузка оборвалась: крутящаяся иконка утверждала бы обратное.
        overlay.querySelector('.update-modal-content')?.classList.remove('downloading');

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

// Поиск ссылки сервера в подписках по метаданным (для обратной совместимости)
function findServerLink(_name: string, address: string, protocol: string) {
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

// Инициализация
async function init() {
  // The title bar reads the version from the backend rather than carrying its own
  // copy, so it always matches the release the update check compares against.
  try {
    const version = await window.api.getAppVersion();
    if (version) setText('appVersion', `v${version}`);
  } catch (e) { console.error('Version lookup failed:', e); }

  // История поднимается с бэкенда, а не из localStorage: там она переживает
  // переустановку и лежит зашифрованной, потому что несёт ссылки на прокси.
  //
  // Здесь, а не рядом с остальной отрисовкой ниже: ниже по функции стоит
  // автоподключение, а завершившаяся сессия пишется поверх кэша. Успей она
  // случиться раньше загрузки — записала бы одну сессию в пустой список и
  // стёрла бы с диска всё, что там лежало.
  await initHistory();

  initLogsView({
    getCurrentLanguage: () => currentLanguage,
    showAlert,
    showConfirm,
  });

  initConnectionsView({
    getCurrentLanguage: () => currentLanguage,
    getAppState: () => appState,
    isViewActive: () => connectionsViewActive,
    isUiActive: () => uiActive,
    upsertCustomRule: (rule) => upsertCustomRule(rule),
    onSaveView: () => {
      collectAndSaveSettings();
    },
    initialView: 'grouped',
  });

  initRoutingView({
    getCurrentLanguage: () => currentLanguage,
    isCoreActive: () => appState === 'on',
    restartCore: () => restartCore(),
    onSaveSettings: () => {
      collectAndSaveSettings();
    },
  });

  initDnsLeakView({
    getCurrentLanguage: () => currentLanguage,
    getCurrentDnsSetting: () => currentDnsSetting(),
    getAppliedDns: () => getAppliedDns(),
  });

  initHistoryView({
    getCurrentLanguage: () => currentLanguage,
    onSelectServer: (link) => selectAndConnectServer(link),
    findServerLink: (name, addr, proto) => findServerLink(name, addr, proto),
    showAlert,
  });

  initProfilesView({
    getCurrentLanguage: () => currentLanguage,
    isCoreActive: () => appState === 'on',
    restartCore: () => restartCore(),
    onSaveSettings: () => collectAndSaveSettings(),
    getActiveServerLink: () => activeServerLink ?? '',
    onSelectServer: (link) => {
      activeServerLink = link;
      const info = parseBasicInfo(link);
      activeServerName.textContent = info.name || info.address || translations[currentLanguage].proxyFallbackName;
      activeServerDetails.textContent = `${info.type.toUpperCase()} • ${info.address}`;
      updateCards();
    },
    onRefreshKillSwitch: () => {
      void refreshKillSwitchStatus();
      void refreshKillSwitchBadge();
    },
    onUpdateInterceptionGuidance: () => updateInterceptionGuidance(),
    showAlert,
  });

  initSettingsView({
    getCurrentLanguage: () => currentLanguage,
    isCoreActive: () => appState === 'on',
    onSaveSettings: () => collectAndSaveSettings(),
    onMarkRulesPending: () => markRulesPending(),
    onRefreshKillSwitch: () => {
      void refreshKillSwitchStatus();
      void refreshKillSwitchBadge();
    },
    onUpdateInterceptionGuidance: () => updateInterceptionGuidance(),
    showAlert,
    showConfirm,
  });

  initKillSwitchView({
    getCurrentLanguage: () => currentLanguage,
    getAppState: () => appState,
    showAlert,
  });

  initServersView({
    getCurrentLanguage: () => currentLanguage,
    getActiveServerLink: () => activeServerLink,
    setActiveServer: (link, name, type, address) => {
      activeServerLink = link;
      activeServerName.textContent = name;
      activeServerDetails.textContent = `${type} • ${address}`;
    },
    getAppState: () => appState,
    restartCore: () => restartCore(),
    startCore: async (link) => {
      await selectAndConnectServer(link);
    },
    onSaveSettings: () => collectAndSaveSettings(),
    showAlert,
    showConfirm,
  });

  initSubscriptionsView({
    getCurrentLanguage: () => currentLanguage,
    onUpdateCards: () => updateCards(),
    onSubscriptionsLoaded: () => renderHistoryTab(),
    showAlert,
  });

  const settings = await window.api.getSettings();
  if (settings) {
    if (settings.language) currentLanguage = settings.language;

    // Тема уже применена из зеркала в localStorage — здесь она сверяется с
    // источником истины. Разойтись они могут после импорта настроек или
    // очистки данных WebView2, и правым в таком споре остаётся settings.json.
    // persist: false — сохранять то, что только что прочитано, нечего.
    const storedTheme = parseTheme(settings.theme);
    if (storedTheme) {
      setTheme(storedTheme, { persist: false });
    } else {
      renderThemeControls();
      renderThemeReport();
    }

    applyLanguage();

    const rememberServer = settings.rememberServer !== undefined ? !!settings.rememberServer : true;
    if (rememberServer && settings.lastSelectedServer) {
       activeServerLink = settings.lastSelectedServer;
       const info = parseBasicInfo(activeServerLink);
       activeServerName.textContent = info.name;
       activeServerDetails.textContent = `${info.type.toUpperCase()} • ${info.address}`;
    }
    
    el<HTMLInputElement>('bypassRuCheckbox').checked = !!settings.bypassRu;
    
    if (settings.dns) setDnsSetting(settings.dns);
    // Снимок того, что уже действует: без него первое же «Сохранить» без
    // единого изменения DNS показало бы плашку об отложенных изменениях.
    setAppliedDns(currentDnsSetting());

    el<HTMLInputElement>('tunModeCheckbox').checked = !!settings.tunMode;
    // По умолчанию включён — так же, как атрибут checked в index.html.
    el<HTMLInputElement>('systemProxyCheckbox').checked =
      settings.systemProxy !== undefined ? !!settings.systemProxy : true;
    el<HTMLInputElement>('autoConnectCheckbox').checked = !!settings.autoConnect;
    el<HTMLInputElement>('autoUpdateSubsCheckbox').checked = !!settings.autoUpdateSubs;
    el<HTMLInputElement>('rememberServerCheckbox').checked = rememberServer;
    el<HTMLInputElement>('openAtLoginCheckbox').checked = !!settings.openAtLogin;
    el<HTMLInputElement>('startMinimizedCheckbox').checked = !!settings.startMinimized;
    // Клавиши регистрируются в системе, а не просто отмечаются галкой,
    // поэтому восстановление настройки — это ещё и попытка их занять. Если
    // сочетание успело достаться другому приложению, галка снимется сама.
    el<HTMLInputElement>('globalHotkeysCheckbox').checked = !!settings.globalHotkeys;
    // Сочетания — до попытки их занять: applyGlobalHotkeys читает поля.
    if (settings.hotkeyToggle) el<HTMLInputElement>('hotkeyToggleInput').value = settings.hotkeyToggle;
    if (settings.hotkeyShow) el<HTMLInputElement>('hotkeyShowInput').value = settings.hotkeyShow;
    if (settings.globalHotkeys) void applyGlobalHotkeys(true, false);
    syncHotkeysDisabled();
    el<HTMLInputElement>('killSwitchCheckbox').checked = !!settings.killSwitch;
    el<HTMLInputElement>('dnsLeakCheckbox').checked = settings.dnsLeak !== undefined ? !!settings.dnsLeak : true;
    el<HTMLInputElement>('ipv6LeakCheckbox').checked = settings.ipv6Leak !== undefined ? !!settings.ipv6Leak : true;
    el<HTMLInputElement>('fakeDnsCheckbox').checked = settings.fakeDns !== undefined ? !!settings.fakeDns : true;
    el<HTMLInputElement>('verboseLoggingCheckbox').checked = !!settings.verboseLogging;
    el<HTMLInputElement>('tlsFragmentCheckbox').checked = !!settings.tlsFragment;
    updateInterceptionGuidance();
    void refreshKillSwitchStatus();
    
    if (settings.processListBlacklist) setProcessListBlacklist(settings.processListBlacklist);
    if (settings.processListWhitelist) setProcessListWhitelist(settings.processListWhitelist);
    if (settings.processMode) setProcessMode(settings.processMode);
    
    // Не powerBtn.click(): автоподключение не должно вести к запросу UAC —
    // см. handleConnectFailure.
    if (settings.autoConnect && activeServerLink) startConnection(false);

    if (settings.favoriteLinks) {
      setFavoriteLinks(settings.favoriteLinks);
    }
    if (settings.profiles) {
      setProfiles(settings.profiles);
    }
    void window.api.rebuildTrayProfiles();
    // persist=false: настройки сейчас читаются, записывать их обратно на этом
    // шаге значило бы сохранять полусобранный объект.
    setConnectionsView(settings.connectionsView === 'flat' ? 'flat' : 'grouped', false);

    if (settings.customRules) {
      setCustomRules(settings.customRules);
    }
  } else {
    applyLanguage();
  }
    await loadSubscriptions();
  renderHistoryTab();
  initScrollWidget();

  // Convert standard select dropdowns to custom glassmorphic dropdowns to prevent WebView2 transparency rendering bugs.
  makeSelectCustom('dnsSelect');
  makeSelectCustom('newRuleAction');
  makeSelectCustom('newRuleType');

  // Последним и без await: интерфейс уже полностью собран и переведён, а окно
  // обновления появится, когда придёт ответ от GitHub, ничего не задерживая.
  void checkForUpdates();
}

// Custom routing rules and pending notification extracted to views/routing.view.ts

// ── Экран «Соединения» ───────────────────────────────────────────────────────

let connectionsViewActive = false;

// Server search filter and auto-best server extracted to views/servers.view.ts

// ── СКОРОСТЬ СОЕДИНЕНИЯ ──────────────────────────────────────────────────────
//
// Отрисовка живёт здесь, а не в мосте: мост — транспорт, и раньше он сам искал
// узлы и писал в них цифры. Данные приходят раз в секунду, пока ядро запущено
// и окно видно; в трее событий нет вовсе — смотреть на них некому.

/**
 * Разделяет скорость на число и единицу.
 *
 * Отдельно потому, что в разметке это два разных по кеглю элемента: единица
 * подчинена числу. Знак после запятой пропадает выше сотни — «128.4 MB/s»
 * длиннее полезного, а лишний разряд двигает строку.
 */
function formatSpeedParts(bytesPerSec: number): { value: string; unit: string } {
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  if (!bytesPerSec || bytesPerSec <= 0) return { value: '0', unit: units[1] };

  const k = 1024;
  const i = Math.min(Math.floor(Math.log(bytesPerSec) / Math.log(k)), units.length - 1);
  const scaled = bytesPerSec / Math.pow(k, i);
  return {
    value: scaled >= 100 ? String(Math.round(scaled)) : scaled.toFixed(1),
    unit: units[i],
  };
}

/**
 * Разделяет объём на число и единицу — теми же правилами, что и скорость,
 * чтобы обе строки таблицы выглядели одинаково набранными.
 */
function formatBytesParts(bytes: number): { value: string; unit: string } {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  if (!bytes || bytes <= 0) return { value: '0', unit: units[0] };

  const k = 1024;
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), units.length - 1);
  const scaled = bytes / Math.pow(k, i);
  return {
    value: scaled >= 100 || i === 0 ? String(Math.round(scaled)) : scaled.toFixed(1),
    unit: units[i],
  };
}

// Последнее записанное значение по каждой ячейке: цифры обновляются раз в
// секунду, и переписывать узел, в котором ничего не изменилось, незачем — та же
// бережность, что у таблицы соединений и журнала.
const trafficLast: Record<string, string> = {};

function writeTrafficCell(idBase: string, parts: { value: string; unit: string }): void {
  if (trafficLast[idBase + 'v'] !== parts.value) {
    trafficLast[idBase + 'v'] = parts.value;
    setText(idBase + 'Value', parts.value);
  }
  if (trafficLast[idBase + 'u'] !== parts.unit) {
    trafficLast[idBase + 'u'] = parts.unit;
    setText(idBase + 'Unit', parts.unit);
  }
}

window.api.onTraffic((stats) => {
  writeTrafficCell('speedDown', formatSpeedParts(stats.down));
  writeTrafficCell('speedUp', formatSpeedParts(stats.up));
  // Итоги считает Go: пока окно в трее, события сюда не приходят, и сумма,
  // накопленная на этой стороне, потеряла бы весь трафик за это время.
  writeTrafficCell('totalDown', formatBytesParts(stats.totalDown));
  writeTrafficCell('totalUp', formatBytesParts(stats.totalUp));
});

/**
 * Показывает индикатор только на подключённом туннеле.
 *
 * Видимостью управляет состояние, а не приход событий: в трее события не
 * приходят вовсе, и привязка к ним прятала бы индикатор у свёрнутого окна,
 * которое затем разворачивают.
 */
function setSpeedMeterVisible(visible: boolean): void {
  // Показывается и прячется карточка целиком, а не таблица внутри неё: иначе
  // на отключённом туннеле в столбце оставалась бы пустая рамка с заголовком.
  const card = optionalEl('trafficCard');
  if (!card) return;
  card.style.display = visible ? 'flex' : 'none';
  if (!visible) {
    // Следующее подключение должно начинаться с нулей, а не с цифр прошлой
    // сессии, мелькнувших до первой выборки.
    for (const key of Object.keys(trafficLast)) delete trafficLast[key];
    writeTrafficCell('speedDown', formatSpeedParts(0));
    writeTrafficCell('speedUp', formatSpeedParts(0));
    writeTrafficCell('totalDown', formatBytesParts(0));
    writeTrafficCell('totalUp', formatBytesParts(0));
  }
}

// Theme editor, presets, and settings transfer extracted to views/settings.view.ts

// Kill Switch state and telemetry extracted to views/kill-switch.view.ts

// ── WATCHDOG EVENT LISTENERS ──────────────────────────────────────────────────
if (window.api.onWatchdogReconnecting) {
  window.api.onWatchdogReconnecting(() => {
    statusText.textContent = translations[currentLanguage].watchdogReconnecting;
    statusText.style.color = 'var(--accent-color)';
    statusDot.className = 'status-dot connecting';
  });
}
if (window.api.onWatchdogReconnected) {
  window.api.onWatchdogReconnected(() => {
    statusText.textContent = translations[currentLanguage].statusOn;
    statusText.style.color = 'var(--success)';
    statusDot.className = 'status-dot on';
  });
}
if (window.api.onWatchdogWaiting) {
  window.api.onWatchdogWaiting(() => {
    const t = translations[currentLanguage];
    // «Нет сети» — правда лишь наполовину, когда сеть режет собственный Kill
    // Switch. Для пользователя это разные ситуации: в одной он ждёт провайдера,
    // в другой — понимает, что интернет вернётся отключением VPN.
    statusText.textContent = isKillSwitchArmed() ? t.watchdogNoNetworkKillSwitch : t.watchdogNoNetwork;
    statusText.style.color = 'var(--accent-color)';
    statusDot.className = 'status-dot connecting';
    void refreshKillSwitchBadge();
  });
}
if (window.api.onWatchdogServerDead) {
  // Сеть есть, а сервер не работает — и заменить его нечем. Отдельно от
  // «нет сети» намеренно: там человек ждёт провайдера, здесь ждать бесполезно,
  // надо менять сервер. Показывается один раз за сессию: watchdog повторяет
  // попытку с нарастающей паузой, и диалог на каждой из них был бы наказанием
  // за то, что приложение продолжает стараться.
  let reported = false;
  window.api.onWatchdogServerDead(() => {
    const t = translations[currentLanguage];
    statusText.textContent = t.watchdogServerDead;
    statusText.style.color = 'var(--warning)';
    statusDot.className = 'status-dot connecting';
    if (!reported) {
      reported = true;
      showAlert(t.alertDialogTitle, t.watchdogServerDeadAlert, false, t);
    }
  });
}
if (window.api.onWatchdogSwitched) {
  window.api.onWatchdogSwitched((link: string, traffic: SessionTraffic) => {
    const t = translations[currentLanguage];

    // Порядок обязателен. finishSessionHistory записывает сессию по текущему
    // activeServerLink, поэтому закрыть прежнюю надо ДО подмены ссылки —
    // иначе время, проведённое на старом сервере, запишется на новый, а
    // кнопка «переподключиться» из истории повела бы не туда.
    void finishSessionHistory(traffic);

    activeServerLink = link;
    const info = parseBasicInfo(link);
    const name = info.name || info.address || t.proxyFallbackName;
    activeServerName.textContent = name;
    activeServerDetails.textContent = `${info.type.toUpperCase()} • ${info.address}`;
    updateCards();
    collectAndSaveSettings();

    // Новая сессия начинается здесь: с этого момента трафик идёт через другой
    // сервер, и в истории это должна быть отдельная запись.
    startSessionTracking();

    statusText.textContent = t.statusOn;
    statusText.style.color = 'var(--success)';
    statusDot.className = 'status-dot on';

    // Подмена сервера под пользователем обязана быть заметной, иначе он будет
    // думать, что сидит на выбранном узле.
    flashConnectionsStatus(t.watchdogSwitched.replace('{name}', name));
    showAlert(t.alertDialogTitle, t.watchdogSwitchedAlert.replace('{name}', name), false, t);
  });
}
if (window.api.onWatchdogFailed) {
  window.api.onWatchdogFailed((err) => {
    statusText.textContent = translations[currentLanguage].watchdogFailed;
    statusText.style.color = 'var(--danger)';
    statusDot.className = 'status-dot error';
    showAlert(translations[currentLanguage].errorDialogTitle, err || 'Watchdog reconnect failed', true, translations[currentLanguage]);
  });
}

// Слушателя mousedown, который на каждый клик звал bringToFront, здесь больше
// нет. Windows и так поднимает окно, по которому кликнули, — а вот побочные
// эффекты были настоящими: WindowShow + SetForegroundWindow + SetFocus на
// каждое нажатие мыши, и следом WM_SETFOCUS, из-за которого WebView2 присылал
// в страницу событие focus.
//
// Отсюда и брался баг с пунктом трея. Закрытие окна крестиком идёт с задержкой
// в 250 мс на анимацию, и focus от того же самого нажатия успевал прийти уже
// ПОСЛЕ того, как окно спрятали: onWindowBackOnScreen видел backendThinksHidden
// и звал NotifyWindowShown. Go снова считал окно видимым, в трее оставалось
// «Скрыть интерфейс» над спрятанным окном, и первый клик по пункту уходил в
// никуда — открывалось только со второго.

init();
