import { translations } from './modules/translations.js';
import { fetchIP, showPrompt, showConfirm, showAlert } from './modules/ui-utils.js';
import { 
    allSubscriptions, 
    currentActiveSubId, 
    loadSubscriptions as loadSubsBase, 
    renderSubTabs, 
    setActiveSubId,
    setSubscriptions
} from './modules/subscription-manager.js';
import { 
    renderCards, 
    parseBasicInfo, 
    pingData, 
    currentSortMode, 
    setSortMode, 
    setPingData 
} from './modules/server-manager.js';

// Элементы DOM
const powerBtn = document.getElementById('powerBtn');
const restartBtn = document.getElementById('restartBtn');
const disconnectBtn = document.getElementById('disconnectBtn');
const statusDot = document.getElementById('statusDot');
const statusText = document.getElementById('statusText');
const currentIp = document.getElementById('currentIp');
const fullLogOutput = document.getElementById('fullLogOutput');
const clearLogsBtn = document.getElementById('clearLogsBtn');
const activeServerName = document.getElementById('activeServerName');
const activeServerDetails = document.getElementById('activeServerDetails');
const serversGrid = document.getElementById('serversGrid');
const subTabsContainer = document.getElementById('subscription-tabs');
const processListBlacklistEl = document.getElementById('processListBlacklist');
const processListWhitelistEl = document.getElementById('processListWhitelist');

// Состояние
let activeServerLink = null;
let currentLanguage = 'RU';
let isRestarting = false;
let appState = 'off';

// Сессионный счётчик трафика (привязаны к window для совместимости с wails-bridge.js)
window.sessionBytesDown = 0;
window.sessionBytesUp = 0;
let sessionConnectedAt = null; // timestamp начала сессии
let sessionTimerInterval = null;

// Favorites
let favoriteLinks = new Set();
let customRules = [];

// Search query for server filter
let serverSearchQuery = '';

function onToggleFavorite(link) {
  if (favoriteLinks.has(link)) {
    favoriteLinks.delete(link);
  } else {
    favoriteLinks.add(link);
  }
  updateCards();
  collectAndSaveSettings();
}

// Навигация
const navItems = document.querySelectorAll('.nav-item');
const views = document.querySelectorAll('.view');

// Инициализируем активную вкладку для кастомных стилей
document.body.setAttribute('data-active-tab', 'view-home');

navItems.forEach(item => {
  item.addEventListener('click', () => {
    navItems.forEach(i => i.classList.remove('active'));
    views.forEach(v => v.classList.remove('active'));
    item.classList.add('active');
    const targetId = item.getAttribute('data-target');
    const targetView = document.getElementById(targetId);
    if (targetView) targetView.classList.add('active');
    
    // Устанавливаем атрибут активной вкладки для кастомных стилей и виджета скроллбара
    document.body.setAttribute('data-active-tab', targetId);
    if (targetId === 'view-servers') {
      setTimeout(updateCustomScroll, 50);
    }
  });
});

function updateCards() {
    let servers = [];
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
    }, serverSearchQuery, favoriteLinks, onToggleFavorite);
}

async function loadSubscriptions() {
    await loadSubsBase(() => {
        renderSubTabs(subTabsContainer, translations, currentLanguage, () => {
            updateCards();
        }, (title, def) => showPrompt('modalOverlay', 'modalTitle', 'modalInput', 'modalCancel', 'modalConfirm', title, def), (title) => showConfirm('modalOverlay', 'modalTitle', 'modalInput', 'modalCancel', 'modalConfirm', title), loadSubscriptions);
        updateCards();
    });
}

