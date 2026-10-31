import { el, optionalEl, setText, setTitle, setPlaceholder, setAria, escapeHtml } from '../modules/dom';
import { translations, translate } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import { errorText } from '../modules/ui-utils';

export type LogLevel = 'INFO' | 'WARN' | 'ERROR' | 'DEBUG';
export type LogFilter = 'ALL' | LogLevel;

export interface LogEntry {
  level: LogLevel;
  text: string;
}

const MAX_LOG_ENTRIES = 500;
const logsArray: LogEntry[] = [];
let currentLogFilter: LogFilter = 'ALL';
let logSearchQuery = '';
let isScrolledToBottom = true;
let logsDomStale = true;

let getLang: () => Language = () => 'RU';
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => void | Promise<unknown> = () => {};
let showConfirmFn: (message: string) => Promise<boolean> = async () => true;
let onLogUnsub: (() => void) | null = null;

function stripAnsi(str: string): string {
  return str.replace(/[\u001b\u009b][[()#;?]*(?:[0-9]{1,4}(?:;[0-9]{0,4})*)?[0-9A-ORZcf-nqry=><]/g, '');
}

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
    const upper = clean.toUpperCase();
    if (upper.includes('ERROR') || upper.includes('FATAL') || upper.includes('FAILED')) {
      level = 'ERROR';
    } else if (upper.includes('WARN') || upper.includes('WARNING')) {
      level = 'WARN';
    } else if (upper.includes('DEBUG')) {
      level = 'DEBUG';
    }
  }

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

function renderLogHtml({ level, timeStr, contextStr, messageStr, isSuccess }: ReturnType<typeof parseLogLine>): string {
  let html = '';

  if (timeStr) {
    let formattedTime = timeStr;
    if (timeStr.length === 4) {
      formattedTime = timeStr.slice(0, 2) + ':' + timeStr.slice(2);
    }
    html += `<span class="log-time">[${formattedTime}]</span> `;
  }

  let badgeClass = `badge-${level.toLowerCase()}`;
  let displayLevel: string = level;
  if (isSuccess && level === 'INFO') {
    badgeClass = 'badge-success';
    displayLevel = 'SUCCESS';
  }
  html += `<span class="log-badge ${badgeClass}">${displayLevel}</span> `;

  if (contextStr) {
    const cleanedContext = escapeHtml(contextStr.trim().replace(/\s+/, ' • '));
    html += `<span class="log-context">[${cleanedContext}]</span> `;
  }

  let formattedMessage = escapeHtml(messageStr);
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

function isLogsViewOpen(): boolean {
  return document.body.getAttribute('data-active-tab') === 'view-logs';
}

function makeLogElement(entry: LogEntry): HTMLElement {
  const node = document.createElement('div');
  node.className = `log-line log-${entry.level.toLowerCase()}`;
  node.innerHTML = renderLogHtml(parseLogLine(entry.text));
  return node;
}

function logMatchesFilters(entry: LogEntry): boolean {
  if (currentLogFilter !== 'ALL' && entry.level !== currentLogFilter) return false;
  if (logSearchQuery && !entry.text.toLowerCase().includes(logSearchQuery)) return false;
  return true;
}

export function renderLogs(): void {
  const fullLogOutput = optionalEl('fullLogOutput');
  if (!fullLogOutput) return;

  fullLogOutput.innerHTML = '';
  const fragment = document.createDocumentFragment();
  let shown = 0;
  logsArray.forEach((entry) => {
    if (!logMatchesFilters(entry)) return;
    fragment.appendChild(makeLogElement(entry));
    shown++;
  });
  fullLogOutput.appendChild(fragment);
  fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
  logsDomStale = false;
  updateLogsEmptyState(shown);
}

function updateLogsEmptyState(shown: number): void {
  const empty = optionalEl('logsEmpty');
  if (!empty) return;
  const t = translations[getLang()];
  const filtering = logSearchQuery !== '' || currentLogFilter !== 'ALL';
  empty.style.display = shown === 0 && filtering && logsArray.length > 0 ? 'block' : 'none';
  empty.textContent = t.noSearchResults;
}

export function addLogEntries(lines: string[]): void {
  if (!Array.isArray(lines) || lines.length === 0) return;

  const fullLogOutput = optionalEl('fullLogOutput');
  const wasAtBottom = isScrolledToBottom;
  const viewOpen = isLogsViewOpen();
  const fragment = viewOpen && fullLogOutput ? document.createDocumentFragment() : null;
  let appended = 0;

  for (const raw of lines) {
    const text = stripAnsi(String(raw));

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

  if (!fragment || !fullLogOutput) {
    logsDomStale = true;
  } else if (appended > 0) {
    fullLogOutput.appendChild(fragment);
    const excess = fullLogOutput.childNodes.length - MAX_LOG_ENTRIES;
    for (let i = 0; i < excess && fullLogOutput.firstChild; i++) {
      fullLogOutput.removeChild(fullLogOutput.firstChild);
    }
    if (wasAtBottom) fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
    updateLogsEmptyState(fullLogOutput.childNodes.length);
  }
}

export function onLogsTabActivated(): void {
  if (logsDomStale) {
    renderLogs();
  }
}

export function applyLogsLanguage(t: Translations): void {
  setText('logsTitle', t.logsTitle);
  setText('clearLogsBtn', t.logsClearBtn);
  setText('saveLogsBtnText', t.saveLogsBtn);
  setTitle('saveLogsBtn', t.saveLogsBtnTitle);
  setPlaceholder('logSearchInput', t.logSearchPlaceholder);
  setAria('logSearchInput', t.logSearchPlaceholder);
  setText('logsEmpty', t.noSearchResults);
  setAria('fullLogOutput', t.logsTitle);

  const logTabs = optionalEl('logTabs') || document.querySelector('.log-tabs');
  if (logTabs) logTabs.setAttribute('aria-label', t.logFilterLabel);

  document.querySelectorAll<HTMLElement>('.log-tab').forEach((tab) => {
    const filter = tab.dataset.filter ?? '';
    tab.textContent = translate(t, `log${filter.charAt(0) + filter.slice(1).toLowerCase()}`);
  });
}

export function initLogsView(options: {
  getCurrentLanguage: () => Language;
  showAlert: (title: string, message: string, isError: boolean, t: Translations) => void | Promise<unknown>;
  showConfirm: (message: string) => Promise<boolean>;
}): () => void {
  getLang = options.getCurrentLanguage;
  showAlertFn = options.showAlert;
  showConfirmFn = options.showConfirm;

  const fullLogOutput = optionalEl('fullLogOutput');
  if (fullLogOutput) {
    fullLogOutput.addEventListener('scroll', () => {
      isScrolledToBottom =
        Math.abs(fullLogOutput.scrollHeight - fullLogOutput.clientHeight - fullLogOutput.scrollTop) < 5;
    });
  }

  document.querySelectorAll<HTMLElement>('.log-tab').forEach((tab) => {
    tab.addEventListener('click', (e) => {
      document.querySelectorAll<HTMLElement>('.log-tab').forEach((t) => {
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

  const clearLogsBtn = optionalEl('clearLogsBtn');
  if (clearLogsBtn) {
    clearLogsBtn.onclick = () => {
      logsArray.length = 0;
      if (fullLogOutput) fullLogOutput.innerHTML = '';
      logsDomStale = false;
    };
  }

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

  const saveLogsBtn = optionalEl('saveLogsBtn');
  if (saveLogsBtn) {
    saveLogsBtn.onclick = async () => {
      const rawLogs = logsArray.map((l) => l.text).join('\n');
      const t = translations[getLang()];
      if (!rawLogs.trim()) {
        showAlertFn(t.alertDialogTitle, t.logsEmptyAlert, false, t);
        return;
      }
      try {
        const path = await window.api.saveLogs(rawLogs);
        if (path) {
          const confirmed = await showConfirmFn(t.logsSavedConfirm.replace('{path}', path));
          if (confirmed) {
            window.api.openLogsFolder();
          }
        } else {
          showAlertFn(t.errorDialogTitle, t.logsSaveFailed, true, t);
        }
      } catch (e) {
        console.error('Failed to save logs:', e);
        showAlertFn(t.errorDialogTitle, errorText(e) || t.logsSaveFailed, true, t);
      }
    };
  }

  if (onLogUnsub) {
    onLogUnsub();
    onLogUnsub = null;
  }
  onLogUnsub = window.api.onLog(addLogEntries);

  return () => {
    if (onLogUnsub) {
      onLogUnsub();
      onLogUnsub = null;
    }
  };
}
