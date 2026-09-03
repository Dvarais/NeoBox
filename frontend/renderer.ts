import { el, optionalEl, query } from './modules/dom';
import { translations } from './modules/translations';
import type { Language, Translations } from './modules/translations';
import type {
  AppSettings,
  ConnectionRow,
  ConnectionsSnapshot,
  CustomRule,
  HistoryEntry,
  Profile,
  ProfileSettings,
  SessionTraffic,
  UpdateInfo,
} from './modules/api';
import { fetchIP, isDialogOpen, showConfirm, showAlert, showPrompt, trapFocus } from './modules/ui-utils';
import {
    allSubscriptions,
    currentActiveSubId,
    loadSubscriptions as loadSubsBase,
    renderSubStatus,
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
    setPingData,
    updateCardPing
} from './modules/server-manager';
import type { SortMode } from './modules/server-manager';
import { groupConnections } from './modules/grouping';
import type { ConnectionGroup, GroupSort } from './modules/grouping';
import { iconSvg } from './modules/icons';
import {
  DEFAULT_THEME,
  PRESETS,
  applyTheme,
  describeTheme,
  deriveTokens,
  parseTheme,
} from './modules/theme';
import type { BackgroundMode, Scheme, ThemeSpec } from './modules/theme';

// ── ТЕМА: раннее применение ──────────────────────────────────────────────────
//
// Здесь, до всего остального, потому что настройки приезжают из Go асинхронно,
// а первый кадр рисуется раньше ответа. Без этой строки человек, выбравший свою
// тему, каждый запуск видел бы вспышку стандартной палитры.
//
// Зеркало в localStorage — только ради этой синхронности. Источник истины
// остаётся в settings.json: он переживает очистку данных WebView2 и уезжает с
// экспортом настроек, чего про localStorage сказать нельзя.
const THEME_STORAGE_KEY = 'neobox-theme';

function readMirroredTheme(): ThemeSpec | null {
  try {
    const raw = localStorage.getItem(THEME_STORAGE_KEY);
    if (!raw) return null;
    return parseTheme(JSON.parse(raw));
  } catch {
    return null;
  }
}

let currentTheme: ThemeSpec = readMirroredTheme() ?? DEFAULT_THEME;
applyTheme(currentTheme);

// Элементы DOM
const powerBtn = el('powerBtn');
const restartBtn = el('restartBtn');
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

// Kill Switch. Объявлено здесь, а не рядом с refreshKillSwitchBadge внизу
// файла: applyLanguage обращается к состоянию раньше по тексту, и `let` в
// точке использования дал бы временную мёртвую зону.
//
// killSwitchArmed — последнее известное состояние, его читает обработчик
// watchdog-waiting. killSwitchStuckReported — про застрявшие правила говорим
// один раз за запуск: это модальное окно, и показывать его на каждом опросе
// значило бы не давать работать.
let killSwitchArmed = false;
let killSwitchStuckReported = false;

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
  // Lines that arrived while this view was closed were kept out of the DOM;
  // rebuild the panel now that it is visible.
  if (targetId === 'view-logs' && logsDomStale) renderLogs();

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
async function applyGlobalHotkeys(enable: boolean, announce: boolean): Promise<void> {
  const failure = await window.api.setGlobalHotkeys(
    enable,
    el<HTMLInputElement>('hotkeyToggleInput').value,
    el<HTMLInputElement>('hotkeyShowInput').value,
  );
  if (!failure) return;

  const t = translations[currentLanguage];
  el<HTMLInputElement>('globalHotkeysCheckbox').checked = false;
  // При восстановлении настройки на старте показывать диалог поверх ещё не
  // собранного интерфейса незачем — там достаточно снятой галки.
  if (announce) {
    showAlert(t.errorDialogTitle, t.globalHotkeysFailed.replace('{reason}', failure), false, t);
  }
  collectAndSaveSettings();
}

