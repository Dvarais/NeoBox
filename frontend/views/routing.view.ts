import { el, optionalEl, escapeHtml, setAria, setText } from '../modules/dom';
import { iconSvg } from '../modules/icons';
import { trapFocus } from '../modules/ui-utils';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import type { AppSettings, CustomRule, ProcessInfo } from '../modules/api';

export interface RoutingViewOptions {
  getCurrentLanguage: () => Language;
  isCoreActive: () => boolean;
  restartCore: () => void;
  onSaveSettings: () => Promise<unknown> | unknown;
}

let getLang: () => Language = () => 'RU';
let isCoreActiveFn: () => boolean = () => false;
let restartCoreFn: () => void = () => {};
let onSaveSettingsFn: () => Promise<unknown> | unknown = () => {};

let customRules: CustomRule[] = [];
let rulesPending = false;
let isInitialized = false;

export interface DebouncedFunction<A extends unknown[]> {
  (...args: A): void;
  cancel: () => void;
}

function debounce<A extends unknown[]>(func: (...args: A) => void, wait: number): DebouncedFunction<A> {
  let timeout: ReturnType<typeof setTimeout> | null = null;
  const executedFunction = (...args: A) => {
    const later = () => {
      timeout = null;
      func(...args);
    };
    if (timeout !== null) clearTimeout(timeout);
    timeout = setTimeout(later, wait);
  };
  executedFunction.cancel = () => {
    if (timeout !== null) {
      clearTimeout(timeout);
      timeout = null;
    }
  };
  return executedFunction;
}

function safeSaveSettings(): void {
  try {
    const res = onSaveSettingsFn();
    if (res instanceof Promise) {
      res.catch((err) => console.error('Failed to save settings:', err));
    }
  } catch (err) {
    console.error('Failed to save settings:', err);
  }
}

export function areRulesPending(): boolean {
  return rulesPending;
}

export function markRulesPending(): void {
  rulesPending = true;
  renderRulesPending();
}

export function clearRulesPending(): void {
  rulesPending = false;
  renderRulesPending();
}

