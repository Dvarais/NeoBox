import { el, optionalEl, setText, setTitle, setPlaceholder, setAria, escapeHtml } from '../modules/dom';
import { translations, translate } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import { errorText } from '../modules/ui-utils';

export type LogLevel = 'INFO' | 'WARN' | 'ERROR' | 'DEBUG';
export type LogFilter = 'ALL' | LogLevel;

export interface LogEntry {
  t: number;             // Unix timestamp ms
  level: LogLevel;       // INFO, WARN, ERROR, DEBUG
  category: string;      // dns, router, inbound, outbound, system
  component?: string;    // inbound/tun[tun-in], dns, router
  message: string;       // Clean text
  raw: string;           // Original raw string
}

// Fixed line height for virtual scrolling
const ROW_HEIGHT = 24;
const MAX_LOG_ENTRIES = 5000;

const logsArray: LogEntry[] = [];
let filteredLogs: LogEntry[] = [];
let currentLogFilter: LogFilter = 'ALL';
let logSearchQuery = '';
let isScrolledToBottom = true;
let logsDomStale = true;
let virtualScrollRaf: number | null = null;

let getLang: () => Language = () => 'RU';
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => void | Promise<unknown> = () => {};
let showConfirmFn: (message: string) => Promise<boolean> = async () => true;
let onLogUnsub: (() => void) | null = null;

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