el<HTMLInputElement>('globalHotkeysCheckbox').addEventListener('change', (e) => {
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

    // Пустое состояние списка. Сообщений два, и выбираются они так, чтобы ни
    // один случай не оказался ложным: «нет подписок» — только когда подписок
    // действительно нет; пустое «Избранное» и безрезультатный поиск получают
    // нейтральное «ничего не найдено».
    const emptyEl = optionalEl('serversEmpty');
    if (emptyEl) {
      const isEmpty = serversGrid.children.length === 0;
      emptyEl.style.display = isEmpty ? 'flex' : 'none';
      if (isEmpty) {
        const t = translations[currentLanguage];
        // Пишем напрямую, а не через setText: тот объявлен ниже по файлу через
        // const, а updateCards поднимается и может быть вызвана раньше.
        const textEl = optionalEl('serversEmptyText');
        if (textEl) textEl.textContent = allSubscriptions.length === 0 ? t.noSubscriptions : t.noSearchResults;
      }
    }
}

async function loadSubscriptions() {
    await loadSubsBase(() => {
        renderSubTabs(subTabsContainer, translations, currentLanguage, () => {
            updateCards();
            // Строка состояния относится к ВЫБРАННОЙ подписке, поэтому
            // перерисовывается и при смене вкладки, а не только списка.
            renderSubStatus(optionalEl('subStatusLine'), translations, currentLanguage);
        }, loadSubscriptions);
        updateCards();
        renderSubStatus(optionalEl('subStatusLine'), translations, currentLanguage);
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
const setAria = (id: string, value: string) => { const node = optionalEl(id); if (node) node.setAttribute('aria-label', value); };

/**
 * Имя иконочной кнопки для вспомогательной технологии плюс подсказка для мыши.
 *
 * title всплывает по наведению, но с клавиатуры не показывается никогда, и
 * скринридеры читают его непоследовательно. Поэтому одна и та же строка идёт и
 * в title, и в aria-label — второй и есть имя элемента.
 */
const setLabel = (id: string, value: string) => {
  const node = optionalEl(id);
  if (!node) return;
  node.title = value;
  node.setAttribute('aria-label', value);
};

/**
 * Подпись поля, у которого нет видимого <label>: placeholder исчезает при
 * первом же символе и именем служить не может.
 */
const setFieldLabel = (id: string, value: string) => {
  const node = optionalEl<HTMLInputElement | HTMLTextAreaElement>(id);
  if (!node) return;
  node.placeholder = value;
  node.setAttribute('aria-label', value);
};

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
  setAria('connTable', t.connTableLabel);
  setAria('fullLogOutput', t.logsTitle);
  const logTabs = document.querySelector('.log-tabs');
  if (logTabs) logTabs.setAttribute('aria-label', t.logFilterLabel);
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

  el('importQrBtn').textContent = t.importQrBtn;
  el('qrModalTitle').textContent = t.qrModalTitle;
  // Подпись в отдельном span: у обеих кнопок рядом стоит иконка, и запись в
  // textContent самой кнопки стирала бы её.
  setText('qrStartCameraBtnText', t.qrStartCameraBtn);
  setText('qrUploadFileBtnText', t.qrUploadFileBtn);
  el('qrPlaceholderText').textContent = t.qrPlaceholderText;
  el('qrModalClose').textContent = t.errorDialogClose;

  el('subManagementTitle').textContent = t.subManagement;
  setFieldLabel('subName', t.subNamePlaceholder);
  setFieldLabel('subUrl', t.subUrlPlaceholder);
  el('addSubBtn').textContent = t.addBtn;
  el('updateSubBtn').textContent = t.updateCurrentBtn;
  setTitle('updateSubBtn', t.updateCurrentBtnTitle);
  el('importClipboardBtn').textContent = t.importClipboardBtn;
  el('myLocationsTitle').textContent = t.myLocations;
  el('pingAllBtn').textContent = t.pingAllBtn;
  el('sortBtnText').textContent = t.sortBtn;
  setFieldLabel('serverSearchInput', t.serverSearchPlaceholder);
  // Подсказки о сочетаниях живут в title, а не в доступном имени: диктору они
  // не нужны, а увидеть их надо тому, кто наводит мышь.
  setTitle('serverSearchInput', `${t.serverSearchPlaceholder} (Ctrl+K)`);
  setTitle('bestServerBtn', t.bestServerBtnTitle);
  setText('bestServerBtnText', t.bestServerBtn);

  document.querySelectorAll<HTMLElement>('.sort-item').forEach(item => {
    const mode = item.dataset.sort ?? '';
    // Подпись живёт в span рядом с иконкой; запись в сам пункт стёрла бы её.
    const label = item.querySelector('span');
    if (label) label.textContent = translate(t, `sort${mode.charAt(0).toUpperCase() + mode.slice(1)}`);
  });

  el('routeSettingsTitle').textContent = t.routeSettings;
  el('bypassRuLabel').textContent = t.bypassRuLabel;
  el('splitTunnelingTitle').textContent = t.splitTunnelingTitle;
  el('splitTunnelingDesc').innerHTML = t.splitTunnelingDesc.replace('chrome.exe', '<b>chrome.exe</b>');
  
  document.querySelectorAll<HTMLElement>('.process-tab').forEach(tab => {
    const mode = tab.dataset.mode;
    tab.textContent = mode === 'blacklist' ? t.blacklistTab : t.whitelistTab;
  });
  el<HTMLTextAreaElement>('processListBlacklist').placeholder = t.blacklistPlaceholder;
  el<HTMLTextAreaElement>('processListWhitelist').placeholder = t.whitelistPlaceholder;
  // Плейсхолдер у этих полей — пример формата, а не название, поэтому имя
  // задаётся отдельной строкой.
  setAria('processListBlacklist', t.blacklistAreaLabel);
  setAria('processListWhitelist', t.whitelistAreaLabel);
  setAria('newRuleAction', t.ruleActionLabel);
  setAria('newRuleType', t.ruleTypeLabel);
  setAria('customDnsInput', t.customDnsLabel);
  // Плейсхолдер был зашит в разметку и мимо таблицы переводов: ключ для него
  // существовал, но никто его не читал.
  setPlaceholder('customDnsInput', t.dnsCustomPlaceholder);
  setText('customDnsHint', t.dnsCustomHint);

  const customRoutesTitle = optionalEl('customRoutesTitle');
  if (customRoutesTitle) customRoutesTitle.textContent = t.customRoutesTitle;
  const customRoutesDesc = optionalEl('customRoutesDesc');
  if (customRoutesDesc) customRoutesDesc.textContent = t.customRoutesDesc;
  const addCustomRuleBtn = optionalEl('addCustomRuleBtn');
  if (addCustomRuleBtn) addCustomRuleBtn.textContent = t.addCustomRuleBtn;

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
  const optTypeProcess = optionalEl('optTypeProcess');
  if (optTypeProcess) optTypeProcess.textContent = t.typeProcessOption;


  const newRuleValue = optionalEl<HTMLInputElement>('newRuleValue');
  if (newRuleValue) setFieldLabel('newRuleValue', t.ruleValuePlaceholder);

  renderCustomRules();

  setText('connectionsTitle', t.connectionsTitle);
  setText('connectionsDesc', t.connectionsDesc);
  setText('connColHost', t.connColHost);
  setText('connColDest', t.connColDest);
  setText('connColProcess', t.connColProcess);
  setText('connColRule', t.connColRule);
  setText('connColTraffic', t.connColTraffic);
  setText('connColAge', t.connColAge);
  setText('connColActions', t.connColActions);
  setText('connGroupSitesTitle', t.connGroupSites);
  setText('connGroupProgramsTitle', t.connGroupPrograms);
  setText('connSitesEmpty', t.connGroupSitesEmpty);
  setText('connProgramsEmpty', processRulesAvailable() ? t.connGroupProgramsEmpty : t.connGroupProgramsNeedTun);
  setText('trafficColDown', t.speedDownLabel);
  setText('trafficColUp', t.speedUpLabel);
  setText('trafficRowNow', t.trafficRowNow);
  setText('trafficRowSession', t.trafficRowSession);
  setText('trafficCardTitle', t.speedMeterLabel);

  setFieldLabel('logSearchInput', t.logSearchPlaceholder);
  setText('logsEmpty', t.noSearchResults);

  setText('appearanceTitle', t.appearanceTitle);
  setText('appearanceDesc', t.appearanceDesc);
  setText('themeSchemeLabel', t.themeSchemeLabel);
  setText('themeSchemeDark', t.themeSchemeDark);
  setText('themeSchemeLight', t.themeSchemeLight);
  setText('themeAccentLabel', t.themeAccentLabel);
  setAria('themeAccentHex', t.themeAccentHexAria);
  setText('themeBgLabel', t.themeBgLabel);
  setText('themeStopsLabel', t.themeStopsLabel);
  setText('themeAngleLabel', t.themeAngleLabel);
  setText('themeModeSolid', t.themeModeSolid);
  setText('themeModeLinear', t.themeModeLinear);
  setText('themeModeRadial', t.themeModeRadial);
  setText('themeStopAdd', t.themeStopAdd);
  setText('themeStopRemove', t.themeStopRemove);
  setText('themeResetBtnText', t.themeReset);
  // Плитки и отчёт несут переведённый текст внутри, поэтому пересобираются
  // целиком, а не подписываются по одному узлу.
  renderThemePresets();
  renderThemeControls();
  renderThemeReport();

  setText('transferTitle', t.transferTitle);
  setText('transferDesc', t.transferDesc);
  setText('exportSettingsBtnText', t.exportSettingsBtn);
  setText('importSettingsBtnText', t.importSettingsBtn);

  setText('connViewGrouped', t.connViewGrouped);
  setText('connViewFlat', t.connViewFlat);
  setText('connSortName', t.connSortName);
  setText('connSortTraffic', t.connSortTraffic);
  // Плашка Kill Switch: подпись зависит и от языка, и от состояния, поэтому
  // перечитываем состояние, а не подставляем строку вслепую.
  void refreshKillSwitchBadge();
  // По id, а не по классу: переключателей с классом .conn-view-switch теперь
  // два — вид и порядок групп, — и querySelector нашёл бы только первый,
  // оставив второй с русским именем в английском интерфейсе.
  setAria('connViewSwitch', t.connViewLabel);
  setAria('connSortSwitch', t.connSortLabel);

  // Строки таблицы и папки содержат переведённые подписи кнопок, а
  // перерисовываются только по тику опроса — на отключённом VPN его нет вовсе.
  // Сбрасываем их, чтобы смена языка не оставила на экране прежний.
  resetConnectionsView();
  renderRulesPending();

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
  el('globalHotkeysLabel').textContent = t.globalHotkeysLabel;
  el('profilesTitle').textContent = t.profilesTitle;
  el('profilesDesc').textContent = t.profilesDesc;
  el('profilesEmpty').textContent = t.profilesEmpty;
  el('saveProfileBtnText').textContent = t.saveProfileBtn;
  el('profileIncludeServerLabel').textContent = t.profileIncludeServerLabel;
  // Список профилей рисуется строками с подписями — перевести атрибуты по
  // месту нельзя, проще перерисовать.
  renderProfiles();
  el('globalHotkeysDesc').textContent = t.globalHotkeysDesc;
  el('hotkeyToggleLabel').textContent = t.hotkeyToggleLabel;
  el('hotkeyShowLabel').textContent = t.hotkeyShowLabel;
  el('hotkeyHint').textContent = t.hotkeyHint;
  el('securityTitle').textContent = t.securityTitle;
  el('killSwitchLabel').textContent = t.killSwitchLabel;
  el('killSwitchDesc').textContent = t.killSwitchDesc;
  el('dnsLeakLabel').textContent = t.dnsLeakLabel;
  el('ipv6LeakLabel').textContent = t.ipv6LeakLabel;
  el('fakeDnsLabel').textContent = t.fakeDnsLabel;
  el('fakeDnsDesc').textContent = t.fakeDnsDesc;
  setText('verboseLoggingLabel', t.verboseLoggingLabel);
  setText('verboseLoggingDesc', t.verboseLoggingDesc);
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

  renderSubTabs(subTabsContainer, translations, currentLanguage, updateCards, loadSubscriptions);
  renderSubStatus(optionalEl('subStatusLine'), translations, currentLanguage);
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
const sortBtn = el<HTMLButtonElement>('sortBtn');
const sortItems = Array.from(document.querySelectorAll<HTMLElement>('.sort-item'));
let sortMenuTimeout: ReturnType<typeof setTimeout>;

// Меню открывалось и закрывалось только мышью — с клавиатуры до сортировки было
// не добраться вовсе. Теперь состояние держится в одном месте и отражается в
// aria-expanded, а CSS дополнительно раскрывает меню по :focus-within.
function setSortMenuOpen(open: boolean): void {
  sortMenu.classList.toggle('show', open);
  sortBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
}

sortDropdown.addEventListener('mouseenter', () => {
  clearTimeout(sortMenuTimeout);
  setSortMenuOpen(true);
});

sortDropdown.addEventListener('mouseleave', () => {
  // Пока фокус внутри меню, уводить его из-под клавиатуры нельзя.
  sortMenuTimeout = setTimeout(() => {
    if (!sortDropdown.contains(document.activeElement)) setSortMenuOpen(false);
  }, 550);
});

sortBtn.addEventListener('click', () => {
  setSortMenuOpen(sortBtn.getAttribute('aria-expanded') !== 'true');
});

sortBtn.addEventListener('keydown', (e: KeyboardEvent) => {
  if (e.key === 'ArrowDown' || e.key === 'Enter' || e.key === ' ') {
    e.preventDefault();
    setSortMenuOpen(true);
    sortItems[0]?.focus();
  }
});

sortDropdown.addEventListener('keydown', (e: KeyboardEvent) => {
  if (e.key !== 'Escape') return;
  setSortMenuOpen(false);
  sortBtn.focus();
});

sortItems.forEach((item, index) => {
  item.addEventListener('click', () => {
    sortItems.forEach(i => {
      i.classList.remove('active');
      i.setAttribute('aria-checked', 'false');
    });
    item.classList.add('active');
    item.setAttribute('aria-checked', 'true');
    setSortMode(item.dataset.sort as SortMode);
    updateCards();
    setSortMenuOpen(false);
    sortBtn.focus();
  });

  item.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
    e.preventDefault();
    const delta = e.key === 'ArrowDown' ? 1 : -1;
    sortItems[(index + delta + sortItems.length) % sortItems.length].focus();
  });
});

// Split Tunneling
const processTabs = document.querySelectorAll<HTMLElement>('.process-tab');
const processModeHidden = el<HTMLInputElement>('processModeHidden');

processTabs.forEach(tab => {
  tab.addEventListener('click', (e) => {
    processTabs.forEach(t => {
      t.classList.remove('active');
      t.setAttribute('aria-pressed', 'false');
      t.style.background = 'transparent';
      t.style.color = 'var(--text-main)';
      t.style.fontWeight = 'normal';
    });
    const target = e.currentTarget as HTMLElement;
    target.classList.add('active');
    target.setAttribute('aria-pressed', 'true');
    target.style.background = 'var(--accent-color)';
    target.style.color = 'var(--on-accent)';
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
// Подстрока поиска по журналу, уже в нижнем регистре: сравнение идёт на каждой
// строке буфера, приводить регистр там же значило бы делать это пятьсот раз.
let logSearchQuery = '';
let isScrolledToBottom = true;
// True when lines arrived while the Logs view was closed, so the panel needs a
// full rebuild before it is shown again.
let logsDomStale = false;

fullLogOutput.addEventListener('scroll', () => {
    isScrolledToBottom = Math.abs(fullLogOutput.scrollHeight - fullLogOutput.clientHeight - fullLogOutput.scrollTop) < 5;
});

document.querySelectorAll<HTMLElement>('.log-tab').forEach(tab => {
  tab.addEventListener('click', (e) => {
      document.querySelectorAll<HTMLElement>('.log-tab').forEach(t => {
        t.classList.remove('active');
        t.setAttribute('aria-pressed', 'false');
      });
      const tabEl = e.currentTarget as HTMLElement;
      tabEl.classList.add('active');
      tabEl.setAttribute('aria-pressed', 'true');
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

// connectErrorText — текст отказа в подключении для показа пользователю.
//
// Всё, что возвращает StartXray, уже переведено на стороне Go, кроме одного:
// admin_required — это признак, по которому запрашивается повышение прав, а не
// сообщение. До сих пор он же и попадал в диалог, и человек с отклонённым
// запросом UAC читал слово «admin_required».
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

/**
 * Проходит ли строка оба фильтра — по уровню и по подстроке.
 *
 * Предикат вынесен потому, что отбор происходит в двух местах: при полной
 * перерисовке и при добавлении пришедшей пачки. Пока условие было выписано в
 * обоих, добавить к нему поиск означало бы завести две копии, которые разойдутся
 * при первой же правке — и новые строки просачивались бы мимо фильтра.
 */
function logMatchesFilters(entry: LogEntry): boolean {
  if (currentLogFilter !== 'ALL' && entry.level !== currentLogFilter) return false;
  if (logSearchQuery && !entry.text.toLowerCase().includes(logSearchQuery)) return false;
  return true;
}

function renderLogs() {
  fullLogOutput.innerHTML = '';
  const fragment = document.createDocumentFragment();
  let shown = 0;
  logsArray.forEach(entry => {
      if (!logMatchesFilters(entry)) return;
      fragment.appendChild(makeLogElement(entry));
      shown++;
  });
  fullLogOutput.appendChild(fragment);
  fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
  logsDomStale = false;
  updateLogsEmptyState(shown);
}

/**
 * Пустой журнал и «поиск ничего не нашёл» — разные сообщения.
 *
 * Без этого отфильтрованный журнал выглядит сломанным: строки идут, а на
 * экране пусто, и понять, что виноват собственный запрос, неоткуда.
 */
function updateLogsEmptyState(shown: number): void {
  const empty = optionalEl('logsEmpty');
  if (!empty) return;
  const t = translations[currentLanguage];
  const filtering = logSearchQuery !== '' || currentLogFilter !== 'ALL';
  empty.style.display = shown === 0 && filtering && logsArray.length > 0 ? 'block' : 'none';
  empty.textContent = t.noSearchResults;
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

    if (fragment && logMatchesFilters(entry)) {
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
    updateLogsEmptyState(fullLogOutput.childNodes.length);
  }
}

// Поиск по журналу.
//
// Перерисовка откладывается: каждый набранный символ иначе пересобирал бы до
// пятисот узлов, а это ровно та беда, из-за которой панель логов однажды уже
// переписывали — непрерывная сборка и выброс узлов, за которой не поспевает
// сборщик мусора WebView2.
const LOG_SEARCH_DEBOUNCE_MS = 150;
let logSearchTimer: ReturnType<typeof setTimeout> | null = null;

const logSearchInput = optionalEl<HTMLInputElement>('logSearchInput');
if (logSearchInput) {
  logSearchInput.addEventListener('input', () => {
    if (logSearchTimer !== null) clearTimeout(logSearchTimer);
    logSearchTimer = setTimeout(() => {
      logSearchTimer = null;
      logSearchQuery = logSearchInput.value.trim().toLowerCase();
      renderLogs();
    }, LOG_SEARCH_DEBOUNCE_MS);
  });
}

// События API
window.api.onLog(addLogEntries);

// The backend signals this once the core is actually up. Previously the UI
// watched the log stream for "sing-box started", which meant the connected
// state silently depended on the core's log level being verbose enough.
window.api.onStarted(() => {
  if (appState !== 'on') updateAppInterface('on');
  // Ядро собрало конфиг из текущих настроек — значит выбранный DNS теперь и
  // есть действующий.
  appliedDns = currentDnsSetting();
  // Ядро поднялось с конфигом, собранным из текущих settings.json, — значит
  // отложенных правил больше нет. Снимается именно здесь, а не по клику на
  // «Применить сейчас»: до этого момента правило ещё не действует.
  if (rulesPending) {
    rulesPending = false;
    renderRulesPending();
  }
});

clearLogsBtn.onclick = () => {
  logsArray.length = 0;
  fullLogOutput.innerHTML = '';
  logsDomStale = false;
};

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
  setPingData(data.link, data.latency);

  // Сортировка по задержке — единственный случай, когда новый замер меняет ещё
  // и порядок, так что список приходится собирать заново. Такие перерисовки
  // объединяются, иначе получится ровно то, от чего мы здесь уходим.
  if (currentSortMode === 'ping') {
    scheduleCardsUpdate();
  } else {
    updateCardPing(data.link, data.latency);
  }
});

// scheduleCardsUpdate сводит пачку запросов на перерисовку списка к одной.
//
// Окно намеренно заметно длиннее кадра: пересборка нескольких сотен карточек
// стоит достаточно, чтобы делать её шестьдесят раз в секунду было немногим
// лучше, чем не объединять вовсе, а перестроение списка по мере поступления
// замеров и так воспринимается как «список утрясается».
const CARDS_UPDATE_COALESCE_MS = 250;
let cardsUpdateTimer: ReturnType<typeof setTimeout> | null = null;

function scheduleCardsUpdate() {
  if (cardsUpdateTimer !== null) return;
  cardsUpdateTimer = setTimeout(() => {
    cardsUpdateTimer = null;
    updateCards();
  }, CARDS_UPDATE_COALESCE_MS);
}

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

  const history = loadHistory();
  history.unshift(entry);
  if (history.length > 100) history.pop(); // храним не более 100 записей
  saveHistory(history);

  renderHistoryTab();
}

// Ключ, под которым история лежала в localStorage до переезда к бэкенду.
// Остаётся здесь ради одноразового переноса — см. initHistory.
const LEGACY_HISTORY_KEY = 'neobox-connection-history';

/**
 * История в памяти.
 *
 * Читается она отовсюду синхронно — вкладка рисуется прямо в обработчике
 * клика, — а бэкенд отвечает промисом. Кэш примиряет одно с другим: он
 * заполняется один раз при старте и дальше служит источником истины для
 * экрана, а запись на диск идёт вдогонку и никого не ждёт. Порядок операций
 * при этом сохраняется: Wails выполняет вызовы по очереди, а сюда история
 * приходит только из finishSessionHistory и кнопки «Очистить».
 */
let historyCache: HistoryEntry[] = [];

function loadHistory(): HistoryEntry[] {
  return historyCache;
}

function saveHistory(history: HistoryEntry[]) {
  historyCache = history;
  void window.api.saveHistory(history);
}

/**
 * Поднимает историю с бэкенда и переносит туда то, что осталось в localStorage
 * от версий до 1.7.6.
 *
 * Перенос делается один раз и только в пустую историю: если у бэкенда уже
 * что-то есть, значит переезд состоялся раньше, а остаток в localStorage —
 * копия того же самого, и затирать ею свежие записи нельзя.
 *
 * Ключ снимается только после успешной записи. Провалившийся перенос обязан
 * оставить старые данные на месте, иначе единственная неудачная попытка стоила
 * бы пользователю всей истории — ровно того, ради чего переезд и затевался.
 */
async function initHistory(): Promise<void> {
  historyCache = await window.api.getHistory();

  const legacyRaw = localStorage.getItem(LEGACY_HISTORY_KEY);
  if (legacyRaw !== null) {
    let legacy: unknown = null;
    try {
      legacy = JSON.parse(legacyRaw);
    } catch {
      legacy = null; // нечитаемый остаток переносить некуда
    }

    if (historyCache.length === 0 && Array.isArray(legacy) && legacy.length > 0) {
      if (await window.api.saveHistory(legacy as HistoryEntry[])) {
        historyCache = legacy as HistoryEntry[];
        localStorage.removeItem(LEGACY_HISTORY_KEY);
      }
    } else {
      localStorage.removeItem(LEGACY_HISTORY_KEY);
    }
  }

  renderHistoryTab();
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
  // Плитка записи несёт один и тот же значок узла для всех протоколов.
  //
  // Раньше протокол кодировался цветным кружком: 🟢 VLESS, 🟡 VMess, 🔷 Trojan,
  // 💜 Shadowsocks, 🟤 TUIC. Различие держалось ровно на цвете и ни на чём
  // больше, то есть для человека, не различающего эти оттенки, четырнадцать
  // протоколов выглядели одинаково — критерий WCAG 1.4.1, а продукт объявил
  // целью AA. Заодно кружки были эмодзи и не подчинялись теме.
  //
  // Дублировать цвет формой было бы честнее, но и незачем: протокол уже
  // написан словом в строке ниже — «VLESS», «HYSTERIA2». Плитке остаётся её
  // настоящая работа — быть кнопкой переподключения (при наведении она
  // показывает ▶), а не вторым, худшим способом сообщить то же самое.
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

    const mark = document.createElement('span');
    mark.className = 'history-icon-mark';
    mark.innerHTML = iconSvg('globe', 20);

    const playGeneric = document.createElement('span');
    playGeneric.className = 'history-icon-play generic';
    playGeneric.innerHTML = PLAY_ICON_SVG;

    const playYoutube = document.createElement('span');
    playYoutube.className = 'history-icon-play youtube';
    playYoutube.innerHTML = YOUTUBE_ICON_SVG;

    icon.appendChild(mark);
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
    // Иконка вставляется разметкой (это константа из набора), а текст —
    // текстовым узлом: протокол приходит из ссылки подписки, то есть из
    // недоверенного источника, и в innerHTML ему делать нечего.
    ([
      ['radio', proto],
      ['clock', dur],
      ['calendar', `${dateStr} ${timeStr}`],
    ] as const).forEach(([name, text]) => {
      const item = document.createElement('span');
      item.className = 'history-meta-item';
      item.innerHTML = iconSvg(name, 12);
      item.appendChild(document.createTextNode(text));
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
// Удержание фокуса ставится один раз на открытие: «Повторить» перезапускает
// проверку в уже открытом окне, и повторный trapFocus положил бы в стек второй
// экземпляр того же диалога.
let dnsLeakRelease: (() => void) | null = null;

function closeDnsLeakModal(): void {
  el('dnsLeakModalOverlay').style.display = 'none';
  dnsLeakRelease?.();
  dnsLeakRelease = null;
}

el('dnsLeakBtn').onclick = () => runDnsLeakTest();
el('dnsLeakCloseBtn').onclick = closeDnsLeakModal;
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
  if (!dnsLeakRelease) {
    dnsLeakRelease = trapFocus(overlay, el('dnsLeakCloseBtn'), closeDnsLeakModal);
  }
  loading.style.display = 'flex';
  result.style.display  = 'none';
  retryBtn.style.display = 'none';
  iconWrap.className = 'dns-leak-icon-wrap';
  banner.className = 'dns-leak-status-banner';

  try {
    // 1. Получаем внешний IP через ipify
    const ipRes = await fetch('https://api.ipify.org?format=json').then(r => r.json());
    const myIp = ipRes.ip || '?';

    // 2. Спрашиваем, какой резолвер на самом деле обслужил запрос.
    //
    // Раньше здесь стоял TXT-запрос whoami.cloudflare.com, отправленный прямо в
    // cloudflare-dns.com. Такой запрос ничего не проверял: резолвером в нём
    // всегда оказывался Cloudflare — его и просили, — поэтому проверка отвечала
    // «утечек нет» при любом состоянии туннеля, а сверка с захардкоженными
    // префиксами Cloudflare начала врать сразу, как только настройка DNS
    // заработала и стало можно выбрать Google или Quad9.
    //
    // edns.ip-api.com отвечает редиректом на случайное имя в своей зоне. Это имя
    // резолвится обычным путём — то есть тем DNS, который у приложения
    // настроен, — а авторитетный сервер зоны видит, кто именно спросил, и
    // возвращает его адрес и владельца. Проверяется поэтому реальный путь
    // запроса, а не то, кому мы этот запрос адресовали.
    const edns = await fetch('https://edns.ip-api.com/json').then(r => r.json());
    const resolverIp: string = edns?.dns?.ip ?? '';
    const resolverGeo: string = edns?.dns?.geo ?? '';

    // Владелец сети резолвера для каждого встроенного варианта из dnsSelect.
    //
    // Сравнивается именно владелец, а не адрес: DoH-провайдер принимает запросы
    // на 1.1.1.1, а к авторитетным серверам ходит с совсем других адресов своей
    // сети. Строка geo от ip-api имеет вид «Russia - Cloudflare, Inc.» — то есть
    // содержит имя организации из записи AS, по нему и опознаём.
    //
    // Карта приходит с бэкенда (core.DNSResolverOwners) — та же, по которой
    // собирается конфиг. Своя копия здесь разошлась бы с ней при первом
    // добавленном провайдере, и разошлась бы худшим образом: проверка объявила
    // бы утечку на исправном туннеле, не зная владельца нового резолвера.
    const resolverOwners = await window.api.dnsResolverOwners();

    // Сверять надо с тем DNS, который ядро использует сейчас, а не с тем, что
    // выбран в списке: настройка вступает в силу только при перезапуске ядра.
    const expectedOwner = resolverOwners[appliedDns || currentDnsSetting()];
    const seen = `${resolverGeo} ${resolverIp}`.toLowerCase();

    // Отображаем результат
    loading.style.display = 'none';
    result.style.display  = 'block';
    retryBtn.style.display = 'flex';
    ipEl.textContent = myIp;

    // textContent, а не innerHTML: строка приходит из ответа стороннего сервиса.
    dnsList.innerHTML = '';
    const div = document.createElement('div');
    div.className = 'dns-leak-dns-entry';
    if (resolverIp) {
      div.textContent = resolverGeo ? `${resolverIp} (${resolverGeo})` : resolverIp;
    } else {
      div.style.color = 'var(--text-dim)';
      div.textContent = t.dnsLeakUnknown;
    }
    dnsList.appendChild(div);

    // Три исхода, а не два. Своему DoH-адресу владельца сети взять неоткуда, и
    // называть это утечкой нельзя: красная плашка на исправном туннеле — ровно
    // та ошибка, из-за которой проверку и переписали. Показываем резолвера и
    // говорим прямо, что подтвердить не смогли.
    if (!resolverIp) {
      iconWrap.className = 'dns-leak-icon-wrap';
      banner.className   = 'dns-leak-status-banner';
      statusIcon.innerHTML = iconSvg('shield', 18);
      statusTxt.textContent  = t.dnsLeakUnknown;
    } else if (!expectedOwner) {
      iconWrap.className = 'dns-leak-icon-wrap';
      banner.className   = 'dns-leak-status-banner';
      statusIcon.innerHTML = iconSvg('shield', 18);
      statusTxt.textContent  = t.dnsLeakUnverified;
    } else if (seen.includes(expectedOwner)) {
      iconWrap.className = 'dns-leak-icon-wrap safe';
      banner.className   = 'dns-leak-status-banner';
      statusIcon.innerHTML = iconSvg('checkCircle', 18);
      statusTxt.textContent  = t.dnsLeakSafe;
    } else {
      iconWrap.className = 'dns-leak-icon-wrap leak';
      banner.className   = 'dns-leak-status-banner leak';
      statusIcon.innerHTML = iconSvg('alertTriangle', 18);
      statusTxt.textContent  = t.dnsLeakDetected;
    }
  } catch (err) {
    loading.style.display = 'none';
    result.style.display  = 'block';
    retryBtn.style.display = 'flex';
    banner.className = 'dns-leak-status-banner leak';
    statusIcon.innerHTML = iconSvg('xCircle', 18);
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
    globalHotkeys: el<HTMLInputElement>('globalHotkeysCheckbox').checked,
    hotkeyToggle: el<HTMLInputElement>('hotkeyToggleInput').value,
    hotkeyShow: el<HTMLInputElement>('hotkeyShowInput').value,
    killSwitch: el<HTMLInputElement>('killSwitchCheckbox').checked,
    dnsLeak: el<HTMLInputElement>('dnsLeakCheckbox').checked,
    ipv6Leak: el<HTMLInputElement>('ipv6LeakCheckbox').checked,
    fakeDns: el<HTMLInputElement>('fakeDnsCheckbox').checked,
    verboseLogging: el<HTMLInputElement>('verboseLoggingCheckbox').checked,
    lastSelectedServer: activeServerLink,
    processMode: processModeHidden.value as AppSettings['processMode'],
    processListBlacklist: processListBlacklistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    processListWhitelist: processListWhitelistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    favoriteLinks: Array.from(favoriteLinks),
    profiles: profiles,
    customRules: customRules,
    connectionsView: connectionsView,
    theme: currentTheme
  };
  return window.api.saveSettings(settings);
}

// ── Профили ──────────────────────────────────────────────────────────────────
//
// Именованный набор того, что различается между сценариями: маршрутизация,
// DNS, раздельное туннелирование, защита. Всё остальное — язык, тема,
// автозапуск, горячие клавиши — принадлежит приложению, а не сценарию, и
// профилем не трогается.
//
// Хранятся профили в зашифрованной половине настроек, потому что могут нести
// ссылку на сервер; по той же причине не попадают в переносимый файл. См.
// secretSettingKeys в backend/service/settings.go.
let profiles: Profile[] = [];

/** Снимок тех полей, которые профиль запоминает. */
function currentProfileSettings(): ProfileSettings {
  return {
    dns: currentDnsSetting(),
    bypassRu: el<HTMLInputElement>('bypassRuCheckbox').checked,
    tunMode: el<HTMLInputElement>('tunModeCheckbox').checked,
    systemProxy: el<HTMLInputElement>('systemProxyCheckbox').checked,
    processMode: processModeHidden.value as AppSettings['processMode'],
    processListBlacklist: processListBlacklistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    processListWhitelist: processListWhitelistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    // Копия, а не ссылка: customRules правится на месте кнопкой «удалить», и
    // сохранённый профиль менялся бы вместе с текущими настройками.
    customRules: customRules.map(r => ({ ...r })),
    killSwitch: el<HTMLInputElement>('killSwitchCheckbox').checked,
    dnsLeak: el<HTMLInputElement>('dnsLeakCheckbox').checked,
    ipv6Leak: el<HTMLInputElement>('ipv6LeakCheckbox').checked,
    fakeDns: el<HTMLInputElement>('fakeDnsCheckbox').checked,
  };
}

/**
 * Раскладывает профиль по элементам интерфейса.
 *
 * Именно по интерфейсу, а не в объект настроек напрямую: collectAndSaveSettings
 * читает состояние с элементов, и любой другой путь означал бы второй источник
 * истины, который разойдётся с первым при следующей добавленной галке.
 */
function applyProfileSettings(p: ProfileSettings): void {
  setDnsSetting(p.dns);
  el<HTMLInputElement>('bypassRuCheckbox').checked = p.bypassRu;
  el<HTMLInputElement>('tunModeCheckbox').checked = p.tunMode;
  el<HTMLInputElement>('systemProxyCheckbox').checked = p.systemProxy;
  processListBlacklistEl.value = p.processListBlacklist.join('\n');
  processListWhitelistEl.value = p.processListWhitelist.join('\n');
  processModeHidden.value = p.processMode;
  const modeTab = document.querySelector<HTMLElement>(`.process-tab[data-mode="${p.processMode}"]`);
  modeTab?.click();
  customRules = p.customRules.map(r => ({ ...r }));
  renderCustomRules();
  el<HTMLInputElement>('killSwitchCheckbox').checked = p.killSwitch;
  el<HTMLInputElement>('dnsLeakCheckbox').checked = p.dnsLeak;
  el<HTMLInputElement>('ipv6LeakCheckbox').checked = p.ipv6Leak;
  el<HTMLInputElement>('fakeDnsCheckbox').checked = p.fakeDns;
}

function renderProfiles(): void {
  const t = translations[currentLanguage];
  const list = el('profilesList');
  list.textContent = '';
  el('profilesEmpty').style.display = profiles.length === 0 ? 'block' : 'none';

  profiles.forEach(profile => {
    const row = document.createElement('div');
    row.className = 'profile-row';

    const apply = document.createElement('button');
    apply.type = 'button';
    apply.className = 'btn-glass profile-apply';
    apply.textContent = profile.name;
    // Наличие сервера в профиле видно до нажатия: переключение, которое ещё и
    // меняет сервер, — заметно другое действие, чем смена одних маршрутов.
    apply.title = profile.server ? t.profileAppliesServer : t.profileRoutingOnly;
    apply.onclick = () => void applyProfile(profile);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'btn-glass profile-delete';
    del.textContent = t.deleteBtn;
    del.setAttribute('aria-label', `${t.deleteBtn}: ${profile.name}`);
    del.onclick = async () => {
      if (!(await showConfirm(t.profileDeleteConfirm.replace('{name}', profile.name)))) return;
      profiles = profiles.filter(p => p.id !== profile.id);
      renderProfiles();
      await collectAndSaveSettings();
      void window.api.rebuildTrayProfiles();
    };

    if (profile.server) {
      const mark = document.createElement('span');
      mark.className = 'profile-server-mark';
      mark.textContent = t.profileServerMark;
      row.append(apply, mark, del);
    } else {
      row.append(apply, del);
    }
    list.appendChild(row);
  });
}

async function applyProfile(profile: Profile): Promise<void> {
  const t = translations[currentLanguage];
  applyProfileSettings(profile.settings);

  if (profile.server) {
    activeServerLink = profile.server;
    const info = parseBasicInfo(profile.server);
    activeServerName.textContent = info.name || info.address || t.proxyFallbackName;
    activeServerDetails.textContent = `${info.type.toUpperCase()} • ${info.address}`;
    updateCards();
  }

  await collectAndSaveSettings();

  // Правила и DNS применяются ядром при сборке конфига, поэтому на работающем
  // подключении смена профиля обязана дойти до ядра — иначе трафик продолжит
  // идти по прежним правилам, а интерфейс будет показывать новые.
  if (appState === 'on') {
    restartCore();
  }
  flashConnectionsStatus(t.profileApplied.replace('{name}', profile.name));
}

el('saveProfileBtn').onclick = async () => {
  const t = translations[currentLanguage];
  const name = (await showPrompt(t.profileNamePrompt))?.trim();
  if (!name) return;

  const includeServer = el<HTMLInputElement>('profileIncludeServerCheckbox').checked;
  const profile: Profile = {
    id: Date.now().toString(),
    name,
    server: includeServer ? activeServerLink : null,
    settings: currentProfileSettings(),
  };

  // Совпадение имён — это почти всегда «перезаписать», а не «завести второй с
  // тем же именем»: два одинаковых пункта в списке различить нечем.
  const existing = profiles.findIndex(p => p.name.toLowerCase() === name.toLowerCase());
  if (existing !== -1) {
    if (!(await showConfirm(t.profileOverwriteConfirm.replace('{name}', name)))) return;
    profile.id = profiles[existing].id;
    profiles[existing] = profile;
  } else {
    profiles.push(profile);
  }

  renderProfiles();
  await collectAndSaveSettings();
  // Меню трея собрано из скрытого пула на стороне Go и о правке ничего не
  // знает: systray не умеет удалять пункты, поэтому перестроить его — это
  // отдельный вызов, а не следствие сохранения.
  void window.api.rebuildTrayProfiles();
  flashConnectionsStatus(t.profileSaved.replace('{name}', name));
};

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
  await collectAndSaveSettings();
  if (appState === 'on') {
    restartCore();
  }
});

window.api.onTrayProfileSelected((id: string) => {
  const profile = profiles.find(p => p.id === id);
  // Профиль мог быть удалён между тем, как меню собрали, и тем, как по нему
  // нажали: подменю трея перестраивается вдогонку, а не мгновенно.
  if (profile) void applyProfile(profile);
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
let appliedDns = '';

/** Значение из настроек DNS в том виде, в каком его сохраняет collectAndSaveSettings. */
function currentDnsSetting(): string {
  const select = el<HTMLSelectElement>('dnsSelect');
  if (select.value !== 'custom') return select.value;
  return el<HTMLInputElement>('customDnsInput').value.trim();
}

/**
 * Обратная currentDnsSetting: раскладывает сохранённое значение по списку и
 * полю «свой сервер».
 *
 * Была частью загрузки настроек и понадобилась вторым местом — переключением
 * профиля. Скопировать её туда значило бы завести второе описание того, как
 * значение DNS ложится на интерфейс; при первом же изменении списка серверов
 * эти два описания разошлись бы.
 */
/**
 * Показывает или прячет поле «свой DNS» вместе с его подсказкой.
 *
 * Владелец видимости обязан быть один. Раньше поле показывали два места —
 * обработчик выбора и setDnsSetting, — а подсказку разворачивал только первый.
 * У человека с уже сохранённым своим DNS поле было видно, а сообщение о
 * непригодном значении не появлялось никогда: ошибка всплывала при
 * «Подключиться», ровно так, как было до появления этой проверки.
 */
function showCustomDnsField(custom: boolean): void {
  const input = optionalEl<HTMLInputElement>('customDnsInput');
  const hint = optionalEl('customDnsHint');
  if (input) input.style.display = custom ? 'block' : 'none';
  if (hint) hint.style.display = custom ? 'block' : 'none';
}

function setDnsSetting(value: string): void {
  const select = optionalEl<HTMLSelectElement>('dnsSelect');
  if (!select || value === '') return;

  const known = Array.from(select.options).some(opt => opt.value === value);
  const customInput = optionalEl<HTMLInputElement>('customDnsInput');

  if (known) {
    select.value = value;
    showCustomDnsField(false);
  } else {
    select.value = 'custom';
    if (customInput) customInput.value = value;
    showCustomDnsField(true);
    void checkCustomDns();
  }
  // Нативный список скрыт, на экране — своя стеклянная копия, и присваивание
  // .value события change не порождает. Без этой строки подпись на кнопке
  // осталась бы от прежнего выбора.
  syncCustomSelectLabels('dnsSelect');
}

el('saveSettingsBtn').onclick = () => {
  collectAndSaveSettings();
  // Сверяем после сохранения, а не по вводу: до нажатия менять ещё нечего.
  if (currentDnsSetting() !== appliedDns) markRulesPending();
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
    appliedDns = currentDnsSetting();

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
    el<HTMLInputElement>('killSwitchCheckbox').checked = !!settings.killSwitch;
    el<HTMLInputElement>('dnsLeakCheckbox').checked = settings.dnsLeak !== undefined ? !!settings.dnsLeak : true;
    el<HTMLInputElement>('ipv6LeakCheckbox').checked = settings.ipv6Leak !== undefined ? !!settings.ipv6Leak : true;
    el<HTMLInputElement>('fakeDnsCheckbox').checked = settings.fakeDns !== undefined ? !!settings.fakeDns : true;
    el<HTMLInputElement>('verboseLoggingCheckbox').checked = !!settings.verboseLogging;
    
    if (settings.processListBlacklist) processListBlacklistEl.value = settings.processListBlacklist.join('\n');
    if (settings.processListWhitelist) processListWhitelistEl.value = settings.processListWhitelist.join('\n');
    
    if (settings.processMode) {
      processModeHidden.value = settings.processMode;
      const targetTab = document.querySelector<HTMLElement>(`.process-tab[data-mode="${settings.processMode}"]`);
      if (targetTab) targetTab.click();
    }
    
    // Не powerBtn.click(): автоподключение не должно вести к запросу UAC —
    // см. handleConnectFailure.
    if (settings.autoConnect && activeServerLink) startConnection(false);

    if (settings.favoriteLinks) {
      favoriteLinks = new Set(settings.favoriteLinks);
    }
    if (settings.profiles) {
      profiles = settings.profiles;
    }
    renderProfiles();
    void window.api.rebuildTrayProfiles();
    // persist=false: настройки сейчас читаются, записывать их обратно на этом
    // шаге значило бы сохранять полусобранный объект.
    setConnectionsView(settings.connectionsView === 'flat' ? 'flat' : 'grouped', false);

    if (settings.customRules) {
      customRules = settings.customRules;
    }
    renderCustomRules();
  } else {
    applyLanguage();
  }
  await loadSubscriptions();
  // Второй проход по вкладке: initHistory отрисовал её до того, как подписки
  // были загружены, а карточка сессии ищет по ним ссылку сервера.
  renderHistoryTab();

  // Listen to background auto-update events to hot-reload the UI server cards
  window.api.onSubscriptionsUpdated(() => {
    loadSubscriptions();
  });

  // Listen to clipboard import result
  window.api.onSubscriptionResult(async (links) => {
    if (!links || links.length === 0) {
      showAlert(
        translations[currentLanguage].alertDialogTitle,
        translations[currentLanguage].clipboardNoLinks,
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
      const name = translations[currentLanguage].clipboardSubName;
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
    emptyDiv.style.cssText = 'color: var(--text-dim); font-size: var(--fs-body); font-style: italic; padding: 8px; text-align: center; border: 1px dashed var(--glass-border); border-radius: 8px;';
    emptyDiv.textContent = translations[currentLanguage].customRulesEmpty;
    container.appendChild(emptyDiv);
    return;
  }
  
  customRules.forEach((rule, idx) => {
    const row = document.createElement('div');
    row.style.cssText = 'display: flex; justify-content: space-between; align-items: center; background: var(--glass-01); border: 1px solid var(--glass-border); padding: 8px 12px; border-radius: 8px; gap: 8px;';
    
    const infoSpan = document.createElement('span');
    infoSpan.style.cssText = 'font-size: var(--fs-body); display: flex; align-items: center; gap: 6px;';
    
    const t = translations[currentLanguage];

    // Значок действия. Форма и слово различают их сами по себе — стрелка
    // насквозь, щит, перечёркнутый круг, — а цвет только усиливает то, что уже
    // сказано. Раньше здесь стояли цветные кружки 🟢🔵🔴: их различал один
    // цвет, и на светлой теме они вдобавок оставались чужими пятнами.
    //
    // Подпись переводится. Здесь стояли литералы Direct / Proxy / Block, то
    // есть в русском интерфейсе список своих правил был наполовину английским
    // — при том что те же три действия строкой ниже, в «Соединениях», давно
    // переведены и ключи для них есть.
    let actionBadge = '';
    if (rule.action === 'direct') actionBadge = `<span class="rule-action rule-action--direct">${iconSvg('arrowRight', 13)}${escapeHtml(t.connActionDirect)}</span>`;
    else if (rule.action === 'proxy') actionBadge = `<span class="rule-action rule-action--proxy">${iconSvg('shield', 13)}${escapeHtml(t.connActionProxy)}</span>`;
    else if (rule.action === 'block') actionBadge = `<span class="rule-action rule-action--block">${iconSvg('slash', 13)}${escapeHtml(t.connActionBlock)}</span>`;

    let typeName: string = rule.type;
    if (rule.type === 'domain_suffix') typeName = t.ruleBadgeSuffix;
    else if (rule.type === 'domain') typeName = t.ruleBadgeDomain;
    else if (rule.type === 'domain_keyword') typeName = t.ruleBadgeKeyword;
    else if (rule.type === 'ip_cidr') typeName = t.ruleBadgeIp;
    else if (rule.type === 'process') typeName = t.ruleBadgeProcess;
    
    // typeName падает обратно на сырой rule.type для неизвестных значений, а
    // settings.json правится вручную — экранируем и его, не только value.
    // Класс, а не style=: CSP запрещает инлайновый атрибут стиля, а этот кусок
    // разметки собирается строкой и уезжает через innerHTML — то есть попадает
    // под запрет ровно так же, как атрибут в index.html.
    infoSpan.innerHTML = `${actionBadge} <span class="rule-type-badge">[${escapeHtml(typeName)}]</span> <strong>${escapeHtml(rule.value)}</strong>`;
    
    const delBtn = document.createElement('button');
    delBtn.className = 'btn-glass';
    delBtn.style.cssText = 'padding: 4px 8px; font-size: var(--fs-micro); color: var(--danger); border-color: rgb(var(--danger-rgb) / 0.2);';
    delBtn.textContent = translations[currentLanguage].deleteBtn;
    
    delBtn.onclick = () => {
      customRules.splice(idx, 1);
      renderCustomRules();
      collectAndSaveSettings();
      markRulesPending();
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

    upsertCustomRule({ action, type, value });
    valInput.value = '';
  };
}

// upsertCustomRule добавляет правило или переписывает действие у уже
// существующего с тем же типом и значением.
//
// Дописывать вторым правилом нельзя: GenerateConfig выдаёт правила в порядке
// массива, а sing-box берёт первое совпадение — два противоречащих правила на
// один хост молча оставили бы работать старое. В таблице «Маршруты» это ещё
// видно глазами, а при клике из списка соединений пользователь получил бы
// кнопку, которая визуально ничего не делает.
//
// Возвращает то, что произошло, чтобы вызывающая сторона могла это показать.
function upsertCustomRule(rule: CustomRule): 'added' | 'replaced' | 'unchanged' {
  const idx = customRules.findIndex(r => r.type === rule.type && r.value === rule.value);
  let outcome: 'added' | 'replaced' | 'unchanged';

  if (idx >= 0 && customRules[idx].action === rule.action) {
    outcome = 'unchanged';
  } else if (idx >= 0) {
    customRules[idx].action = rule.action;
    outcome = 'replaced';
  } else {
    customRules.push(rule);
    outcome = 'added';
  }

  if (outcome !== 'unchanged') {
    renderCustomRules();
    collectAndSaveSettings();
    markRulesPending();
  }
  return outcome;
}

// ── Отложенное применение правил ─────────────────────────────────────────────
//
// Правило записывается в settings.json сразу, а действовать начинает только
// после перезапуска ядра: конфиг sing-box генерируется один раз при старте.
// Раньше об этом не сообщалось никак — правило добавлялось, ничего не менялось,
// и выглядело это как сломанный интерфейс. Плашка объясняет разрыв и даёт
// кнопку его закрыть.
//
// Флаг живёт только в рамках сессии интерфейса и намеренно не сохраняется: на
// диске правило уже лежит, и после перезапуска приложения оно действует для
// любого нового соединения — «отложенного» не остаётся.
let rulesPending = false;

function markRulesPending() {
  rulesPending = true;
  renderRulesPending();
}

function renderRulesPending() {
  const t = translations[currentLanguage];
  const connected = appState === 'on';

  document.querySelectorAll<HTMLElement>('.rules-pending-bar').forEach(bar => {
    bar.style.display = rulesPending ? 'flex' : 'none';
    if (!rulesPending) return;

    const text = bar.querySelector<HTMLElement>('.rules-pending-text');
    if (text) text.textContent = connected ? t.rulesPendingText : t.rulesPendingOnNextConnect;

    // Перезапускать нечего, пока ядро не запущено: правило и так подхватится
    // при следующем подключении.
    const apply = bar.querySelector<HTMLButtonElement>('.rules-pending-apply');
    if (apply) {
      apply.textContent = t.rulesApplyNow;
      apply.style.display = connected ? 'inline-flex' : 'none';
    }
  });
}

document.querySelectorAll<HTMLButtonElement>('.rules-pending-apply').forEach(btn => {
  // Флаг снимается не здесь, а в onStarted: пока ядро не поднялось с новым
  // конфигом, правило по-прежнему не действует, и упавший перезапуск не должен
  // выглядеть как применённый.
  btn.onclick = () => restartCore();
});

// ── Экран «Соединения» ───────────────────────────────────────────────────────

// Дефолт самого sing-box для этого потока — и потолок: Snapshot() на стороне
// ядра вызывает runtime.ReadMemStats, а это stop-the-world в процессе, который
// в этот момент гонит трафик. Чаще опрашивать нельзя.
const CONNECTIONS_POLL_MS = 1000;
// Зеркалит maxConnectionRows в backend/service/connections.go. Здесь оно нужно
// только для проверки: строк сверх лимита прийти не должно.
const MAX_CONNECTION_ROWS = 200;

// Сгруппированный вид по умолчанию: экран задуман как панель управления
// маршрутизацией. Плоская таблица остаётся для разбора и выбирается вручную.
let connectionsView: AppSettings['connectionsView'] = 'grouped';

// Ячейки, содержимое которых меняется от тика к тику, вместе с последним, что в
// них было записано. Сравнение до присваивания дешевле, чем трогать DOM.
interface ConnectionRowHandles {
  tr: HTMLTableRowElement;
  traffic: HTMLElement;
  age: HTMLElement;
  lastTraffic: string;
  lastAge: string;
}

const connectionRowHandles = new Map<string, ConnectionRowHandles>();
let connectionsViewActive = false;
let connectionsTimer: ReturnType<typeof setInterval> | null = null;
let connectionsInFlight = false;

function connectionsShouldPoll() {
  return connectionsViewActive && uiActive && appState === 'on';
}

// syncConnectionsPolling — единственная точка, которая решает, идёт опрос или
// нет. Её зовут все, кто может изменить любое из трёх условий: навигация,
// уход окна в трей, смена состояния подключения.
function syncConnectionsPolling() {
  if (connectionsShouldPoll()) {
    if (connectionsTimer !== null) return;
    void pollConnections();
    connectionsTimer = setInterval(() => void pollConnections(), CONNECTIONS_POLL_MS);
    return;
  }

  if (connectionsTimer !== null) {
    clearInterval(connectionsTimer);
    connectionsTimer = null;
  }
  // Таблица остаётся от прошлой сессии ядра и врала бы: эти соединения давно
  // закрыты. Чистим сразу, а не при следующем открытии экрана.
  if (!connectionsViewActive || appState !== 'on') resetConnectionsView();
}

async function pollConnections() {
  // Тик, пришедший на неотвеченный запрос, пропускается: подтормозившее ядро
  // не должно копить очередь запросов, каждый из которых держит его же лок.
  if (connectionsInFlight || !connectionsShouldPoll()) return;
  connectionsInFlight = true;
  try {
    const snapshot = await window.api.getConnections();
    // Пока запрос шёл, экран могли закрыть или VPN отключить.
    if (!connectionsShouldPoll()) return;
    renderConnections(snapshot);
  } catch (e) {
    console.error('getConnections failed:', e);
  } finally {
    connectionsInFlight = false;
  }
}

function resetConnectionsView() {
  const body = optionalEl('connectionsBody');
  if (body) body.innerHTML = '';
  connectionRowHandles.clear();

  // Сгруппированный вид чистится тем же вызовом: иначе после отключения VPN на
  // экране остались бы папки от прошлой сессии ядра.
  for (const [, handles] of groupHandles) handles.root.remove();
  groupHandles.clear();
  groupOrderSignature.clear();
  // Раскрытые папки при этом сохраняются: пользователь их открыл сам, и после
  // переподключения к тому же серверу они должны остаться раскрытыми.

  renderConnectionsPlaceholder(0, 0);
}

/**
 * Переключает вид и запоминает выбор. Плоская таблица и папки живут в разных
 * контейнерах, поэтому переключение — это показ одного и скрытие другого;
 * содержимое пересоберётся на ближайшем тике опроса.
 */
function setConnectionsView(view: AppSettings['connectionsView'], persist = true) {
  connectionsView = view;

  const grouped = optionalEl('connectionsGrouped');
  const flat = optionalEl('connectionsFlatWrap');
  if (grouped) grouped.style.display = view === 'grouped' ? 'block' : 'none';
  if (flat) flat.style.display = view === 'flat' ? 'block' : 'none';

  // Селектор сужен до [data-view]. Класс .conn-view-btn носят теперь оба
  // переключателя в шапке — вид и порядок групп, — и общий селектор считал бы
  // кнопки сортировки кнопками вида: у них нет data-view, сравнение давало бы
  // false, и нажатие «по трафику» переключало бы экран на папки.
  document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-view]').forEach((btn) => {
    const active = btn.dataset.view === view;
    btn.classList.toggle('active', active);
    btn.setAttribute('aria-pressed', active ? 'true' : 'false');
  });

  if (persist) collectAndSaveSettings();
}

document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-view]').forEach((btn) => {
  btn.addEventListener('click', () => {
    setConnectionsView(btn.dataset.view === 'flat' ? 'flat' : 'grouped');
  });
});

/**
 * Порядок папок на экране «Соединения».
 *
 * Не сохраняется в настройках, в отличие от вида, и это осознанно: алфавит —
 * рабочее состояние экрана, а сортировка по трафику нужна ровно на то время,
 * пока смотрят, кто забирает канал. Запомненная между запусками, она встречала
 * бы человека переставляющимися под курсором папками, причём он бы не помнил,
 * что сам об этом просил.
 */
let connectionsSort: GroupSort = 'name';

function setConnectionsSort(sort: GroupSort) {
  connectionsSort = sort;
  document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-sort]').forEach((btn) => {
    const active = btn.dataset.sort === sort;
    btn.classList.toggle('active', active);
    btn.setAttribute('aria-pressed', active ? 'true' : 'false');
  });
  // Порядок применяется на ближайшем тике опроса, но ждать секунду после
  // нажатия незачем — перерисовываем сразу тем, что уже есть.
  if (lastConnectionsSnapshot) renderConnections(lastConnectionsSnapshot);
}

document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-sort]').forEach((btn) => {
  btn.addEventListener('click', () => {
    setConnectionsSort(btn.dataset.sort === 'traffic' ? 'traffic' : 'name');
  });
});

function renderConnectionsPlaceholder(shown: number, total: number) {
  const t = translations[currentLanguage];

  const empty = optionalEl('connectionsEmpty');
  if (empty) {
    empty.textContent = appState === 'on' ? t.connectionsEmpty : t.connectionsOffline;
    empty.style.display = shown === 0 ? 'flex' : 'none';
  }

  const count = optionalEl('connectionsCount');
  if (count) {
    count.textContent = total > shown
      ? t.connectionsShowing.replace('{shown}', String(shown)).replace('{total}', String(total))
      : '';
  }
}

// renderConnections сверяет таблицу со снимком вместо того, чтобы собирать её
// заново.
//
// Перерисовка 200 строк раз в секунду — это ровно тот случай, что уже был
// однажды с панелью логов: непрерывная сборка и выброс узлов, за которой не
// поспевает сборщик мусора WebView2. Поэтому: у знакомых строк переписываются
// только изменившиеся ячейки, пришедшие добавляются в начало, исчезнувшие
// удаляются, а строка, дожившая до следующего тика, не двигается вовсе —
// снимок приходит отсортированным по времени, новые всегда новее выживших.
// ── Сгруппированный вид ──────────────────────────────────────────────────────
//
// Две папки-секции над одним и тем же снимком: по сайту и по программе.
// Соединение попадает в обе, если у него есть и хост, и процесс.
//
// Порядок обновления тот же, что у плоской таблицы, и по той же причине:
// пересобирать всё раз в секунду нельзя. Разница в том, что свёрнутая папка не
// держит в DOM ни одной строки, поэтому обычно узлов здесь на порядок меньше,
// чем в таблице на 200 строк.

interface GroupHandles {
  root: HTMLElement;
  keyEl: HTMLElement;
  countEl: HTMLElement;
  trafficEl: HTMLElement;
  members: HTMLElement;
  lastCount: string;
  lastTraffic: string;
  lastMembers: string;
}

type GroupKind = 'site' | 'process';

const groupHandles = new Map<string, GroupHandles>();
// Раскрытые папки переживают тики: пересборка списка не должна схлопывать то,
// что пользователь только что открыл.
const expandedGroups = new Set<string>();
// Подпись упорядоченного списка ключей. Пока она не меняется, порядок узлов в
// DOM не трогается вовсе — иначе перестановка на каждом тике сбрасывала бы
// фокус с кнопки, на которую пользователь только что перешёл клавишей.
const groupOrderSignature = new Map<GroupKind, string>();

/** Правило по программе применимо только в TUN — см. GenerateConfig. */
function processRulesAvailable(): boolean {
  const box = optionalEl<HTMLInputElement>('tunModeCheckbox');
  return box ? box.checked : false;
}

function groupRuleTarget(kind: GroupKind, key: string): CustomRule | null {
  if (kind === 'site') return { action: 'direct', type: 'domain_suffix', value: key };
  return { action: 'direct', type: 'process', value: key };
}

function applyRule(rule: CustomRule) {
  const t = translations[currentLanguage];
  const outcome = upsertCustomRule(rule);
  flashConnectionsStatus(
    outcome === 'unchanged' ? t.ruleAlreadyExists
      : outcome === 'replaced' ? t.ruleReplaced
      : t.ruleAdded,
  );
}

/** Тройка действий: напрямую / через VPN / заблокировать. */
function buildRuleActions(
  base: CustomRule,
  disabledReason: string | null,
  compact: boolean,
): HTMLElement {
  const t = translations[currentLanguage];
  const wrap = document.createElement('div');
  wrap.className = compact ? 'conn-group-actions conn-group-actions--compact' : 'conn-group-actions';

  const add = (action: CustomRule['action'], className: string, label: string) => {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = `conn-action ${className}`;
    btn.textContent = label;
    if (disabledReason) {
      btn.disabled = true;
      btn.title = disabledReason;
    } else {
      btn.title = `${label}: ${base.value}`;
      btn.onclick = (e) => {
        e.stopPropagation();
        applyRule({ ...base, action });
      };
    }
    wrap.appendChild(btn);
  };

  add('direct', 'conn-action-direct', t.connActionDirect);
  add('proxy', 'conn-action-proxy', t.connActionProxy);
  add('block', 'conn-action-block', t.connActionBlock);
  return wrap;
}

function setGroupExpanded(handles: GroupHandles, id: string, expanded: boolean) {
  const t = translations[currentLanguage];
  handles.root.dataset.expanded = expanded ? 'true' : 'false';
  handles.members.style.display = expanded ? 'flex' : 'none';
  const toggle = handles.root.querySelector<HTMLButtonElement>('.conn-group-toggle');
  if (toggle) {
    toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    toggle.title = expanded ? t.connGroupCollapse : t.connGroupExpand;
  }
  if (expanded) expandedGroups.add(id); else expandedGroups.delete(id);
}

function buildGroup(kind: GroupKind, id: string, group: ConnectionGroup): GroupHandles {
  const t = translations[currentLanguage];

  const root = document.createElement('div');
  root.className = 'conn-group';
  root.dataset.expanded = 'false';

  const head = document.createElement('div');
  head.className = 'conn-group-head';

  const toggle = document.createElement('button');
  toggle.type = 'button';
  toggle.className = 'conn-group-toggle';
  toggle.setAttribute('aria-expanded', 'false');
  toggle.title = t.connGroupExpand;

  const chevron = document.createElement('span');
  chevron.className = 'conn-group-chevron';
  chevron.setAttribute('aria-hidden', 'true');
  chevron.textContent = '▶';

  const keyEl = document.createElement('span');
  keyEl.className = 'conn-group-key';
  keyEl.textContent = group.key;

  const countEl = document.createElement('span');
  countEl.className = 'conn-group-count';

  const trafficEl = document.createElement('span');
  trafficEl.className = 'conn-group-traffic';

  toggle.append(chevron, keyEl, countEl, trafficEl);

  const members = document.createElement('div');
  members.className = 'conn-group-members';
  members.style.display = 'none';
  const membersId = `conn-members-${kind}-${group.key.replace(/[^\w.-]/g, '_')}`;
  members.id = membersId;
  toggle.setAttribute('aria-controls', membersId);

  const handles: GroupHandles = {
    root, keyEl, countEl, trafficEl, members,
    lastCount: '', lastTraffic: '', lastMembers: '',
  };

  toggle.onclick = () => setGroupExpanded(handles, id, root.dataset.expanded !== 'true');

  head.appendChild(toggle);
  const base = groupRuleTarget(kind, group.key);
  if (base) {
    const blocked = kind === 'process' && !processRulesAvailable();
    head.appendChild(buildRuleActions(base, blocked ? t.connProcessRuleNeedsTun : null, false));
  }

  root.append(head, members);
  return handles;
}

function renderGroupMembers(handles: GroupHandles, group: ConnectionGroup, now: number) {
  const t = translations[currentLanguage];
  // Подпись содержимого: пока она та же, узлы не пересобираются. Иначе список
  // перестраивался бы раз в секунду ради неизменившихся цифр.
  const signature = group.rows
    .map((r) => `${r.id}|${r.upload}|${r.download}`)
    .join(';');
  if (signature === handles.lastMembers) return;
  handles.lastMembers = signature;

  handles.members.textContent = '';
  for (const row of group.rows) {
    const item = document.createElement('div');
    item.className = 'conn-member';

    const host = document.createElement('span');
    host.className = 'conn-member-host';
    const label = row.host || row.destIP || t.connNoHost;
    host.textContent = row.process && row.host ? `${label} — ${row.process}` : label;
    host.title = `${label}${row.destIP ? ` · ${row.destIP}` : ''}`;

    const meta = document.createElement('span');
    meta.className = 'conn-member-meta';
    meta.textContent = `↑ ${formatBytes(row.upload)}  ↓ ${formatBytes(row.download)}  ·  ${formatConnectionAge(now - row.startMs)}`;

    item.append(host, meta);

    // Действие на отдельном соединении: правило уже посчитано бэкендом
    // (SuggestRuleTarget), и оно точнее группового — домен целиком против
    // одного хоста.
    if (row.ruleType) {
      item.appendChild(buildRuleActions(
        { action: 'direct', type: row.ruleType as CustomRule['type'], value: row.ruleValue },
        null,
        true,
      ));
    }

    handles.members.appendChild(item);
  }
}

function renderGroupSection(
  kind: GroupKind,
  listId: string,
  emptyId: string,
  groups: ConnectionGroup[],
  now: number,
) {
  const list = optionalEl(listId);
  const empty = optionalEl(emptyId);
  if (!list) return;

  if (empty) empty.style.display = groups.length === 0 ? 'block' : 'none';

  const seen = new Set<string>();
  for (const group of groups) {
    const id = `${kind}:${group.key}`;
    seen.add(id);

    let handles = groupHandles.get(id);
    if (!handles) {
      handles = buildGroup(kind, id, group);
      groupHandles.set(id, handles);
      list.appendChild(handles.root);
      // Папка могла быть раскрыта до того, как исчезла и появилась снова.
      if (expandedGroups.has(id)) setGroupExpanded(handles, id, true);
    }

    const count = translations[currentLanguage].connGroupCount.replace('{n}', String(group.rows.length));
    if (handles.lastCount !== count) {
      handles.countEl.textContent = count;
      handles.lastCount = count;
    }
    const traffic = `↑ ${formatBytes(group.upload)}  ↓ ${formatBytes(group.download)}`;
    if (handles.lastTraffic !== traffic) {
      handles.trafficEl.textContent = traffic;
      handles.lastTraffic = traffic;
    }

    if (handles.root.dataset.expanded === 'true') {
      renderGroupMembers(handles, group, now);
    } else if (handles.lastMembers !== '') {
      // Свёрнутая папка не держит строк в DOM — ради этого группировка и
      // затевалась.
      handles.members.textContent = '';
      handles.lastMembers = '';
    }
  }

  for (const [id, handles] of groupHandles) {
    if (!id.startsWith(`${kind}:`) || seen.has(id)) continue;
    handles.root.remove();
    groupHandles.delete(id);
  }

  // Порядок правится только когда состав действительно изменился.
  const signature = groups.map((g) => g.key).join(' ');
  if (groupOrderSignature.get(kind) !== signature) {
    groupOrderSignature.set(kind, signature);
    for (const group of groups) {
      const handles = groupHandles.get(`${kind}:${group.key}`);
      if (handles) list.appendChild(handles.root);
    }
  }
}

function renderConnectionsGrouped(rows: ConnectionRow[]) {
  const now = Date.now();
  const { sites, programs } = groupConnections(rows, connectionsSort);
  renderGroupSection('site', 'connSitesList', 'connSitesEmpty', sites, now);
  renderGroupSection('process', 'connProgramsList', 'connProgramsEmpty', programs, now);

  // Пустой срез программ вне TUN — не поломка, а следствие режима. Говорим об
  // этом прямо, иначе выглядит как потерянные данные.
  const programsEmpty = optionalEl('connProgramsEmpty');
  if (programsEmpty && programs.length === 0) {
    const t = translations[currentLanguage];
    programsEmpty.textContent = processRulesAvailable() ? t.connGroupProgramsEmpty : t.connGroupProgramsNeedTun;
  }
}

// Последний снимок держится ради смены порядка групп: она обязана быть видна
// сразу, а не на следующем тике опроса через секунду.
let lastConnectionsSnapshot: ConnectionsSnapshot | null = null;

function renderConnections(snapshot: ConnectionsSnapshot) {
  lastConnectionsSnapshot = snapshot;
  const rowsAll = (snapshot.connections ?? []).slice(0, MAX_CONNECTION_ROWS);
  if (connectionsView === 'grouped') {
    renderConnectionsGrouped(rowsAll);
    renderConnectionsPlaceholder(rowsAll.length, snapshot.total ?? rowsAll.length);
    return;
  }

  const body = optionalEl<HTMLTableSectionElement>('connectionsBody');
  if (!body) return;

  const rows = (snapshot.connections ?? []).slice(0, MAX_CONNECTION_ROWS);
  const seen = new Set<string>();
  const arriving = document.createDocumentFragment();
  const now = Date.now();

  for (const row of rows) {
    seen.add(row.id);
    const traffic = `↑ ${formatBytes(row.upload)}  ↓ ${formatBytes(row.download)}`;
    const age = formatConnectionAge(now - row.startMs);

    const known = connectionRowHandles.get(row.id);
    if (known) {
      if (known.lastTraffic !== traffic) {
        known.traffic.textContent = traffic;
        known.lastTraffic = traffic;
      }
      if (known.lastAge !== age) {
        known.age.textContent = age;
        known.lastAge = age;
      }
      continue;
    }

    const handles = buildConnectionRow(row, traffic, age);
    connectionRowHandles.set(row.id, handles);
    arriving.appendChild(handles.tr);
  }

  for (const [id, handles] of connectionRowHandles) {
    if (seen.has(id)) continue;
    handles.tr.remove();
    connectionRowHandles.delete(id);
  }

  if (arriving.childNodes.length > 0) body.prepend(arriving);
  renderConnectionsPlaceholder(rows.length, snapshot.total ?? rows.length);
}

function buildConnectionRow(row: ConnectionRow, traffic: string, age: string): ConnectionRowHandles {
  const t = translations[currentLanguage];
  const tr = document.createElement('tr');
  tr.className = 'conn-row';

  // Каждая ячейка собирается через textContent. host, process и rule приходят
  // прямо из сети — это не то место, где стоит повторять innerHTML с
  // экранированием, как сделано в renderCustomRules.
  const cell = (className: string, text: string, title?: string) => {
    const td = document.createElement('td');
    td.className = className;
    td.textContent = text;
    if (title) td.title = title;
    tr.appendChild(td);
    return td;
  };

  cell('conn-cell-host', row.host || t.connNoHost, row.host);
  cell('conn-cell-dest', row.destPort ? `${row.destIP}:${row.destPort}` : row.destIP);
  cell('conn-cell-process', row.process, row.process);
  cell('conn-cell-rule', row.outbound ? `${row.rule} → ${row.outbound}` : row.rule,
    row.outbound ? `${row.rule} → ${row.outbound}` : row.rule);
  const trafficCell = cell('conn-cell-traffic', traffic);
  const ageCell = cell('conn-cell-age', age);

  const actions = document.createElement('td');
  actions.className = 'conn-cell-actions';

  const ruleAction = (action: CustomRule['action'], className: string, label: string) => {
    const btn = document.createElement('button');
    btn.className = `conn-action ${className}`;
    btn.textContent = label;
    if (row.ruleType === '') {
      // Ни хоста, ни адреса, для которого можно написать безопасное правило:
      // для приватных адресов и диапазона FakeIP оно вытеснило бы правила, на
      // которых держится туннель. См. core.SuggestRuleTarget.
      btn.disabled = true;
      btn.title = t.connActionUnavailable;
    } else {
      btn.title = `${label}: ${row.ruleValue}`;
      btn.onclick = () => {
        const outcome = upsertCustomRule({ action, type: row.ruleType as CustomRule['type'], value: row.ruleValue });
        flashConnectionsStatus(
          outcome === 'unchanged' ? t.ruleAlreadyExists
            : outcome === 'replaced' ? t.ruleReplaced
            : t.ruleAdded
        );
      };
    }
    actions.appendChild(btn);
  };

  ruleAction('direct', 'conn-action-direct', t.connActionDirect);
  ruleAction('proxy', 'conn-action-proxy', t.connActionProxy);
  ruleAction('block', 'conn-action-block', t.connActionBlock);

  const closeBtn = document.createElement('button');
  closeBtn.className = 'conn-action conn-action-close';
  closeBtn.innerHTML = iconSvg('x', 13);
  closeBtn.title = t.connActionClose;
  closeBtn.onclick = () => {
    // Строка убирается сразу, не дожидаясь подтверждения: следующий тик всё
    // равно приведёт таблицу к тому, что на самом деле у ядра.
    tr.remove();
    connectionRowHandles.delete(row.id);
    void window.api.closeConnection(row.id);
  };
  actions.appendChild(closeBtn);

  tr.appendChild(actions);
  return { tr, traffic: trafficCell, age: ageCell, lastTraffic: traffic, lastAge: age };
}

// Возраст соединения в моноширинной колонке: mm:ss, а за час — h:mm:ss.
function formatConnectionAge(ms: number) {
  const total = Math.max(0, Math.floor(ms / 1000));
  const s = String(total % 60).padStart(2, '0');
  const m = Math.floor(total / 60) % 60;
  const h = Math.floor(total / 3600);
  return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${s}` : `${String(m).padStart(2, '0')}:${s}`;
}

let connectionsFlashTimer: ReturnType<typeof setTimeout> | null = null;

// Короткое подтверждение действия в строке. Правило может оказаться уже
// существующим или переписать прежнее — без ответа клик по кнопке выглядел бы
// как промах.
function flashConnectionsStatus(message: string) {
  const node = optionalEl('connectionsFlash');
  if (!node) return;
  node.textContent = message;
  node.style.display = 'inline';
  if (connectionsFlashTimer !== null) clearTimeout(connectionsFlashTimer);
  connectionsFlashTimer = setTimeout(() => {
    node.style.display = 'none';
    connectionsFlashTimer = null;
  }, 2500);
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
    bestServerBtnText.textContent = translations[currentLanguage].bestServerSearching;

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
          const confirmMsg = translations[currentLanguage].bestServerSwitchConfirm
            .replace('{name}', info.name);

          const confirmed = await showConfirm(confirmMsg);
          if (!confirmed) {
            return;
          }
        } else if (wasActive && !isNewServer) {
          showAlert(
            translations[currentLanguage].alertDialogTitle,
            translations[currentLanguage].bestServerAlready,
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
        showAlert(translations[currentLanguage].alertDialogTitle, translations[currentLanguage].bestServerFailed, false, translations[currentLanguage]);
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
      showAlert(translations[currentLanguage].alertDialogTitle, translations[currentLanguage].logsEmptyAlert, false, translations[currentLanguage]);
      return;
    }
    const path = await window.api.saveLogs(rawLogs);
    if (path) {
      const confirmed = await showConfirm(translations[currentLanguage].logsSavedConfirm.replace('{path}', path));
      if (confirmed) {
        window.api.openLogsFolder();
      }
    } else {
      showAlert(translations[currentLanguage].errorDialogTitle, translations[currentLanguage].logsSaveFailed, true, translations[currentLanguage]);
    }
  };
}

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

// ── ВНЕШНИЙ ВИД ──────────────────────────────────────────────────────────────
//
// Выбор человека — акцент и фон; всё остальное выводит modules/theme.ts. Здесь
// только связь этого выбора с полями и с сохранением.
//
// Предпросмотра как отдельного элемента нет намеренно: тема применяется к
// живому приложению, и образец размером с ноготь показал бы меньше, чем экран,
// на который человек и так смотрит.

/** Тема, из которой собран текущий вид. Пишется только через setTheme. */
function applyPendingTheme(spec: ThemeSpec): void {
  currentTheme = spec;
  applyTheme(spec);
  try {
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify(spec));
  } catch {
    // Приватный режим или переполненное хранилище: тема применилась, а
    // без вспышки при следующем запуске переживём — settings.json на месте.
  }
}

// Цвет тянут за ползунком системного диалога, то есть событие input приходит
// десятками в секунду. Применение стоит перерисовки всего окна, а запись в
// settings.json — обращения к диску: первое привязано к кадру, второе ждёт
// паузы.
let themeFrame = 0;
let themeSaveTimer: ReturnType<typeof setTimeout> | null = null;

function setTheme(spec: ThemeSpec, options: { persist?: boolean; syncControls?: boolean } = {}): void {
  const { persist = true, syncControls = true } = options;

  currentTheme = spec;
  cancelAnimationFrame(themeFrame);
  themeFrame = requestAnimationFrame(() => applyPendingTheme(spec));

  if (syncControls) renderThemeControls();
  renderThemeReport();
  markActivePreset();

  if (!persist) return;
  if (themeSaveTimer) clearTimeout(themeSaveTimer);
  themeSaveTimer = setTimeout(() => {
    themeSaveTimer = null;
    void collectAndSaveSettings();
  }, 500);
}

/** Крошечный градиент для плитки пресета — тот же, что уйдёт на фон. */
function presetPreview(spec: ThemeSpec): { background: string; accent: string } {
  const tokens = deriveTokens(spec);
  return {
    background: tokens['--bg-image'] === 'none' ? tokens['--bg-color'] : tokens['--bg-image'],
    accent: `rgb(${tokens['--accent-rgb']})`,
  };
}

function renderThemePresets(): void {
  const host = optionalEl('themePresets');
  if (!host) return;
  const t = translations[currentLanguage];

  host.textContent = '';
  for (const preset of PRESETS) {
    const preview = presetPreview(preset.spec);
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'theme-swatch';
    button.dataset.preset = preset.id;
    button.setAttribute('role', 'radio');
    button.setAttribute('aria-checked', 'false');
    button.style.background = preview.background;

    const accent = document.createElement('span');
    accent.className = 'theme-swatch-accent';
    accent.style.background = preview.accent;
    button.appendChild(accent);

    const name = document.createElement('span');
    name.className = 'theme-swatch-name';
    name.textContent = translate(t, preset.labelKey as keyof Translations);
    button.appendChild(name);

    button.onclick = () => setTheme(structuredClone(preset.spec));
    host.appendChild(button);
  }

  markActivePreset();
}

/**
 * Совпадают ли две темы.
 *
 * Сравнение по полям, а не по JSON.stringify: тема возвращается с диска через
 * Go, где она лежит в map[string]interface{}, а тот выкладывает ключи по
 * алфавиту. Строки не совпали бы никогда после первого же перезапуска, и
 * выбранный пресет переставал бы подсвечиваться.
 */
function sameTheme(a: ThemeSpec, b: ThemeSpec): boolean {
  const hex = (v: string) => v.trim().toLowerCase();
  return (
    a.scheme === b.scheme &&
    hex(a.accent) === hex(b.accent) &&
    a.bg.mode === b.bg.mode &&
    a.bg.angle === b.bg.angle &&
    a.bg.stops.length === b.bg.stops.length &&
    a.bg.stops.every((stop, i) => hex(stop) === hex(b.bg.stops[i]))
  );
}

/** Одна из плиток отмечена, только если тема совпадает с ней целиком. */
function markActivePreset(): void {
  const host = optionalEl('themePresets');
  if (!host) return;
  for (const button of Array.from(host.querySelectorAll<HTMLButtonElement>('.theme-swatch'))) {
    const preset = PRESETS.find((p) => p.id === button.dataset.preset);
    button.setAttribute('aria-checked', preset && sameTheme(preset.spec, currentTheme) ? 'true' : 'false');
  }
}

/** Приводит поля в соответствие с текущей темой. */
function renderThemeControls(): void {
  const accentInput = optionalEl<HTMLInputElement>('themeAccentInput');
  const accentHex = optionalEl<HTMLInputElement>('themeAccentHex');
  if (accentInput) accentInput.value = currentTheme.accent;
  if (accentHex && accentHex !== document.activeElement) accentHex.value = currentTheme.accent.toUpperCase();

  // Селекторы по data-атрибуту, а не по классу .theme-mode: класс носят оба
  // переключателя — и режим, и форма фона, — и общий селектор снимал бы
  // отметку с одного, синхронизируя другой.
  for (const button of Array.from(document.querySelectorAll<HTMLButtonElement>('[data-scheme]'))) {
    button.setAttribute('aria-checked', button.dataset.scheme === currentTheme.scheme ? 'true' : 'false');
  }
  for (const button of Array.from(document.querySelectorAll<HTMLButtonElement>('[data-mode]'))) {
    button.setAttribute('aria-checked', button.dataset.mode === currentTheme.bg.mode ? 'true' : 'false');
  }

  // Угол есть только у линейного градиента: у сплошного его нет вовсе, а у
  // радиального направление задаёт не он.
  const angleField = optionalEl('themeAngleField');
  if (angleField) angleField.style.display = currentTheme.bg.mode === 'linear' ? 'flex' : 'none';
  const angle = optionalEl<HTMLInputElement>('themeAngle');
  if (angle) angle.value = String(currentTheme.bg.angle);
  setText('themeAngleValue', `${currentTheme.bg.angle}°`);

  renderThemeStops();

  const add = optionalEl<HTMLButtonElement>('themeStopAdd');
  const remove = optionalEl<HTMLButtonElement>('themeStopRemove');
  // Один стоп — это уже сплошная заливка, три — предел: четвёртый цвет на
  // ширине окна перестаёт читаться как переход и начинает читаться как полосы.
  if (add) add.disabled = currentTheme.bg.stops.length >= 3 || currentTheme.bg.mode === 'solid';
  if (remove) remove.disabled = currentTheme.bg.stops.length <= 1;
}

function renderThemeStops(): void {
  const host = optionalEl('themeStops');
  if (!host) return;
  const t = translations[currentLanguage];

  // Сплошной фон рисуется первым стопом; остальные поля прячутся, но из темы
  // не пропадают — вернувшись к градиенту, человек находит свои цвета на месте.
  const shown = currentTheme.bg.mode === 'solid' ? 1 : currentTheme.bg.stops.length;

  host.textContent = '';
  for (let i = 0; i < shown; i++) {
    const input = document.createElement('input');
    input.type = 'color';
    input.className = 'color-well';
    input.value = currentTheme.bg.stops[i];
    input.setAttribute('aria-label', translate(t, 'themeStopAria').replace('{n}', String(i + 1)));
    input.oninput = () => {
      const stops = [...currentTheme.bg.stops];
      stops[i] = input.value;
      // syncControls: false — перерисовка поля во время перетаскивания
      // отобрала бы у него фокус и остановила бы сам жест.
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, stops } }, { syncControls: false });
    };
    host.appendChild(input);
  }
}

function renderThemeReport(): void {
  const line = optionalEl('themeContrast');
  if (!line) return;
  const t = translations[currentLanguage];
  const report = describeTheme(currentTheme);
  const round = (v: number) => v.toFixed(2);

  const parts = [
    translate(t, report.passesAA ? 'themeContrastOk' : 'themeContrastLow')
      .replace('{text}', round(report.textContrast))
      .replace('{accent}', round(report.accentContrast))
      .replace('{signal}', round(report.signalContrast)),
  ];
  if (report.backgroundAdjusted) {
    parts.push(translate(t, report.scheme === 'dark' ? 'themeAdjustedBgDark' : 'themeAdjustedBgLight'));
  }
  if (report.accentAdjusted) {
    parts.push(translate(t, 'themeAdjustedAccent').replace('{hex}', report.accentApplied.toUpperCase()));
  }

  line.textContent = parts.join(' ');
  line.classList.toggle(
    'theme-contrast--adjusted',
    !report.passesAA || report.backgroundAdjusted || report.accentAdjusted,
  );
}

{
  const accentInput = optionalEl<HTMLInputElement>('themeAccentInput');
  if (accentInput) {
    accentInput.oninput = () => {
      setTheme({ ...currentTheme, accent: accentInput.value }, { syncControls: false });
      const hex = optionalEl<HTMLInputElement>('themeAccentHex');
      if (hex) hex.value = accentInput.value.toUpperCase();
    };
  }

  const accentHex = optionalEl<HTMLInputElement>('themeAccentHex');
  if (accentHex) {
    accentHex.oninput = () => {
      const value = accentHex.value.trim();
      // Пока набирают «#3», это ещё не цвет. Молчим до полного кода вместо
      // того, чтобы мигать палитрой на каждом символе.
      if (!/^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(value)) return;
      setTheme({ ...currentTheme, accent: value.startsWith('#') ? value : `#${value}` }, { syncControls: false });
      const well = optionalEl<HTMLInputElement>('themeAccentInput');
      if (well) well.value = currentTheme.accent;
    };
    // Ушли из поля с недописанным кодом — возвращаем в него то, что реально
    // применено, а не оставляем обрывок.
    accentHex.onblur = () => renderThemeControls();
  }

  for (const button of Array.from(document.querySelectorAll<HTMLButtonElement>('[data-scheme]'))) {
    button.onclick = () => {
      setTheme({ ...currentTheme, scheme: button.dataset.scheme as Scheme });
    };
  }

  for (const button of Array.from(document.querySelectorAll<HTMLButtonElement>('[data-mode]'))) {
    button.onclick = () => {
      const mode = button.dataset.mode as BackgroundMode;
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, mode } });
    };
  }

  const angle = optionalEl<HTMLInputElement>('themeAngle');
  if (angle) {
    angle.oninput = () => {
      setTheme(
        { ...currentTheme, bg: { ...currentTheme.bg, angle: Number(angle.value) } },
        { syncControls: false },
      );
      setText('themeAngleValue', `${angle.value}°`);
    };
  }

  const add = optionalEl<HTMLButtonElement>('themeStopAdd');
  if (add) {
    add.onclick = () => {
      const stops = [...currentTheme.bg.stops];
      if (stops.length >= 3) return;
      // Новый стоп — копия последнего: переход из цвета в самого себя не
      // меняет картинку, поэтому человек видит ровно тот фон, что и до нажатия,
      // и правит новый цвет осознанно, а не разгребает случайный.
      stops.push(stops[stops.length - 1]);
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, stops } });
    };
  }

  const remove = optionalEl<HTMLButtonElement>('themeStopRemove');
  if (remove) {
    remove.onclick = () => {
      const stops = currentTheme.bg.stops.slice(0, -1);
      if (stops.length < 1) return;
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, stops } });
    };
  }

  const reset = optionalEl<HTMLButtonElement>('themeResetBtn');
  if (reset) reset.onclick = () => setTheme(structuredClone(DEFAULT_THEME));
}

