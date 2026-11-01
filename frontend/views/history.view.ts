import { optionalEl, setText } from '../modules/dom';
import { iconSvg } from '../modules/icons';
import { formatBytes, formatDuration, errorText } from '../modules/ui-utils';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import type { HistoryEntry } from '../modules/api';

export const LEGACY_HISTORY_KEY = 'neobox-connection-history';

export interface HistoryViewOptions {
  getCurrentLanguage: () => Language;
  onSelectServer?: (link: string) => void;
  findServerLink?: (name: string, address: string, protocol: string) => string | null;
  showAlert: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void;
}

let getLang: () => Language = () => 'RU';
let onSelectServerFn: ((link: string) => void) | null = null;
let findServerLinkFn: ((name: string, address: string, protocol: string) => string | null) | null = null;
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void = () => {};

let historyCache: HistoryEntry[] = [];
let isInitialized = false;

const PLAY_ICON_SVG = `
  <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
    <polygon points="6 3 20 12 6 21 6 3" fill="currentColor"/>
  </svg>`;

const YOUTUBE_ICON_SVG = `
  <svg width="24" height="24" viewBox="0 0 24 24" fill="none" xmlns="http://www.w3.org/2000/svg">
    <path d="M21.58 7.18a3.001 3.001 0 0 0-2.12-2.12C17.59 4.5 12 4.5 12 4.5s-5.59 0-7.46.56A3.001 3.001 0 0 0 2.42 7.18 31.248 31.248 0 0 0 1.8 12c0 1.6.2 3.22.62 4.82a3.001 3.001 0 0 0 2.12 2.12c1.87.56 7.46.56 7.46.56s5.59 0 7.46-.56a3.001 3.001 0 0 0 2.12-2.12c.42-1.6.62-3.22.62-4.82a31.248 31.248 0 0 0-.62-4.82z" fill="#FF0000"/>
    <polygon points="10 9 10 15 15 12" fill="#FFFFFF"/>
  </svg>`;

export function loadHistory(): HistoryEntry[] {
  return historyCache;
}

export async function saveHistory(history: HistoryEntry[]): Promise<boolean> {
  historyCache = history;
  try {
    const success = await window.api.saveHistory(history);
    if (!success) {
      console.error('saveHistory: backend returned false');
    }
    return success;
  } catch (e) {
    console.error('saveHistory failed:', e);
    return false;
  }
}

export function addSessionHistory(entry: HistoryEntry): void {
  const history = loadHistory();
  history.unshift(entry);
  if (history.length > 100) history.pop();
  void saveHistory(history);
  renderHistoryTab();
}

export async function initHistory(): Promise<void> {
  try {
    historyCache = await window.api.getHistory();
  } catch (e) {
    console.error('getHistory failed:', e);
    historyCache = [];
  }

  const legacyRaw = localStorage.getItem(LEGACY_HISTORY_KEY);
  if (legacyRaw !== null) {
    let legacy: unknown = null;
    try {
      legacy = JSON.parse(legacyRaw);
    } catch {
      legacy = null;
    }

    if (historyCache.length === 0 && Array.isArray(legacy) && legacy.length > 0) {
      try {
        if (await window.api.saveHistory(legacy as HistoryEntry[])) {
          historyCache = legacy as HistoryEntry[];
          localStorage.removeItem(LEGACY_HISTORY_KEY);
        }
      } catch (e) {
        console.error('Migrating legacy history failed:', e);
      }
    } else {
      localStorage.removeItem(LEGACY_HISTORY_KEY);
    }
  }

  renderHistoryTab();
}

function onHistoryListClick(e: MouseEvent): void {
  const target = e.target as HTMLElement | null;
  if (!target) return;

  // Удаление отдельной записи
  const delBtn = target.closest<HTMLElement>('.history-delete-btn');
  if (delBtn) {
    e.stopPropagation();
    const deleteId = delBtn.dataset.deleteId;
    if (deleteId) {
      const hist = loadHistory();
      const prev = [...hist];
      const updated = hist.filter((item) => item.id !== deleteId);
      historyCache = updated;
      renderHistoryTab();

      void (async () => {
        try {
          const ok = await window.api.saveHistory(updated);
          if (!ok) throw new Error('Failed to save updated history');
        } catch (err) {
          console.error('Failed to delete history entry:', err);
          historyCache = prev;
          renderHistoryTab();
          const t = translations[getLang()];
          void showAlertFn(t.errorDialogTitle, errorText(err) || t.unknownError, true, t);
        }
      })();
    }
    return;
  }

  // Подключение к серверу
  const iconBtn = target.closest<HTMLElement>('.history-card-icon');
  if (iconBtn) {
    const entryId = iconBtn.getAttribute('data-id');
    const hist = loadHistory();
    const entry = hist.find((item) => item.id === entryId);
    if (entry) {
      const link =
        entry.link ||
        (findServerLinkFn ? findServerLinkFn(entry.server, entry.address, entry.protocol) : null);
      if (link && onSelectServerFn) {
        onSelectServerFn(link);
      } else {
        const t = translations[getLang()];
        void showAlertFn(
          t.alertDialogTitle,
          t.historyServerNotFound,
          false,
          t,
        );
      }
    }
  }
}