export function renderRulesPending(): void {
  const currentLang = getLang();
  const t = translations[currentLang];
  const connected = isCoreActiveFn();

  document.querySelectorAll<HTMLElement>('.rules-pending-bar').forEach((bar) => {
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

export function getCustomRules(): CustomRule[] {
  return customRules;
}

export function setCustomRules(rules: CustomRule[]): void {
  customRules = Array.isArray(rules) ? rules : [];
  renderCustomRules();
}

export function renderCustomRules(): void {
  const container = optionalEl('customRulesList');
  if (!container) return;
  container.innerHTML = '';

  const currentLang = getLang();
  const t = translations[currentLang];

  if (customRules.length === 0) {
    const emptyDiv = document.createElement('div');
    emptyDiv.className = 'custom-rules-empty';
    emptyDiv.textContent = t.customRulesEmpty;
    container.appendChild(emptyDiv);
    return;
  }

  customRules.forEach((rule, idx) => {
    const row = document.createElement('div');
    row.className = 'custom-rule-row';

    const infoSpan = document.createElement('span');
    infoSpan.className = 'custom-rule-info';

    let actionBadge = '';
    if (rule.action === 'direct') {
      actionBadge = `<span class="rule-action rule-action--direct">${iconSvg('arrowRight', 12)}<span>${escapeHtml(t.connActionDirect)}</span></span>`;
    } else if (rule.action === 'proxy') {
      actionBadge = `<span class="rule-action rule-action--proxy">${iconSvg('shield', 12)}<span>${escapeHtml(t.connActionProxy)}</span></span>`;
    } else if (rule.action === 'block') {
      actionBadge = `<span class="rule-action rule-action--block">${iconSvg('slash', 12)}<span>${escapeHtml(t.connActionBlock)}</span></span>`;
    }

    let typeName: string = rule.type;
    if (rule.type === 'domain_suffix') typeName = t.ruleBadgeSuffix;
    else if (rule.type === 'domain') typeName = t.ruleBadgeDomain;
    else if (rule.type === 'domain_keyword') typeName = t.ruleBadgeKeyword;
    else if (rule.type === 'ip_cidr') typeName = t.ruleBadgeIp;
    else if (rule.type === 'process') typeName = t.ruleBadgeProcess;

    infoSpan.innerHTML = `${actionBadge} <span class="rule-type-badge">${escapeHtml(typeName)}</span> <code class="rule-value-mono">${escapeHtml(rule.value)}</code>`;

    const delBtn = document.createElement('button');
    delBtn.type = 'button';
    delBtn.className = 'btn-glass btn-icon-only rule-delete-btn';
    delBtn.title = t.deleteBtn;
    delBtn.setAttribute('aria-label', t.deleteBtn);
    delBtn.innerHTML = iconSvg('trash', 14);

    delBtn.onclick = async () => {
      try {
        customRules.splice(idx, 1);
        renderCustomRules();
        await onSaveSettingsFn();
        markRulesPending();
      } catch (err) {
        console.error('Failed to save settings on custom rule delete:', err);
      }
    };

    row.appendChild(infoSpan);
    row.appendChild(delBtn);
    container.appendChild(row);
  });
}

// upsertCustomRule добавляет правило или переписывает действие у уже
// существующего с тем же типом и значением.
export function upsertCustomRule(rule: CustomRule): 'added' | 'replaced' | 'unchanged' {
  const idx = customRules.findIndex((r) => r.type === rule.type && r.value === rule.value);
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
    safeSaveSettings();
    markRulesPending();
  }
  return outcome;
}

export function getProcessMode(): AppSettings['processMode'] {
  const hidden = optionalEl<HTMLInputElement>('processModeHidden');
  return hidden?.value === 'whitelist' ? 'whitelist' : 'blacklist';
}

export function setProcessMode(mode: AppSettings['processMode']): void {
  const safeMode: AppSettings['processMode'] = mode === 'whitelist' ? 'whitelist' : 'blacklist';
  const hidden = optionalEl<HTMLInputElement>('processModeHidden');
  if (hidden) hidden.value = safeMode;

  const blacklistEl = optionalEl<HTMLTextAreaElement>('processListBlacklist');
  const whitelistEl = optionalEl<HTMLTextAreaElement>('processListWhitelist');

  if (safeMode === 'blacklist') {
    if (blacklistEl) blacklistEl.style.display = 'block';
    if (whitelistEl) whitelistEl.style.display = 'none';
  } else {
    if (blacklistEl) blacklistEl.style.display = 'none';
    if (whitelistEl) whitelistEl.style.display = 'block';
  }

  document.querySelectorAll<HTMLElement>('.process-tab').forEach((tab) => {
    const isTarget = tab.getAttribute('data-mode') === safeMode;
    tab.classList.toggle('active', isTarget);
    tab.setAttribute('aria-pressed', isTarget ? 'true' : 'false');
    tab.style.background = isTarget ? 'var(--accent-color)' : 'transparent';
    tab.style.color = isTarget ? 'var(--on-accent)' : 'var(--text-main)';
    tab.style.fontWeight = isTarget ? 'bold' : 'normal';
  });
}

export function getProcessListBlacklist(): string[] {
  const area = optionalEl<HTMLTextAreaElement>('processListBlacklist');
  if (!area) return [];
  return area.value
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

export function setProcessListBlacklist(lines?: string[] | null): void {
  const area = optionalEl<HTMLTextAreaElement>('processListBlacklist');
  if (area) area.value = Array.isArray(lines) ? lines.join('\n') : '';
}

export function getProcessListWhitelist(): string[] {
  const area = optionalEl<HTMLTextAreaElement>('processListWhitelist');
  if (!area) return [];
  return area.value
    .split('\n')
    .map((s) => s.trim())
    .filter((s) => s.length > 0);
}

export function setProcessListWhitelist(lines?: string[] | null): void {
  const area = optionalEl<HTMLTextAreaElement>('processListWhitelist');
  if (area) area.value = Array.isArray(lines) ? lines.join('\n') : '';
}

export function applyRoutingLanguage(t: Translations): void {
  const routeSettingsTitle = optionalEl('routeSettingsTitle');
  if (routeSettingsTitle) routeSettingsTitle.textContent = t.routeSettings;

  const bypassRuLabel = optionalEl('bypassRuLabel');
  if (bypassRuLabel) bypassRuLabel.textContent = t.bypassRuLabel;

  const blockTorrentLabel = optionalEl('blockTorrentLabel');
  if (blockTorrentLabel) blockTorrentLabel.textContent = t.blockTorrentLabel;

  const blockTorrentDesc = optionalEl('blockTorrentDesc');
  if (blockTorrentDesc) blockTorrentDesc.textContent = t.blockTorrentDesc;

  const blockQuicLabel = optionalEl('blockQuicLabel');
  if (blockQuicLabel) blockQuicLabel.textContent = t.blockQuicLabel;

  const blockQuicDesc = optionalEl('blockQuicDesc');
  if (blockQuicDesc) blockQuicDesc.textContent = t.blockQuicDesc;

  const splitTunnelingTitle = optionalEl('splitTunnelingTitle');
  if (splitTunnelingTitle) splitTunnelingTitle.textContent = t.splitTunnelingTitle;

  const splitTunnelingDesc = optionalEl('splitTunnelingDesc');
  if (splitTunnelingDesc) splitTunnelingDesc.innerHTML = t.splitTunnelingDesc.replace('chrome.exe', '<b>chrome.exe</b>');

  document.querySelectorAll<HTMLElement>('.process-tab').forEach((tab) => {
    const mode = tab.dataset.mode;
    tab.textContent = mode === 'blacklist' ? t.blacklistTab : t.whitelistTab;
  });

  const black = optionalEl<HTMLTextAreaElement>('processListBlacklist');
  if (black) black.placeholder = t.blacklistPlaceholder;

  const white = optionalEl<HTMLTextAreaElement>('processListWhitelist');
  if (white) white.placeholder = t.whitelistPlaceholder;

  setText('saveAppsBtn', t.saveAppsBtn);
  setText('appsStatus', t.statusDone);

  const processTabsGroup = optionalEl('processTabsGroup');
  if (processTabsGroup) processTabsGroup.setAttribute('aria-label', t.processModeLabel);

  setAria('processListBlacklist', t.blacklistAreaLabel);
  setAria('processListWhitelist', t.whitelistAreaLabel);
  setAria('newRuleAction', t.ruleActionLabel);
  setAria('newRuleType', t.ruleTypeLabel);

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

  const ruleVal = optionalEl<HTMLInputElement>('newRuleValue');
  if (ruleVal) {
    ruleVal.placeholder = t.ruleValuePlaceholder;
    ruleVal.setAttribute('aria-label', t.ruleValuePlaceholder);
  }

  setText('openProcessPickerBtnText', t.openProcessPickerBtn);
  setText('processPickerTitle', t.processPickerTitle);
  setText('processPickerSubtitle', t.processPickerSubtitle);
  const procSearch = optionalEl<HTMLInputElement>('processPickerSearch');
  if (procSearch) {
    procSearch.placeholder = t.processPickerSearch;
    procSearch.setAttribute('aria-label', t.processPickerSearch);
  }
  setText('processPickerLoadingText', t.processPickerLoading);
  setText('processPickerEmptyText', t.processPickerEmpty);
  setText('processPickerCancelBtn', t.modalCancel);
  setText('processPickerApplyBtn', t.processPickerApply);
  updateSelectedCount();

  renderCustomRules();
  renderRulesPending();
}

let appsStatusTimer: ReturnType<typeof setTimeout> | null = null;
let processPickerRelease: (() => void) | null = null;
let allProcesses: ProcessInfo[] = [];
const selectedProcessMap = new Map<string, string>(); // lowerName -> originalName

function updateSelectedCount(): void {
  const countEl = optionalEl('processPickerSelectedCount');
  if (!countEl) return;
  const currentLang = getLang();
  const t = translations[currentLang];
  countEl.textContent = t.processPickerSelected.replace('{n}', String(selectedProcessMap.size));
}

export function closeProcessPicker(): void {
  const overlay = optionalEl('processPickerModalOverlay');
  if (overlay) overlay.style.display = 'none';
  processPickerRelease?.();
  processPickerRelease = null;
}

function renderProcessList(): void {
  const listEl = optionalEl('processPickerList');
  const emptyEl = optionalEl('processPickerEmpty');
  const searchInput = optionalEl<HTMLInputElement>('processPickerSearch');
  if (!listEl) return;

  const currentLang = getLang();
  const t = translations[currentLang];
  const query = (searchInput?.value ?? '').trim().toLowerCase();

  const filtered = allProcesses.filter((proc) => {
    if (!query) return true;
    return (
      proc.name.toLowerCase().includes(query) ||
      (proc.path && proc.path.toLowerCase().includes(query))
    );
  });

  listEl.innerHTML = '';

  if (filtered.length === 0) {
    listEl.style.display = 'none';
    if (emptyEl) emptyEl.style.display = 'block';
    return;
  }

  if (emptyEl) emptyEl.style.display = 'none';
  listEl.style.display = 'flex';

  for (const proc of filtered) {
    const row = document.createElement('div');
    row.className = 'process-item';
    const lower = proc.name.toLowerCase();
    const isSelected = selectedProcessMap.has(lower);
    if (isSelected) {
      row.classList.add('selected');
    }

    const checkbox = document.createElement('input');
    checkbox.type = 'checkbox';
    checkbox.className = 'process-item-checkbox';
    checkbox.checked = isSelected;
    checkbox.setAttribute('aria-label', proc.name);

    const content = document.createElement('div');
    content.className = 'process-item-content';

    const topRow = document.createElement('div');
    topRow.className = 'process-item-top';

    const nameEl = document.createElement('span');
    nameEl.className = 'process-name';
    nameEl.textContent = proc.name;
    topRow.appendChild(nameEl);

    if (proc.count > 1) {
      const badge = document.createElement('span');
      badge.className = 'process-badge';
      badge.textContent = `${proc.count}`;
      badge.title = t.processPickerInstances.replace('{n}', String(proc.count));
      topRow.appendChild(badge);
    }
    content.appendChild(topRow);

    if (proc.path) {
      const pathEl = document.createElement('span');
      pathEl.className = 'process-path';
      pathEl.textContent = proc.path;
      pathEl.title = proc.path;
      content.appendChild(pathEl);
    }

    row.appendChild(checkbox);
    row.appendChild(content);

    const toggle = (nextState?: boolean) => {
      const checked = typeof nextState === 'boolean' ? nextState : !selectedProcessMap.has(lower);
      checkbox.checked = checked;
      if (checked) {
        selectedProcessMap.set(lower, proc.name);
        row.classList.add('selected');
      } else {
        selectedProcessMap.delete(lower);
        row.classList.remove('selected');
      }
      updateSelectedCount();
    };

    checkbox.addEventListener('change', (e) => {
      e.stopPropagation();
      toggle(checkbox.checked);
    });

    row.addEventListener('click', (e) => {
      if (e.target === checkbox) return;
      toggle();
    });

    listEl.appendChild(row);
  }
}

export async function openProcessPicker(): Promise<void> {
  const overlay = optionalEl('processPickerModalOverlay');
  if (!overlay) return;

  const currentLang = getLang();
  const t = translations[currentLang];

  const hiddenMode = optionalEl<HTMLInputElement>('processModeHidden');
  const mode = (hiddenMode?.value ?? 'blacklist') as 'blacklist' | 'whitelist';
  const targetTextarea = optionalEl<HTMLTextAreaElement>(
    mode === 'whitelist' ? 'processListWhitelist' : 'processListBlacklist'
  );

  selectedProcessMap.clear();
  if (targetTextarea) {
    const lines = targetTextarea.value.split('\n');
    for (const raw of lines) {
      const trimmed = raw.trim();
      if (trimmed) {
        selectedProcessMap.set(trimmed.toLowerCase(), trimmed);
      }
    }
  }

  const searchInput = optionalEl<HTMLInputElement>('processPickerSearch');
  if (searchInput) searchInput.value = '';

  overlay.style.display = 'flex';
  const loadingEl = optionalEl('processPickerLoading');
  const listEl = optionalEl('processPickerList');
  const emptyEl = optionalEl('processPickerEmpty');
  if (loadingEl) loadingEl.style.display = 'flex';
  if (listEl) {
    listEl.style.display = 'none';
    listEl.innerHTML = '';
  }
  if (emptyEl) emptyEl.style.display = 'none';

  updateSelectedCount();

  processPickerRelease = trapFocus(overlay, searchInput, closeProcessPicker);

  try {
    const list = await window.api.getRunningProcesses();
    allProcesses = Array.isArray(list) ? list : [];
  } catch (err) {
    console.error('Failed to get running processes:', err);
    allProcesses = [];
  }

  if (loadingEl) loadingEl.style.display = 'none';
  renderProcessList();
}

function applyProcessPicker(): void {
  const hiddenMode = optionalEl<HTMLInputElement>('processModeHidden');
  const mode = (hiddenMode?.value ?? 'blacklist') as 'blacklist' | 'whitelist';
  const targetTextarea = optionalEl<HTMLTextAreaElement>(
    mode === 'whitelist' ? 'processListWhitelist' : 'processListBlacklist'
  );

  if (targetTextarea) {
    const existingLines = targetTextarea.value
      .split('\n')
      .map((l) => l.trim())
      .filter((l) => l.length > 0);

    const existingLower = new Set(existingLines.map((l) => l.toLowerCase()));
    const finalLines = [...existingLines];

    for (const [lower, name] of selectedProcessMap.entries()) {
      if (!existingLower.has(lower)) {
        finalLines.push(name);
        existingLower.add(lower);
      }
    }

    targetTextarea.value = finalLines.join('\n');
    safeSaveSettings();

    const status = optionalEl('appsStatus');
    if (status) {
      status.style.display = 'inline';
      if (appsStatusTimer !== null) clearTimeout(appsStatusTimer);
      appsStatusTimer = setTimeout(() => {
        status.style.display = 'none';
        appsStatusTimer = null;
      }, 2000);
    }
  }

  closeProcessPicker();
}

export function initRoutingView(options: RoutingViewOptions): void {
  getLang = options.getCurrentLanguage;
  isCoreActiveFn = options.isCoreActive;
  restartCoreFn = options.restartCore;
  onSaveSettingsFn = options.onSaveSettings;

  if (isInitialized) return;
  isInitialized = true;

  // Block BitTorrent (P2P) checkbox
  const blockTorrentCheckbox = optionalEl<HTMLInputElement>('blockTorrentCheckbox');
  if (blockTorrentCheckbox) {
    blockTorrentCheckbox.onchange = () => {
      markRulesPending();
      safeSaveSettings();
    };
  }

  // Block QUIC / HTTP-3 checkbox
  const blockQuicCheckbox = optionalEl<HTMLInputElement>('blockQuicCheckbox');
  if (blockQuicCheckbox) {
    blockQuicCheckbox.onchange = () => {
      markRulesPending();
      safeSaveSettings();
    };
  }

  // Process Mode tabs
  const processTabs = document.querySelectorAll<HTMLElement>('.process-tab');

  processTabs.forEach((tab) => {
    tab.addEventListener('click', (e) => {
      const target = e.currentTarget as HTMLElement;
      const mode = (target.getAttribute('data-mode') ?? 'blacklist') as AppSettings['processMode'];
      setProcessMode(mode);
      safeSaveSettings();
    });
  });

  // Debounced textarea inputs
  const blacklistEl = optionalEl<HTMLTextAreaElement>('processListBlacklist');
  if (blacklistEl) {
    blacklistEl.oninput = debounce(() => {
      safeSaveSettings();
    }, 500);
  }

  const whitelistEl = optionalEl<HTMLTextAreaElement>('processListWhitelist');
  if (whitelistEl) {
    whitelistEl.oninput = debounce(() => {
      safeSaveSettings();
    }, 500);
  }

  // Save Apps button (explicit save for split tunneling)
  const saveAppsBtn = optionalEl('saveAppsBtn');
  if (saveAppsBtn) {
    saveAppsBtn.onclick = async () => {
      try {
        await onSaveSettingsFn();
        const status = optionalEl('appsStatus');
        if (status) {
          status.style.display = 'inline';
          if (appsStatusTimer !== null) clearTimeout(appsStatusTimer);
          appsStatusTimer = setTimeout(() => {
            status.style.display = 'none';
            appsStatusTimer = null;
          }, 2000);
        }
      } catch (err) {
        console.error('Failed to save app lists:', err);
      }
    };
  }

  // Add rule button
  const addCustomRuleBtn = optionalEl('addCustomRuleBtn');
  const valInput = optionalEl<HTMLInputElement>('newRuleValue');

  const handleAddRule = () => {
    const actionSelect = optionalEl<HTMLSelectElement>('newRuleAction');
    const typeSelect = optionalEl<HTMLSelectElement>('newRuleType');
    if (!actionSelect || !typeSelect || !valInput) return;

    const action = actionSelect.value as CustomRule['action'];
    const type = typeSelect.value as CustomRule['type'];
    const value = valInput.value.trim();

    if (!value) return;

    upsertCustomRule({ action, type, value });
    valInput.value = '';
  };

  if (addCustomRuleBtn) {
    addCustomRuleBtn.onclick = handleAddRule;
  }

  if (valInput) {
    valInput.addEventListener('keydown', (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        handleAddRule();
      }
    });
  }

  // Pending rules apply click
  document.querySelectorAll<HTMLButtonElement>('.rules-pending-apply').forEach((btn) => {
    btn.onclick = () => {
      try {
        restartCoreFn();
      } catch (err) {
        console.error('Failed to restart core from pending rules:', err);
      }
    };
  });

  // Process Picker modal buttons & events
  const openProcessPickerBtn = optionalEl('openProcessPickerBtn');
  if (openProcessPickerBtn) {
    openProcessPickerBtn.onclick = () => {
      void openProcessPicker();
    };
  }

  const processPickerCancelBtn = optionalEl('processPickerCancelBtn');
  if (processPickerCancelBtn) {
    processPickerCancelBtn.onclick = () => {
      closeProcessPicker();
    };
  }

  const processPickerApplyBtn = optionalEl('processPickerApplyBtn');
  if (processPickerApplyBtn) {
    processPickerApplyBtn.onclick = () => {
      applyProcessPicker();
    };
  }

  const procSearch = optionalEl<HTMLInputElement>('processPickerSearch');
  if (procSearch) {
    procSearch.oninput = () => {
      renderProcessList();
    };
    procSearch.onkeydown = (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        applyProcessPicker();
      }
    };
  }

  const procOverlay = optionalEl('processPickerModalOverlay');
  if (procOverlay) {
    procOverlay.addEventListener('click', (e) => {
      if (e.target === procOverlay) {
        closeProcessPicker();
      }
    });
  }
}