// ── ПЕРЕНОС НАСТРОЕК ─────────────────────────────────────────────────────────
//
// Экспортируется только открытая половина настроек: подписки и избранное — это
// ссылки с учётными данными, они шифруются и привязаны к машине через DPAPI.
// Отбор делает бэкенд, здесь только диалоги и уведомления.

el('exportSettingsBtn').onclick = async () => {
  const t = translations[currentLanguage];
  try {
    // Сначала сохраняем то, что человек мог наменять и не нажать «Сохранить
    // всё»: иначе в файл уехало бы прошлое состояние, а на экране было бы
    // текущее.
    collectAndSaveSettings();
    const path = await window.api.exportSettings();
    if (!path) return; // диалог закрыли — это не ошибка
    showAlert(t.alertDialogTitle, t.exportSettingsDone.replace('{path}', path), false, t);
  } catch (e) {
    console.error('exportSettings failed:', e);
    showAlert(t.errorDialogTitle, `${t.exportSettingsFailed}: ${e}`, true, t);
  }
};

el('importSettingsBtn').onclick = async () => {
  const t = translations[currentLanguage];
  if (!(await showConfirm(t.importSettingsConfirm))) return;
  try {
    const path = await window.api.importSettings();
    if (!path) return;
    await showAlert(t.alertDialogTitle, t.importSettingsDone, false, t);
    // Перезагрузка окна вместо расстановки два десятка контролов вручную:
    // разбор настроек в элементы интерфейса живёт в пути инициализации, и
    // повторять его здесь значило бы завести вторую копию, которая разойдётся
    // с первой. Ядро при этом не перезапускается — оно в Go.
    window.location.reload();
  } catch (e) {
    console.error('importSettings failed:', e);
    showAlert(t.errorDialogTitle, `${t.importSettingsFailed}: ${e}`, true, t);
  }
};