export function renderHistoryTab(): void {
  const t = translations[getLang()];
  const history = loadHistory();
  const list = optionalEl('historyList');
  const empty = optionalEl('historyEmpty');
  const statsRow = optionalEl('historyStatsRow');

  if (!list || !empty || !statsRow) return;

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
  setText('historyStatSessions', String(history.length));
  setText('historyStatTime', formatDuration(totalDuration, t));
  setText('historyStatDown', formatBytes(totalDown));
  setText('historyStatUp', formatBytes(totalUp));

  list.innerHTML = '';
  const fragment = document.createDocumentFragment();

  history.forEach((entry: HistoryEntry) => {
    const date = new Date(entry.connectedAt);
    const dateStr = date.toLocaleDateString(t.dateLocale, { day: '2-digit', month: '2-digit' });
    const timeStr = date.toLocaleTimeString(t.dateLocale, { hour: '2-digit', minute: '2-digit' });
    const dur = formatDuration(entry.durationSec, t);
    const down = formatBytes(entry.bytesDown || 0);
    const up = formatBytes(entry.bytesUp || 0);
    const proto = (entry.protocol || '?').toUpperCase();
    const serverName = entry.server || '';

    const isStableEgg =
      entry.server?.toLowerCase().includes('youtube') ||
      entry.server?.toLowerCase().includes('yt') ||
      entry.server?.toLowerCase().includes('goog') ||
      entry.server?.toLowerCase().includes('dpi') ||
      (entry.id && entry.id.charCodeAt(0) % 10 === 0);

    const card = document.createElement('div');
    card.className = 'history-card';

    // Кнопка повторного подключения
    const icon = document.createElement('button');
    icon.type = 'button';
    icon.className = isStableEgg ? 'history-card-icon youtube-easter-egg' : 'history-card-icon';
    icon.dataset.id = entry.id;
    icon.title = t.historyConnectTitle;
    icon.setAttribute('aria-label', `${t.historyConnectTitle}: ${serverName}`);

    const mark = document.createElement('span');
    mark.className = 'history-icon-mark';
    mark.innerHTML = iconSvg('globe', 18);

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
    server.title = serverName;
    server.textContent = serverName;

    const meta = document.createElement('div');
    meta.className = 'history-card-meta';

    const protoChip = document.createElement('span');
    protoChip.className = 'history-proto-chip';
    protoChip.textContent = proto;
    meta.appendChild(protoChip);

    const durItem = document.createElement('span');
    durItem.className = 'history-meta-item';
    durItem.innerHTML = iconSvg('clock', 12);
    const durText = document.createElement('span');
    durText.textContent = dur;
    durItem.appendChild(durText);
    meta.appendChild(durItem);

    const dateItem = document.createElement('span');
    dateItem.className = 'history-meta-item';
    dateItem.innerHTML = iconSvg('calendar', 12);
    const dateText = document.createElement('span');
    dateText.textContent = `${dateStr} ${timeStr}`;
    dateItem.appendChild(dateText);
    meta.appendChild(dateItem);

    body.appendChild(server);
    body.appendChild(meta);

    // Трафик
    const traffic = document.createElement('div');
    traffic.className = 'history-card-traffic';

    const downSpan = document.createElement('span');
    downSpan.className = 'history-traffic-badge down';
    downSpan.innerHTML = `<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="7 10 12 15 17 10"/><line x1="12" y1="15" x2="12" y2="3"/></svg>`;
    const downText = document.createElement('span');
    downText.textContent = down;
    downSpan.appendChild(downText);

    const upSpan = document.createElement('span');
    upSpan.className = 'history-traffic-badge up';
    upSpan.innerHTML = `<svg width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="17 14 12 9 7 14"/><line x1="12" y1="9" x2="12" y2="21"/></svg>`;
    const upText = document.createElement('span');
    upText.textContent = up;
    upSpan.appendChild(upText);

    traffic.appendChild(downSpan);
    traffic.appendChild(upSpan);

    // Кнопка удаления отдельной записи
    const delBtn = document.createElement('button');
    delBtn.type = 'button';
    delBtn.className = 'history-delete-btn';
    delBtn.dataset.deleteId = entry.id;
    delBtn.title = t.historyDeleteTitle;
    delBtn.setAttribute('aria-label', `${t.historyDeleteTitle}: ${serverName}`);
    delBtn.innerHTML = `<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6m3 0V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/></svg>`;

    card.appendChild(icon);
    card.appendChild(body);
    card.appendChild(traffic);
    card.appendChild(delBtn);
    fragment.appendChild(card);
  });

  list.appendChild(fragment);
}

export function applyHistoryLanguage(t: Translations): void {
  setText('historyTitle', t.historyTitle);
  setText('clearHistoryBtnText', t.historyClearBtn);
  setText('historyStatSessionsLabel', t.historyStatSessionsLabel);
  setText('historyStatTimeLabel', t.historyStatTimeLabel);
  setText('historyStatDownLabel', t.historyStatDownLabel);
  setText('historyStatUpLabel', t.historyStatUpLabel);
  setText('historyEmptyText', t.historyEmptyText);

  renderHistoryTab();
}

export function initHistoryView(options: HistoryViewOptions): void {
  getLang = options.getCurrentLanguage;
  if (options.onSelectServer) onSelectServerFn = options.onSelectServer;
  if (options.findServerLink) findServerLinkFn = options.findServerLink;
  showAlertFn = options.showAlert;

  if (isInitialized) return;
  isInitialized = true;

  const list = optionalEl('historyList');
  if (list) {
    list.addEventListener('click', onHistoryListClick);
  }

  const clearBtn = optionalEl('clearHistoryBtn');
  if (clearBtn) {
    clearBtn.onclick = async () => {
      const hist = loadHistory();
      if (hist.length === 0) return;
      const prev = [...hist];
      historyCache = [];
      renderHistoryTab();
      try {
        const ok = await window.api.saveHistory([]);
        if (!ok) throw new Error('Failed to clear history');
      } catch (err) {
        console.error('Failed to clear history:', err);
        historyCache = prev;
        renderHistoryTab();
        const t = translations[getLang()];
        void showAlertFn(t.errorDialogTitle, errorText(err) || t.unknownError, true, t);
      }
    };
  }
}