function applyLanguage() {
  const t = translations[currentLanguage];
  
  document.querySelectorAll('.nav-item').forEach(item => {
    const target = item.getAttribute('data-target');
    if (target === 'view-home') item.title = t.home;
    if (target === 'view-servers') item.title = t.servers;
    if (target === 'view-routes') item.title = t.routes;
    if (target === 'view-settings') item.title = t.settings;
    if (target === 'view-logs') item.title = t.logs;
  });
  document.getElementById('langToggle').textContent = currentLanguage;

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
  
  document.getElementById('restartBtnText').textContent = t.restartBtn;
  document.getElementById('disconnectBtnText').textContent = t.disconnectBtn;
  document.getElementById('speedDownloadLabel').textContent = t.downloadLabel;
  document.getElementById('speedUploadLabel').textContent = t.uploadLabel;
  const speedTotalLabel = document.getElementById('speedTotalLabel');
  if (speedTotalLabel) speedTotalLabel.textContent = t.totalTrafficLabel;

  // Переводы для игрового спидометра
  const trafficGameTitle = document.getElementById('trafficGameTitle');
  if (trafficGameTitle) trafficGameTitle.textContent = t.trafficGameTitle;
  const trafficGameDownLabel = document.getElementById('trafficGameDownLabel');
  if (trafficGameDownLabel) trafficGameDownLabel.textContent = t.trafficGameDownLabel;
  const trafficGameUpLabel = document.getElementById('trafficGameUpLabel');
  if (trafficGameUpLabel) trafficGameUpLabel.textContent = t.trafficGameUpLabel;
  const trafficGameLimitLabel = document.getElementById('trafficGameLimitLabel');
  if (trafficGameLimitLabel) trafficGameLimitLabel.textContent = t.trafficGameLimitLabel;

  document.getElementById('importQrBtn').textContent = t.importQrBtn;
  document.getElementById('qrModalTitle').textContent = t.qrModalTitle;
  document.getElementById('qrStartCameraBtn').textContent = t.qrStartCameraBtn;
  document.getElementById('qrUploadFileBtn').textContent = t.qrUploadFileBtn;
  document.getElementById('qrPlaceholderText').textContent = t.qrPlaceholderText;
  document.getElementById('qrModalClose').textContent = t.errorDialogClose;

  document.getElementById('subManagementTitle').textContent = t.subManagement;
  document.getElementById('subName').placeholder = t.subNamePlaceholder;
  document.getElementById('subUrl').placeholder = t.subUrlPlaceholder;
  document.getElementById('addSubBtn').textContent = t.addBtn;
  document.getElementById('updateSubBtn').textContent = t.updateCurrentBtn;
  document.getElementById('importClipboardBtn').textContent = t.importClipboardBtn;
  document.getElementById('myLocationsTitle').textContent = t.myLocations;
  document.getElementById('pingAllBtn').textContent = t.pingAllBtn;
  document.getElementById('sortBtnText').textContent = t.sortBtn;
  
  document.querySelectorAll('.sort-item').forEach(item => {
    const mode = item.dataset.sort;
    item.textContent = t[`sort${mode.charAt(0).toUpperCase() + mode.slice(1)}`];
  });

  document.getElementById('routeSettingsTitle').textContent = t.routeSettings;
  document.getElementById('directDomainsLabel').textContent = t.directDomainsLabel;
  document.getElementById('bypassRuLabel').textContent = t.bypassRuLabel;
  document.getElementById('splitTunnelingTitle').textContent = t.splitTunnelingTitle;
  document.getElementById('splitTunnelingDesc').innerHTML = t.splitTunnelingDesc.replace('chrome.exe', '<b>chrome.exe</b>');
  
  document.querySelectorAll('.process-tab').forEach(tab => {
    const mode = tab.dataset.mode;
    tab.textContent = mode === 'blacklist' ? t.blacklistTab : t.whitelistTab;
  });
  document.getElementById('processListBlacklist').placeholder = t.blacklistPlaceholder;
  document.getElementById('processListWhitelist').placeholder = t.whitelistPlaceholder;

  const customRoutesTitle = document.getElementById('customRoutesTitle');
  if (customRoutesTitle) customRoutesTitle.textContent = t.customRoutesTitle;
  const customRoutesDesc = document.getElementById('customRoutesDesc');
  if (customRoutesDesc) customRoutesDesc.textContent = t.customRoutesDesc;
  const addCustomRuleBtn = document.getElementById('addCustomRuleBtn');
  if (addCustomRuleBtn) addCustomRuleBtn.textContent = t.addCustomRuleBtn;
  const saveRoutesBtn2 = document.getElementById('saveRoutesBtn2');
  if (saveRoutesBtn2) saveRoutesBtn2.textContent = t.saveRoutesBtn2;
  const routesStatus2 = document.getElementById('routesStatus2');
  if (routesStatus2) routesStatus2.textContent = t.statusDone;
  
  const optActionDirect = document.getElementById('optActionDirect');
  if (optActionDirect) optActionDirect.textContent = t.actionDirectOption;
  const optActionProxy = document.getElementById('optActionProxy');
  if (optActionProxy) optActionProxy.textContent = t.actionProxyOption;
  const optActionBlock = document.getElementById('optActionBlock');
  if (optActionBlock) optActionBlock.textContent = t.actionBlockOption;
  
  const optTypeSuffix = document.getElementById('optTypeSuffix');
  if (optTypeSuffix) optTypeSuffix.textContent = t.typeSuffixOption;
  const optTypeDomain = document.getElementById('optTypeDomain');
  if (optTypeDomain) optTypeDomain.textContent = t.typeDomainOption;
  const optTypeKeyword = document.getElementById('optTypeKeyword');
  if (optTypeKeyword) optTypeKeyword.textContent = t.typeKeywordOption;
  const optTypeIp = document.getElementById('optTypeIp');
  if (optTypeIp) optTypeIp.textContent = t.typeIpOption;
  
  const newRuleValue = document.getElementById('newRuleValue');
  if (newRuleValue) newRuleValue.placeholder = t.ruleValuePlaceholder;
  
  renderCustomRules();

  document.getElementById('appSettingsTitle').textContent = t.appSettingsTitle;
  document.getElementById('dnsServerLabel').textContent = t.dnsServerLabel;
  document.querySelector('#dnsSelect option[value="custom"]').textContent = t.placeholderDns;
  document.getElementById('tunModeLabel').textContent = t.tunModeLabel;
  document.getElementById('tunModeDesc').textContent = t.tunModeDesc;
  document.getElementById('systemProxyLabel').textContent = t.systemProxyLabel;
  document.getElementById('autoConnectLabel').textContent = t.autoConnectLabel;
  document.getElementById('autoUpdateSubsLabel').textContent = t.autoUpdateSubsLabel;
  document.getElementById('rememberServerLabel').textContent = t.rememberServerLabel;
  document.getElementById('openAtLoginLabel').textContent = t.openAtLoginLabel;
  document.getElementById('startMinimizedLabel').textContent = t.startMinimizedLabel;
  document.getElementById('securityTitle').textContent = t.securityTitle;
  document.getElementById('killSwitchLabel').textContent = t.killSwitchLabel;
  document.getElementById('killSwitchDesc').textContent = t.killSwitchDesc;
  document.getElementById('dnsLeakLabel').textContent = t.dnsLeakLabel;
  document.getElementById('ipv6LeakLabel').textContent = t.ipv6LeakLabel;
  document.getElementById('fakeDnsLabel').textContent = t.fakeDnsLabel;
  document.getElementById('fakeDnsDesc').textContent = t.fakeDnsDesc;
  document.getElementById('saveRoutesBtn').textContent = t.saveRoutesBtn;
  document.getElementById('routesStatus').textContent = t.statusDone;
  document.getElementById('saveAppsBtn').textContent = t.saveAppsBtn;
  document.getElementById('appsStatus').textContent = t.statusDone;
  document.getElementById('saveSettingsBtn').textContent = t.saveAllBtn;
  document.getElementById('settingsStatus').textContent = t.statusDone;
  document.getElementById('logsTitle').textContent = t.logsTitle;
  document.getElementById('clearLogsBtn').textContent = t.logsClearBtn;
  document.querySelectorAll('.log-tab').forEach(tab => {
    const filter = tab.dataset.filter;
    tab.textContent = t[`log${filter.charAt(0) + filter.slice(1).toLowerCase()}`];
  });

  // Update Modal translations
  const updateModalTitleEl = document.getElementById('updateModalTitle');
  if (updateModalTitleEl) updateModalTitleEl.textContent = t.updateModalTitle;
  
  const updateModalChangelogTitleEl = document.querySelector('.update-changelog-title');
  if (updateModalChangelogTitleEl) updateModalChangelogTitleEl.textContent = t.updateModalChangelogTitle;
  
  const updateModalCancelEl = document.getElementById('updateModalCancel');
  if (updateModalCancelEl) updateModalCancelEl.textContent = t.updateModalCancel;
  
  const updateModalConfirmEl = document.getElementById('updateModalConfirm');
  if (updateModalConfirmEl) updateModalConfirmEl.textContent = t.updateModalConfirm;

  renderSubTabs(subTabsContainer, translations, currentLanguage, updateCards, (title, def) => showPrompt('modalOverlay', 'modalTitle', 'modalInput', 'modalCancel', 'modalConfirm', title, def), (title) => showConfirm('modalOverlay', 'modalTitle', 'modalInput', 'modalCancel', 'modalConfirm', title), loadSubscriptions);
  updateCards();
}