// ── KILL SWITCH: ВИДИМОСТЬ СОСТОЯНИЯ ─────────────────────────────────────────
//
// Правила брандмауэра переживают процесс, который их поставил. Пока состояние
// жило только галкой в «Настройках», у пользователя не было ни одного способа
// связать пропавший интернет с приложением — а именно приложение его и резало.
// Здесь состояние спрашивается у бэкенда и показывается на «Главной».

async function refreshKillSwitchBadge(): Promise<void> {
  const badge = optionalEl('killSwitchBadge');
  if (!badge) return;

  let state;
  try {
    state = await window.api.getKillSwitchState();
  } catch (e) {
    console.error('getKillSwitchState failed:', e);
    return;
  }

  const t = translations[currentLanguage];
  killSwitchArmed = state.active;

  badge.style.display = state.active ? 'flex' : 'none';
  badge.classList.toggle('status-badge--danger', state.stuck);
  badge.classList.toggle('status-badge--warning', !state.stuck);
  setText('killSwitchBadgeText', state.stuck ? t.killSwitchStuck : t.killSwitchBadge);
  badge.title = state.stuck ? t.killSwitchStuckAlert : t.killSwitchBadgeTitle;

  // Застрявшие правила — худшее состояние продукта: машина без сети,
  // приложение даже не подключено, причина видна только в crash-логе. Здесь
  // она произносится вслух, вместе с тем, что делать.
  if (state.stuck && !killSwitchStuckReported) {
    killSwitchStuckReported = true;
    badge.style.display = 'flex';
    showAlert(t.errorDialogTitle, t.killSwitchStuckAlert, true, t);
  }
}

