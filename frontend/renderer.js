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
    });
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
  
  if (state === 'on') {
    powerBtn.classList.add('on', 'pulse-animation');
    statusDot.className = 'status-dot on';
    statusText.textContent = t.statusOn;
    statusText.style.color = 'var(--success)';
    restartBtn.style.display = 'flex';
    disconnectBtn.style.display = 'flex';
    isRestarting = false;
    setTimeout(() => fetchIP(currentIp, t), 2000);
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
    const cleanedContext = contextStr.trim().replace(/\s+/, ' • ');
    html += `<span class="log-context">[${cleanedContext}]</span> `;
  }

  // 4. Message formatting
  let formattedMessage = messageStr;
  
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
  if (!isRestarting) updateAppInterface('off');
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
});

window.api.onTrayStartReconnect((data) => {
  activeServerLink = data.link;
  const info = parseBasicInfo(data.link);
  activeServerName.textContent = info.name;
  updateCards();
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
  updateAppInterface('off');
  window.api.stopXray();
};

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
    processListWhitelist: processListWhitelistEl.value.split('\n').map(s => s.trim()).filter(s => s.length > 0)
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

    if (settings.rememberServer && settings.lastSelectedServer) {
       activeServerLink = settings.lastSelectedServer;
       const info = parseBasicInfo(activeServerLink);
       activeServerName.textContent = info.name;
       activeServerDetails.textContent = `${info.type.toUpperCase()} • ${info.address}`;
    }
    
    document.getElementById('bypassRuCheckbox').checked = !!settings.bypassRu;
    document.getElementById('tunModeCheckbox').checked = !!settings.tunMode;
    document.getElementById('autoConnectCheckbox').checked = !!settings.autoConnect;
    document.getElementById('autoUpdateSubsCheckbox').checked = !!settings.autoUpdateSubs;
    document.getElementById('rememberServerCheckbox').checked = !!settings.rememberServer;
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
  } else {
    applyLanguage();
  }
  await loadSubscriptions();

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
}

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
      trimmed.startsWith('hy2://')) {
    
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