document.getElementById('langToggle').onclick = () => {
  currentLanguage = currentLanguage === 'RU' ? 'EN' : 'RU';
  applyLanguage();
  collectAndSaveSettings();
};

// Сортировка
const sortDropdown = document.querySelector('.sort-dropdown');
const sortMenu = document.querySelector('.sort-menu');
const sortItems = document.querySelectorAll('.sort-item');
let sortMenuTimeout;

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
    setSortMode(item.dataset.sort);
    updateCards();
    sortMenu.classList.remove('show');
  });
});

// Split Tunneling
const processTabs = document.querySelectorAll('.process-tab');
const processModeHidden = document.getElementById('processModeHidden');

processTabs.forEach(tab => {
  tab.addEventListener('click', (e) => {
    processTabs.forEach(t => {
      t.classList.remove('active');
      t.style.background = 'transparent';
      t.style.color = 'var(--text-main)';
      t.style.fontWeight = 'normal';
    });
    const target = e.currentTarget;
    target.classList.add('active');
    target.style.background = 'var(--accent-color)';
    target.style.color = '#000';
    target.style.fontWeight = 'bold';
    processModeHidden.value = target.getAttribute('data-mode');

    if (processModeHidden.value === 'blacklist') {
      processListBlacklistEl.style.display = 'block';
      processListWhitelistEl.style.display = 'none';
    } else {
      processListBlacklistEl.style.display = 'none';
      processListWhitelistEl.style.display = 'block';
    }
  });
});

function updateAppInterface(state) {
  appState = state;
  const t = translations[currentLanguage];
  const timerBadge = document.getElementById('sessionTimerBadge');
  const timerText = document.getElementById('sessionTimerText');

  if (state === 'on') {
    powerBtn.classList.add('on', 'pulse-animation');
    statusDot.className = 'status-dot on';
    statusText.textContent = t.statusOn;
    statusText.style.color = 'var(--success)';
    restartBtn.style.display = 'flex';
    disconnectBtn.style.display = 'flex';
    isRestarting = false;
    setTimeout(() => fetchIP(currentIp, t), 2000);
    startSessionTracking();
    // Start live connection timer
    if (timerBadge) timerBadge.style.display = 'flex';
    clearInterval(sessionTimerInterval);
    const timerStart = Date.now();
    sessionTimerInterval = setInterval(() => {
      const elapsed = Math.floor((Date.now() - timerStart) / 1000);
      const h = String(Math.floor(elapsed / 3600)).padStart(2, '0');
      const m = String(Math.floor((elapsed % 3600) / 60)).padStart(2, '0');
      const s = String(elapsed % 60).padStart(2, '0');
      if (timerText) timerText.textContent = `${h}:${m}:${s}`;
    }, 1000);
  } else if (state === 'connecting') {
    powerBtn.classList.add('on');
    powerBtn.classList.remove('pulse-animation');
    statusDot.className = 'status-dot connecting';
    statusText.textContent = t.statusConnecting;
    statusText.style.color = 'var(--accent-color)';
    currentIp.textContent = t.ipDetermining;
    restartBtn.style.display = 'none';
    disconnectBtn.style.display = 'flex';
  } else {
    powerBtn.classList.remove('on', 'pulse-animation');
    statusDot.className = 'status-dot';
    statusText.textContent = t.statusOff;
    statusText.style.color = 'var(--text-dim)';
    restartBtn.style.display = 'none';
    disconnectBtn.style.display = 'none';
    currentIp.textContent = '—';
    clearInterval(sessionTimerInterval);
    sessionTimerInterval = null;
    if (timerBadge) timerBadge.style.display = 'none';
    if (timerText) timerText.textContent = '00:00:00';
  }
}

// Логгер
const MAX_LOG_ENTRIES = 500;
const logsArray = [];
let currentLogFilter = 'ALL';
let isScrolledToBottom = true;

fullLogOutput.addEventListener('scroll', () => {
    isScrolledToBottom = Math.abs(fullLogOutput.scrollHeight - fullLogOutput.clientHeight - fullLogOutput.scrollTop) < 5;
});