// Состояние меняется на подключении и отключении, а застрявшие правила видны
// уже при старте — поэтому опрос и там, и там.
void refreshKillSwitchBadge();
window.api.onStarted(() => void refreshKillSwitchBadge());
window.api.onStopped(() => void refreshKillSwitchBadge());

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
    statusText.textContent = killSwitchArmed ? t.watchdogNoNetworkKillSwitch : t.watchdogNoNetwork;
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

let qrModalRelease: (() => void) | null = null;

function closeQrModal() {
  stopQrCamera();
  qrModalOverlay.style.display = 'none';
  qrModalRelease?.();
  qrModalRelease = null;
}

importQrBtn.onclick = () => {
  const t = translations[currentLanguage];
  qrPlaceholderText.textContent = t.qrPlaceholderText;
  qrModalOverlay.style.display = 'flex';
  qrModalRelease = trapFocus(qrModalOverlay, el('qrStartCameraBtn'), closeQrModal);
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
      const name = translations[currentLanguage].qrSubName;
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
    const custom = (e.target as HTMLSelectElement).value === 'custom';
    showCustomDnsField(custom);
    if (custom) void checkCustomDns();
};

// Проверка «своего DNS» на месте ввода.
//
// Раньше непригодное значение вскрывалось только при нажатии «Подключиться»,
// ошибкой сборки конфига — пользователь успевал закрыть настройки и не понимал,
// при чём тут DNS. Проверяет бэкенд тем же кодом, что собирает конфиг, поэтому
// поле не может принять то, на чём подключение потом упадёт.
//
// Поле при этом не блокируется: настройку можно сохранить и починить позже.
const DNS_CHECK_DEBOUNCE_MS = 400;
let dnsCheckTimer: ReturnType<typeof setTimeout> | null = null;