function parseLegacyLogLine(clean: string): {
  level: LogLevel;
  contextStr: string;
  messageStr: string;
} {
  let level: LogLevel = 'INFO';
  let contextStr = '';
  let messageStr = clean;

  const match = clean.match(/^([A-Z]+)\[([0-9]+)\]\s+(?:\[([0-9\s.a-zA-Z]+)\]\s+)?(.*)$/);
  if (match) {
    level = normalizeLevel(match[1]);
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

  return { level, contextStr, messageStr };
}

function formatLogLineFast(entry: LogEntry): string {
  const date = new Date(entry.t || Date.now());
  const timeStr = `${String(date.getHours()).padStart(2, '0')}:${String(date.getMinutes()).padStart(2, '0')}:${String(date.getSeconds()).padStart(2, '0')}`;
  const lvl = (entry.level || 'INFO').toLowerCase();

  let compHtml = '';
  if (entry.component) {
    compHtml = `<span class="log-context">[${escapeHtml(entry.component)}]</span> `;
  }

  const rawMsg = (entry.message || entry.raw || '').replace(/\x1b\[[0-9;]*[a-zA-Z]/g, '');
  return `<span class="log-time">[${timeStr}]</span> <span class="log-badge badge-${lvl}">${escapeHtml(entry.level || 'INFO')}</span> ${compHtml}<span class="log-msg">${escapeHtml(rawMsg)}</span>`;
}

function isLogsViewOpen(): boolean {
  return document.body.getAttribute('data-active-tab') === 'view-logs';
}

function logMatchesFilters(entry: LogEntry): boolean {
  if (currentLogFilter !== 'ALL' && entry.level !== currentLogFilter) return false;
  if (logSearchQuery && !entry.raw.toLowerCase().includes(logSearchQuery)) return false;
  return true;
}

function recomputeFilteredLogs(): void {
  filteredLogs = logsArray.filter(logMatchesFilters);
}

function updateVirtualViewport(): void {
  const fullLogOutput = optionalEl('fullLogOutput');
  if (!fullLogOutput) return;

  const total = filteredLogs.length;
  const clientH = fullLogOutput.clientHeight || 400;
  const scrollTop = fullLogOutput.scrollTop;

  const visibleCount = Math.ceil(clientH / ROW_HEIGHT) + 4;
  const startIndex = Math.max(0, Math.min(Math.floor(scrollTop / ROW_HEIGHT), Math.max(0, total - visibleCount)));
  const endIndex = Math.min(total, startIndex + visibleCount);

  const topOffset = startIndex * ROW_HEIGHT;
  const bottomOffset = Math.max(0, (total - endIndex) * ROW_HEIGHT);

  let html = `<div style="height:${topOffset}px;pointer-events:none;"></div>`;
  for (let i = startIndex; i < endIndex; i++) {
    const entry = filteredLogs[i];
    html += `<div class="log-line log-${(entry.level || 'info').toLowerCase()}">${formatLogLineFast(entry)}</div>`;
  }
  html += `<div style="height:${bottomOffset}px;pointer-events:none;"></div>`;

  fullLogOutput.innerHTML = html;
}

export function renderLogs(): void {
  const fullLogOutput = optionalEl('fullLogOutput');
  if (!fullLogOutput) return;

  recomputeFilteredLogs();
  updateLogsEmptyState(filteredLogs.length);
  updateVirtualViewport();

  if (isScrolledToBottom) {
    fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
  }
  logsDomStale = false;
}

function updateLogsEmptyState(shown: number): void {
  const empty = optionalEl('logsEmpty');
  if (!empty) return;
  const t = translations[getLang()];
  const filtering = logSearchQuery !== '' || currentLogFilter !== 'ALL';
  empty.style.display = shown === 0 && filtering && logsArray.length > 0 ? 'block' : 'none';
  empty.textContent = t.noSearchResults;
}

export function addLogEntries(batch: unknown[]): void {
  if (!Array.isArray(batch) || batch.length === 0) return;

  const viewOpen = isLogsViewOpen();

  for (const item of batch) {
    let entry: LogEntry;
    if (typeof item === 'string') {
      const parsed = parseLegacyLogLine(item);
      entry = {
        t: Date.now(),
        level: parsed.level,
        category: 'system',
        component: parsed.contextStr,
        message: parsed.messageStr,
        raw: item,
      };
    } else if (item && typeof item === 'object') {
      const obj = item as Record<string, unknown>;
      entry = {
        t: Number(obj.t || Date.now()),
        level: normalizeLevel(String(obj.level || 'INFO')),
        category: String(obj.category || 'system'),
        component: obj.component ? String(obj.component) : undefined,
        message: String(obj.message || obj.raw || ''),
        raw: String(obj.raw || obj.message || ''),
      };
    } else {
      continue;
    }
    logsArray.push(entry);
  }

  if (logsArray.length > MAX_LOG_ENTRIES) {
    logsArray.splice(0, logsArray.length - MAX_LOG_ENTRIES);
  }

  if (!viewOpen) {
    logsDomStale = true;
    return;
  }

  recomputeFilteredLogs();
  updateVirtualViewport();

  if (isScrolledToBottom) {
    const fullLogOutput = optionalEl('fullLogOutput');
    if (fullLogOutput) {
      fullLogOutput.scrollTop = fullLogOutput.scrollHeight;
    }
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
        Math.abs(fullLogOutput.scrollHeight - fullLogOutput.clientHeight - fullLogOutput.scrollTop) < 20;

      if (virtualScrollRaf === null) {
        virtualScrollRaf = requestAnimationFrame(() => {
          virtualScrollRaf = null;
          updateVirtualViewport();
        });
      }
    }, { passive: true });
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
      filteredLogs.length = 0;
      if (fullLogOutput) fullLogOutput.innerHTML = '';
      logsDomStale = false;
      updateLogsEmptyState(0);
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
      const rawLogs = logsArray.map((l) => l.raw).join('\n');
      const t = translations[getLang()];
      if (!rawLogs.trim()) {
        showAlertFn(t.alertDialogTitle, t.logsEmptyAlert, false, t);
        return;
      }
      try {
        // NBX-192-SEC: Автоматическое обезличивание логов перед сохранением
        let sanitized = rawLogs;
        if (window.api && (window.api as unknown as { sanitizeLogText?: (s: string) => Promise<string> }).sanitizeLogText) {
          sanitized = await (window.api as unknown as { sanitizeLogText: (s: string) => Promise<string> }).sanitizeLogText(rawLogs);
        } else {
          sanitized = rawLogs
            .replace(/\b[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}\b/g, '[REDACTED-UUID]')
            .replace(/(?:password|secret|key|uuid|token)=([^&\s]+)/gi, 'token=[REDACTED]')
            .replace(/\b([0-9]{1,3}\.[0-9]{1,3}\.)[0-9]{1,3}\.[0-9]{1,3}(:[0-9]+)?\b/g, (match) => {
              if (match.startsWith('127.0.0.1') || match.startsWith('0.0.0.0')) return match;
              const parts = match.split('.');
              const portIdx = parts[3].indexOf(':');
              const port = portIdx !== -1 ? parts[3].slice(portIdx) : '';
              return `${parts[0]}.${parts[1]}.*.*${port}`;
            });
        }
        const path = await window.api.saveLogs(sanitized);
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
  onLogUnsub = window.api.onLog(addLogEntries as (lines: string[]) => void);

  return () => {
    if (onLogUnsub) {
      onLogUnsub();
      onLogUnsub = null;
    }
  };
}