document.querySelectorAll('.log-tab').forEach(tab => {
  tab.addEventListener('click', (e) => {
      document.querySelectorAll('.log-tab').forEach(t => t.classList.remove('active'));
      e.target.classList.add('active');
      currentLogFilter = e.target.dataset.filter;
      renderLogs();
  });
});

function stripAnsi(str) {
  return str.replace(/[\u001b\u009b][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]/g, '');
}

// Prevent XSS: escape HTML special chars before injecting into innerHTML.
// Log messages contain domain names and server addresses from the network.
function escapeHtml(str) {
  return str
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

function parseLogLine(rawString) {
  const clean = stripAnsi(rawString);
  let level = 'INFO';
  let timeStr = '';
  let contextStr = '';
  let messageStr = clean;

  const match = clean.match(/^([A-Z]+)\[([0-9]+)\]\s+(?:\[([0-9\s.a-zA-Z]+)\]\s+)?(.*)$/);
  if (match) {
    level = match[1];
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

  if (level === 'WARNING') level = 'WARN';

  // Fix the NOERROR -> ERROR bug
  let isSuccess = false;
  const upperMsg = messageStr.toUpperCase();
  if (upperMsg.includes('NOERROR') || upperMsg.includes('SUCCESS') || upperMsg.includes('OPENED') || upperMsg.includes('STARTED')) {
    isSuccess = true;
    if (level === 'ERROR') {
      level = 'INFO';
    }
  }

  // Format HTML
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
  let displayLevel = level;
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

  return {
    level: level,
    htmlText: html
  };
}

function renderLogs() {
  fullLogOutput.innerHTML = '';
  const filteredLogs = currentLogFilter === 'ALL' ? logsArray : logsArray.filter(log => log.level === currentLogFilter);
  const fragment = document.createDocumentFragment();
  filteredLogs.forEach(log => {
      const el = document.createElement('div');
      el.className = `log-line log-${log.level.toLowerCase()}`;
      el.innerHTML = log.htmlText || log.text;
      fragment.appendChild(el);
  });
  fullLogOutput.appendChild(fragment);
  fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
}

function addLogEntry(rawString) {
  const parsed = parseLogLine(rawString);

  const logEntry = { 
    level: parsed.level, 
    text: stripAnsi(rawString), 
    htmlText: parsed.htmlText 
  };
  logsArray.push(logEntry);
  if (logsArray.length > MAX_LOG_ENTRIES) logsArray.shift();

  if (currentLogFilter === 'ALL' || currentLogFilter === parsed.level) {
      const wasAtBottom = isScrolledToBottom;
      const el = document.createElement('div');
      el.className = `log-line log-${parsed.level.toLowerCase()}`;
      el.innerHTML = parsed.htmlText;
      fullLogOutput.appendChild(el);
      while (fullLogOutput.childNodes.length > MAX_LOG_ENTRIES) fullLogOutput.removeChild(fullLogOutput.firstChild);
      if (wasAtBottom) fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
  }
}

// События API
window.api.onLog((data) => {
  const cleanData = data.toString();
  // Фильтруем системный "шум" Windows, который не является ошибкой приложения
  if (cleanData.includes('wsasend: An established connection was aborted')) return;
  if (cleanData.includes('connection was aborted by the software in your host machine')) return;
  
  addLogEntry(cleanData);
  // Use a precise pattern so partial matches like 'DNS server started' or
  // 'restarted' don't falsely trigger the connected state.
  if (/\bsing-box\s+started\b/i.test(cleanData) && appState !== 'on') updateAppInterface('on');
});

clearLogsBtn.onclick = () => {
  logsArray.length = 0;
  fullLogOutput.innerHTML = '';
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

window.api.onTrayServerSelected((link) => {
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
      showAlert(translations[currentLanguage].errorDialogTitle, e.message, true, translations[currentLanguage]);
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
        const res = await window.api.startXray(activeServerLink, document.getElementById('systemProxyCheckbox').checked);
        if (res && !res.success) {
          showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
          updateAppInterface('off');
        }
      } catch (e) {
        showAlert(translations[currentLanguage].errorDialogTitle, e.message, true, translations[currentLanguage]);
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
  document.getElementById('sessionTotalDown').textContent = '0 B';
  document.getElementById('sessionTotalUp').textContent = '0 B';
  const speedTotal = document.getElementById('speedTotal');
  if (speedTotal) speedTotal.textContent = '0 B';
  const speedometerTotalContainer = document.getElementById('speedometerTotalContainer');
  if (speedometerTotalContainer) speedometerTotalContainer.style.display = 'none';
  document.getElementById('sessionTotalContainer').style.display = 'none'; // покажем после первого обновления

  // Сброс элементов игрового спидометра
  const ratioEl = document.getElementById('trafficGameRatio');
  const fillEl = document.getElementById('trafficGameProgressFill');
  const totalLimitEl = document.getElementById('trafficGameTotalAndLimit');
  if (ratioEl) ratioEl.textContent = '0%';
  if (fillEl) fillEl.style.width = '0%';
  if (totalLimitEl) totalLimitEl.textContent = '0 B / 100 MB';
}

// Форматирование байт в читаемый вид
function formatBytes(bytes) {
  if (bytes === 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return (bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 2) + ' ' + sizes[i];
}

// Формат длительности сессии в человекоческий вид
function formatDuration(seconds) {
  if (seconds < 60) return `${seconds}с`;
  const m = Math.floor(seconds / 60);
  const s = seconds % 60;
  if (m < 60) return `${m}м ${s}с`;
  const h = Math.floor(m / 60);
  const rm = m % 60;
  return `${h}ч ${rm}м`;
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

function loadHistory() {
  try { return JSON.parse(localStorage.getItem('neobox-connection-history') || '[]'); }
  catch { return []; }
}

function saveHistory(history) {
  localStorage.setItem('neobox-connection-history', JSON.stringify(history));
}

// Поиск ссылки сервера в подписках по метаданным (для обратной совместимости)
function findServerLink(name, address, protocol) {
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
async function selectAndConnectServer(link) {
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
      const res = await window.api.startXray(activeServerLink, document.getElementById('systemProxyCheckbox').checked);
      if (res && !res.success) {
        showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, e.message, true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  } else {
    restartBtn.click();
  }

  // Переключаем вкладку на Главную
  const homeTab = document.querySelector('.nav-item[data-target="view-home"]');
  if (homeTab) {
    homeTab.click();
  }
}

// Рендер вкладки История
function renderHistoryTab() {
  const history = loadHistory();
  const list = document.getElementById('historyList');
  const empty = document.getElementById('historyEmpty');
  const statsRow = document.getElementById('historyStatsRow');

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
  const totalDuration = history.reduce((a, e) => a + e.durationSec, 0);
  const totalDown = history.reduce((a, e) => a + (e.bytesDown || 0), 0);
  const totalUp = history.reduce((a, e) => a + (e.bytesUp || 0), 0);
  document.getElementById('historyStatSessions').textContent = history.length;
  document.getElementById('historyStatTime').textContent = formatDuration(totalDuration);
  document.getElementById('historyStatDown').textContent = formatBytes(totalDown);
  document.getElementById('historyStatUp').textContent = formatBytes(totalUp);

  // Карточки
  const protocolEmoji = { vless: '🟢', vmess: '🟡', trojan: '🔷', ss: '💜', tuic: '🟤', hysteria2: '🔵', hy2: '🔵' };
  list.innerHTML = history.map(entry => {
    const emoji = protocolEmoji[entry.protocol?.toLowerCase()] || '🌐';
    const date = new Date(entry.connectedAt);
    const dateStr = date.toLocaleDateString('ru-RU', { day: '2-digit', month: '2-digit' });
    const timeStr = date.toLocaleTimeString('ru-RU', { hour: '2-digit', minute: '2-digit' });
    const dur = formatDuration(entry.durationSec);
    const down = formatBytes(entry.bytesDown || 0);
    const up = formatBytes(entry.bytesUp || 0);
    const proto = (entry.protocol || '?').toUpperCase();
    
    // Пасхалка: YouTube иконка для серверов с youtube/yt/goog/dpi или случайно ~10% записей
    const isStableEgg = entry.server?.toLowerCase().includes('youtube') || 
                        entry.server?.toLowerCase().includes('yt') || 
                        entry.server?.toLowerCase().includes('goog') || 
                        entry.server?.toLowerCase().includes('dpi') ||
                        (entry.id && entry.id.charCodeAt(0) % 10 === 0);
    const eggClass = isStableEgg ? ' youtube-easter-egg' : '';

    return `
      <div class="history-card">
        <div class="history-card-icon${eggClass}" data-id="${entry.id}" title="Подключиться к этому серверу">
          <span class="history-icon-emoji">${emoji}</span>
          <span class="history-icon-play generic">
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
              <polygon points="6 3 20 12 6 21 6 3" fill="currentColor"/>
            </svg>
          </span>
          <span class="history-icon-play youtube">
            <svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
              <path d="M21.58 7.18a3.001 3.001 0 0 0-2.12-2.12C17.59 4.5 12 4.5 12 4.5s-5.59 0-7.46.56A3.001 3.001 0 0 0 2.42 7.18 31.248 31.248 0 0 0 1.8 12c0 1.6.2 3.22.62 4.82a3.001 3.001 0 0 0 2.12 2.12c1.87.56 7.46.56 7.46.56s5.59 0 7.46-.56a3.001 3.001 0 0 0 2.12-2.12c.42-1.6.62-3.22.62-4.82a31.248 31.248 0 0 0-.62-4.82z" fill="#FF0000"/>
              <polygon points="10 9 10 15 15 12" fill="#FFFFFF"/>
            </svg>
          </span>
        </div>
        <div class="history-card-body">
          <div class="history-card-server" title="${entry.server}">${entry.server}</div>
          <div class="history-card-meta">
            <span class="history-meta-item">📡 ${proto}</span>
            <span class="history-meta-item">⏱ ${dur}</span>
            <span class="history-meta-item">📅 ${dateStr} ${timeStr}</span>
          </div>
        </div>
        <div class="history-card-traffic">
          <span class="history-traffic-down">↓ ${down}</span>
          <span class="history-traffic-up">↑ ${up}</span>
        </div>
      </div>`;
  }).join('');

  // Обработчик клика по иконке запуска (через делегирование)
  list.onclick = (e) => {
    const iconBtn = e.target.closest('.history-card-icon');
    if (iconBtn) {
      const entryId = iconBtn.getAttribute('data-id');
      const hist = loadHistory();
      const entry = hist.find(item => item.id === entryId);
      if (entry) {
        const link = entry.link || findServerLink(entry.server, entry.address, entry.protocol);
        if (link) {
          selectAndConnectServer(link);
        } else {
          showAlert(translations[currentLanguage].alertDialogTitle, "Не удалось найти сервер в текущих подписках", false, translations[currentLanguage]);
        }
      }
    }
  };

  // Динамический Shift-пасхалка при движении мыши (для мгновенной реакции на зажатый Shift)
  list.onmousemove = (e) => {
    const iconBtn = e.target.closest('.history-card-icon');
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

document.getElementById('clearHistoryBtn').onclick = () => {
  saveHistory([]);
  renderHistoryTab();
};

// ── DNS Leak Test ────────────────────────────────────────────────────────────
document.getElementById('dnsLeakBtn').onclick = () => runDnsLeakTest();
document.getElementById('dnsLeakCloseBtn').onclick = () => {
  document.getElementById('dnsLeakModalOverlay').style.display = 'none';
};
document.getElementById('dnsLeakRetryBtn').onclick = () => runDnsLeakTest();

async function runDnsLeakTest() {
  const overlay = document.getElementById('dnsLeakModalOverlay');
  const loading = document.getElementById('dnsLeakLoading');
  const result  = document.getElementById('dnsLeakResult');
  const iconWrap = document.getElementById('dnsLeakIconWrap');
  const banner   = document.getElementById('dnsLeakStatusBanner');
  const statusTxt = document.getElementById('dnsLeakStatusText');
  const statusIcon = document.getElementById('dnsLeakStatusIcon');
  const ipEl     = document.getElementById('dnsLeakIp');
  const dnsList  = document.getElementById('dnsLeakDnsList');
  const retryBtn = document.getElementById('dnsLeakRetryBtn');

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

    const dnsServers = [];
    let remoteIp = '';
    let asn = '';
    let country = '';

    if (dohRes.Answer) {
      dohRes.Answer.forEach(ans => {
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
    const isCloudflareDns = (ip) =>
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
      div.textContent = 'Не удалось определить';
      dnsList.appendChild(div);
    }

    if (isLeaking) {
      iconWrap.className = 'dns-leak-icon-wrap leak';
      banner.className   = 'dns-leak-status-banner leak';
      statusIcon.textContent = '⚠️';
      statusTxt.textContent  = 'Обнаружена утечка DNS';
    } else {
      iconWrap.className = 'dns-leak-icon-wrap safe';
      banner.className   = 'dns-leak-status-banner';
      statusIcon.textContent = '✅';
      statusTxt.textContent  = 'Утечек не обнаружено';
    }
  } catch (err) {
    loading.style.display = 'none';
    result.style.display  = 'block';
    retryBtn.style.display = 'flex';
    banner.className = 'dns-leak-status-banner leak';
    statusIcon.textContent = '❌';
    statusTxt.textContent  = 'Ошибка проверки';
    ipEl.textContent = '—';
    dnsList.innerHTML = `<div class="dns-leak-dns-entry" style="color:var(--danger)">${err.message}</div>`;
  }
}

restartBtn.onclick = () => {
  if (!activeServerLink) return;
  isRestarting = true;
  updateAppInterface('connecting');
  (async () => {
    try {
      const res = await window.api.restartXray(activeServerLink, document.getElementById('systemProxyCheckbox').checked);
      if (res && !res.success) {
        showAlert(translations[currentLanguage].errorDialogTitle, res.error || 'Unknown error', true, translations[currentLanguage]);
        updateAppInterface('off');
      }
    } catch (e) {
      showAlert(translations[currentLanguage].errorDialogTitle, e.message, true, translations[currentLanguage]);
      updateAppInterface('off');
    }
  })();
};

// Настройки
function collectAndSaveSettings() {
  const settings = {
    language: currentLanguage,
    dns: document.getElementById('dnsSelect').value === 'custom' ? document.getElementById('customDnsInput').value : document.getElementById('dnsSelect').value,
    bypassRu: document.getElementById('bypassRuCheckbox').checked,
    tunMode: document.getElementById('tunModeCheckbox').checked,
    autoConnect: document.getElementById('autoConnectCheckbox').checked,
    autoUpdateSubs: document.getElementById('autoUpdateSubsCheckbox').checked,
    rememberServer: document.getElementById('rememberServerCheckbox').checked,
    openAtLogin: document.getElementById('openAtLoginCheckbox').checked,
    startMinimized: document.getElementById('startMinimizedCheckbox').checked,
    killSwitch: document.getElementById('killSwitchCheckbox').checked,
    dnsLeak: document.getElementById('dnsLeakCheckbox').checked,
    ipv6Leak: document.getElementById('ipv6LeakCheckbox').checked,
    fakeDns: document.getElementById('fakeDnsCheckbox').checked,
    lastSelectedServer: activeServerLink,
    customDirect: document.getElementById('customDirect').value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    processMode: processModeHidden.value,
    processListBlacklist: processListBlacklistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    processListWhitelist: processListWhitelistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0),
    favoriteLinks: Array.from(favoriteLinks),
    customRules: customRules
  };
  return window.api.saveSettings(settings);
}

document.getElementById('saveRoutesBtn').onclick = async () => {
  await collectAndSaveSettings();
  const status = document.getElementById('routesStatus');
  status.style.display = 'inline';
  setTimeout(() => status.style.display = 'none', 2000);
};

document.getElementById('saveAppsBtn').onclick = async () => {
  await collectAndSaveSettings();
  const status = document.getElementById('appsStatus');
  status.style.display = 'inline';
  setTimeout(() => status.style.display = 'none', 2000);
};

document.getElementById('saveSettingsBtn').onclick = () => {
  collectAndSaveSettings();
  const status = document.getElementById('settingsStatus');
  status.style.display = 'inline';
  setTimeout(() => status.style.display = 'none', 2000);
};

// Управление окном
async function animateAndAction(action) {
  document.body.classList.add('window-hidden');
  await new Promise(res => setTimeout(res, 250));
  action();
}

document.getElementById('minimizeBtn').onclick = () => animateAndAction(() => window.api.minimize());
document.getElementById('closeBtn').onclick = () => animateAndAction(() => window.api.close());

window.api.onWindowRestored(() => {
  document.body.classList.remove('window-hidden');
});

function showUpdateModal(update) {
  return new Promise((resolve) => {
    const overlay = document.getElementById('updateModalOverlay');
    const versionEl = document.getElementById('updateModalVersion');
    const changelogEl = document.getElementById('updateModalChangelog');
    const cancelBtn = document.getElementById('updateModalCancel');
    const confirmBtn = document.getElementById('updateModalConfirm');
    const progressSection = document.getElementById('updateProgressSection');
    const progressStatus = document.getElementById('updateProgressStatus');
    const progressPercent = document.getElementById('updateProgressPercent');
    const progressBar = document.getElementById('updateProgressBar');
    const actions = document.getElementById('updateModalActions');
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
        window.api.openUpdateLink(update.url);
        overlay.style.display = 'none';
        resolve(true);
        return;
      }

      // Transition UI to download state
      cancelBtn.style.display = 'none';
      confirmBtn.disabled = true;
      confirmBtn.textContent = currentLanguage === 'RU' ? 'Загрузка...' : 'Downloading...';
      progressSection.style.display = 'block';

      const cleanupEvents = [];

      const onProgress = (percent) => {
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

      const onError = (errMsg) => {
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
        await window.api.downloadAndInstallUpdate(update.downloadUrl);
      } catch (err) {
        onError(err.message || err);
      }
    };
  });
}

// Инициализация
async function init() {
  // Проверка обновлений
  try {
    const update = await window.api.checkUpdates();
    if (update && update.available) {
      await showUpdateModal(update);
    }
  } catch (e) { console.error('Update check failed:', e); }

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
    
    document.getElementById('bypassRuCheckbox').checked = !!settings.bypassRu;
    
    // Load and select the correct DNS server option on startup
    if (settings.dns) {
      const select = document.getElementById('dnsSelect');
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
          const customDnsInput = document.getElementById('customDnsInput');
          if (customDnsInput) {
            customDnsInput.value = settings.dns;
            customDnsInput.style.display = 'block';
          }
        }
      }
    }
    document.getElementById('tunModeCheckbox').checked = !!settings.tunMode;
    document.getElementById('autoConnectCheckbox').checked = !!settings.autoConnect;
    document.getElementById('autoUpdateSubsCheckbox').checked = !!settings.autoUpdateSubs;
    document.getElementById('rememberServerCheckbox').checked = rememberServer;
    document.getElementById('openAtLoginCheckbox').checked = !!settings.openAtLogin;
    document.getElementById('startMinimizedCheckbox').checked = !!settings.startMinimized;
    document.getElementById('killSwitchCheckbox').checked = !!settings.killSwitch;
    document.getElementById('dnsLeakCheckbox').checked = settings.dnsLeak !== undefined ? !!settings.dnsLeak : true;
    document.getElementById('ipv6LeakCheckbox').checked = settings.ipv6Leak !== undefined ? !!settings.ipv6Leak : true;
    document.getElementById('fakeDnsCheckbox').checked = settings.fakeDns !== undefined ? !!settings.fakeDns : true;
    if (settings.customDirect) document.getElementById('customDirect').value = settings.customDirect.join('\n');
    
    if (settings.processListBlacklist) processListBlacklistEl.value = settings.processListBlacklist.join('\n');
    if (settings.processListWhitelist) processListWhitelistEl.value = settings.processListWhitelist.join('\n');
    
    if (settings.processMode) {
      processModeHidden.value = settings.processMode;
      const targetTab = document.querySelector(`.process-tab[data-mode="${settings.processMode}"]`);
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
}

// ── CUSTOM ROUTING RULES UI ──────────────────────────────────────────────────
function renderCustomRules() {
  const container = document.getElementById('customRulesList');
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
    
    let typeName = rule.type;
    if (rule.type === 'domain_suffix') typeName = 'Suffix';
    else if (rule.type === 'domain') typeName = 'Domain';
    else if (rule.type === 'domain_keyword') typeName = 'Keyword';
    else if (rule.type === 'ip_cidr') typeName = 'IP/CIDR';
    
    infoSpan.innerHTML = `${actionBadge} <span style="color:var(--text-dim); font-size:11px;">[${typeName}]</span> <strong>${escapeHtml(rule.value)}</strong>`;
    
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

const addCustomRuleBtn = document.getElementById('addCustomRuleBtn');
if (addCustomRuleBtn) {
  addCustomRuleBtn.onclick = () => {
    const action = document.getElementById('newRuleAction').value;
    const type = document.getElementById('newRuleType').value;
    const valInput = document.getElementById('newRuleValue');
    const value = valInput.value.trim();
    
    if (!value) return;
    
    customRules.push({ action, type, value });
    valInput.value = '';
    renderCustomRules();
    collectAndSaveSettings();
  };
}

const saveRoutesBtn2 = document.getElementById('saveRoutesBtn2');
if (saveRoutesBtn2) {
  saveRoutesBtn2.onclick = async () => {
    await collectAndSaveSettings();
    const status = document.getElementById('routesStatus2');
    if (status) {
      status.style.display = 'inline';
      setTimeout(() => status.style.display = 'none', 2000);
    }
  };
}

// ── SEARCH LIVE FILTER ───────────────────────────────────────────────────────
const serverSearchInput = document.getElementById('serverSearchInput');
if (serverSearchInput) {
  serverSearchInput.addEventListener('input', (e) => {
    serverSearchQuery = e.target.value;
    updateCards();
  });
}

// ── AUTO-BEST SERVER ──────────────────────────────────────────────────────────
const bestServerBtn = document.getElementById('bestServerBtn');
if (bestServerBtn) {
  bestServerBtn.onclick = async () => {
    let links = [];
    allSubscriptions.forEach(s => links.push(...s.links));
    const uniqueLinks = Array.from(new Set(links));
    if (uniqueLinks.length === 0) return;
    
    // Show pinging status
    uniqueLinks.forEach(l => {
      setPingData(l, 'pinging');
      window.api.pingServer(l);
    });
    updateCards();
    
    bestServerBtn.disabled = true;
    const originalText = bestServerBtn.textContent;
    bestServerBtn.textContent = currentLanguage === 'RU' ? 'Поиск...' : 'Finding...';
    
    setTimeout(async () => {
      bestServerBtn.disabled = false;
      bestServerBtn.textContent = originalText;
      
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
            : document.getElementById('systemProxyCheckbox').checked;
          
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
          showAlert(translations[currentLanguage].errorDialogTitle, e.message, true, translations[currentLanguage]);
          updateAppInterface('off');
        }
      } else {
        showAlert(translations[currentLanguage].alertDialogTitle, currentLanguage === 'RU' ? 'Не удалось определить самый быстрый сервер!' : 'Could not determine the fastest server!', false, translations[currentLanguage]);
      }
    }, 2000);
  };
}

// ── SAVE LOGS ────────────────────────────────────────────────────────────────
const saveLogsBtn = document.getElementById('saveLogsBtn');
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
const importQrBtn = document.getElementById('importQrBtn');
const qrModalOverlay = document.getElementById('qrModalOverlay');
const qrModalClose = document.getElementById('qrModalClose');
const qrStartCameraBtn = document.getElementById('qrStartCameraBtn');
const qrUploadFileBtn = document.getElementById('qrUploadFileBtn');
const qrFileInput = document.getElementById('qrFileInput');
const qrVideo = document.getElementById('qrVideo');
const qrCanvas = document.getElementById('qrCanvas');
const qrPlaceholder = document.getElementById('qrScannerPlaceholder');
const qrPlaceholderText = document.getElementById('qrPlaceholderText');
const qrReticle = document.getElementById('qrScannerReticle');

let qrStream = null;
let qrAnimationId = null;

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
  const file = e.target.files[0];
  if (!file) return;

  const t = translations[currentLanguage];
  const reader = new FileReader();
  reader.onload = (event) => {
    const img = new Image();
    img.onload = async () => {
      const tempCanvas = document.createElement('canvas');
      const ctx = tempCanvas.getContext('2d');
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
    img.src = event.target.result;
  };
  reader.readAsDataURL(file);
  e.target.value = ''; // Reset file input
};

qrStartCameraBtn.onclick = async () => {
  stopQrCamera();
  const t = translations[currentLanguage];

  try {
    qrStream = await navigator.mediaDevices.getUserMedia({
      video: { facingMode: 'environment' }
    });
    qrVideo.srcObject = qrStream;
    qrVideo.setAttribute('playsinline', true);
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
    const canvasCtx = qrCanvas.getContext('2d');
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

async function handleQrImport(link) {
  const t = translations[currentLanguage];
  const trimmed = link.trim();
  
  if (trimmed.startsWith('vless://') || trimmed.startsWith('vmess://') ||
      trimmed.startsWith('ss://') || trimmed.startsWith('trojan://') ||
      trimmed.startsWith('tuic://') || trimmed.startsWith('hysteria2://') ||
      trimmed.startsWith('hy2://') || trimmed.startsWith('hysteria://')) {
    
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
  } else {
    showAlert(t.errorDialogTitle, t.qrNoCodeError, true, t);
  }
}

document.getElementById('pingAllBtn').onclick = () => {
  let links = [];
  allSubscriptions.forEach(s => links.push(...s.links));
  Array.from(new Set(links)).forEach(l => {
    setPingData(l, 'pinging');
    window.api.pingServer(l);
  });
  updateCards();
};

document.getElementById('dnsSelect').onchange = (e) => {
    document.getElementById('customDnsInput').style.display = e.target.value === 'custom' ? 'block' : 'none';
};

document.getElementById('tunModeCheckbox').onchange = async (e) => {
  if (e.target.checked) {
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

document.getElementById('addSubBtn').onclick = async () => {
  const nameInput = document.getElementById('subName');
  const urlInput = document.getElementById('subUrl');
  const name = nameInput.value.trim();
  const url = urlInput.value.trim();
  if (!name || !url) return;

  nameInput.value = '';
  urlInput.value = '';

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
    }
  })();
};

document.getElementById('importClipboardBtn').onclick = () => {
  window.api.importFromClipboard();
};

document.getElementById('updateSubBtn').onclick = async () => {
  const t = translations[currentLanguage];
  const originalText = document.getElementById('updateSubBtn').textContent;
  document.getElementById('updateSubBtn').textContent = currentLanguage === 'RU' ? 'Обновление...' : 'Updating...';
  
  if (currentActiveSubId === 'all') {
    // Update all subscriptions
    for (const sub of allSubscriptions) {
      try {
        const links = await window.api.fetchSubscription(sub.url);
        if (links && links.length > 0) {
          sub.links = links;
        }
      } catch (e) {
        console.error('Failed to update subscription:', sub.name, e);
      }
    }
    await window.api.saveSubscriptions(allSubscriptions);
    await loadSubscriptions();
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
      }
    }
  }
  document.getElementById('updateSubBtn').textContent = originalText;
};

// --- Вспомогательная функция дебаунса для автосохранения ---
function debounce(func, wait) {
  let timeout;
  return function executedFunction(...args) {
    const later = () => {
      clearTimeout(timeout);
      func(...args);
    };
    clearTimeout(timeout);
    timeout = setTimeout(later, wait);
  };
}

// --- Автосохранение при вводе в текстовые поля на лету ---
document.getElementById('customDirect').oninput = debounce(() => {
  collectAndSaveSettings();
}, 500);

document.getElementById('bypassRuCheckbox').onchange = () => {
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

const scrollWidget = document.getElementById('serversScrollWidget');
const scrollTrack = document.getElementById('scrollTrack');
const scrollThumb = document.getElementById('scrollThumb');
const scrollUpBtn = document.getElementById('scrollUpBtn');
const scrollDownBtn = document.getElementById('scrollDownBtn');
const mainContainer = document.querySelector('main');

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
function makeSelectCustom(selectId) {
  const select = document.getElementById(selectId);
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
  select.parentNode.insertBefore(wrapper, select.nextSibling);

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