async function checkCustomDns(): Promise<void> {
  const input = optionalEl<HTMLInputElement>('customDnsInput');
  const hint = optionalEl('customDnsHint');
  if (!input || !hint) return;

  const t = translations[currentLanguage];
  const value = input.value.trim();

  // Пустое поле — ещё не ошибка, человек только начал печатать.
  if (value === '') {
    hint.textContent = t.dnsCustomHint;
    hint.classList.remove('field-hint--invalid');
    return;
  }

  let problem = '';
  try {
    problem = await window.api.validateDns(value);
  } catch (e) {
    console.error('validateDns failed:', e);
    return;
  }

  hint.textContent = problem || t.dnsCustomHint;
  hint.classList.toggle('field-hint--invalid', problem !== '');
}

const customDnsInput = optionalEl<HTMLInputElement>('customDnsInput');
if (customDnsInput) {
  customDnsInput.addEventListener('input', () => {
    if (dnsCheckTimer !== null) clearTimeout(dnsCheckTimer);
    dnsCheckTimer = setTimeout(() => {
      dnsCheckTimer = null;
      void checkCustomDns();
    }, DNS_CHECK_DEBOUNCE_MS);
  });
}

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
  el('updateSubBtn').textContent = translations[currentLanguage].updateSubBtnBusy;
  
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
el<HTMLInputElement>('bypassRuCheckbox').onchange = () => {
  collectAndSaveSettings();
  // Галка попадает в конфиг только при запуске ядра, ровно как кастомные
  // правила. Раньше об этом говорила кнопка «Сохранить маршруты» — теперь
  // говорит та же плашка, что и у правил, на этой же вкладке.
  markRulesPending();
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

/**
 * Переносит подписи с нативных <option> на уже построенный стеклянный список.
 * Возвращает false, если списка ещё нет — тогда его надо собрать.
 *
 * Существует потому, что стеклянный список — это КОПИЯ текстов, снятая в момент
 * сборки, а не живое отражение <select>. Смена языка переписывает нативные
 * <option>, но копию не трогала, и на «Маршрутах» подпись кнопки оставалась на
 * прежнем языке: в английском интерфейсе висели «Напрямую» и «.суффикс домена».
 * Заметно это было только на кнопке, потому что она видна всегда, — но и весь
 * раскрытый список отставал ровно так же.
 *
 * Тексты подменяются на месте, а не пересборкой: на каждом <div> висит onclick,
 * замкнутый на свой <option>, и пересборка стоила бы их всех на ровном месте.
 */
function syncCustomSelectLabels(selectId: string): boolean {
  const select = optionalEl<HTMLSelectElement>(selectId);
  if (!select) return false;

  const wrapper = select.nextElementSibling;
  if (!wrapper || !wrapper.classList.contains('custom-select-wrapper')) return false;

  const trigger = wrapper.querySelector('.custom-select-trigger');
  const selectedOption = select.options[select.selectedIndex];
  if (trigger && selectedOption) {
    trigger.textContent = selectedOption.textContent;
  }

  wrapper.querySelectorAll('.custom-select-option').forEach((optDiv, idx) => {
    const opt = select.options[idx];
    if (opt) optDiv.textContent = opt.textContent;
  });
  return true;
}

// Function to convert native <select> elements to custom glassmorphic dropdowns
function makeSelectCustom(selectId: string) {
  const select = optionalEl<HTMLSelectElement>(selectId);
  if (!select) return;

  // If already customized, skip but re-read the labels off the native options
  if (syncCustomSelectLabels(selectId)) return;

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
