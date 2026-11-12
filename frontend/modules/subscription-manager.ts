import { showConfirm, showPrompt } from './ui-utils';
import { iconSvg } from './icons';
import type { Subscription } from './api';
import type { Language, Translations } from './translations';

export type { Subscription };

/** Идентификатор выбранной вкладки: id подписки либо один из двух псевдо-списков. */
export type SubscriptionTabId = string | 'all' | 'favorites';

export let allSubscriptions: Subscription[] = [];
export let currentActiveSubId: SubscriptionTabId = 'all';

/**
 * Пункт контекстного меню вкладки: иконка плюс подпись.
 *
 * Иконка вставляется разметкой из своего набора, подпись — текстовым узлом:
 * имя подписки задаёт человек, и через innerHTML оно попадать не должно.
 *
 * Оформление здесь классом, а не десятком присвоений element.style, как было
 * раньше у каждого из двух пунктов по отдельности. Правило после ужесточения
 * CSP звучит так: разметка получает классы, а скрипты меняют только то, что
 * действительно меняется на ходу; наведение мыши к таковому не относится и
 * принадлежит :hover.
 */
function styleMenuItem(iconHtml: string, label: string): HTMLDivElement {
  const item = document.createElement('div');
  item.className = 'tab-context-item';
  item.innerHTML = iconHtml;
  item.appendChild(document.createTextNode(label));
  return item;
}

export async function loadSubscriptions(callback?: () => void): Promise<void> {
  allSubscriptions = await window.api.getSubscriptions();
  if (callback) callback();
}

export function renderSubTabs(
  container: HTMLElement | null,
  translations: Record<Language, Translations>,
  currentLanguage: Language,
  onTabChange: () => void,
  reload: () => void | Promise<void>,
): void {
  if (!container) return;
  const t = translations[currentLanguage];
  container.innerHTML = '';

  const createTab = (id: SubscriptionTabId, name: string): HTMLButtonElement => {
    const btn = document.createElement('button');
    btn.className = `btn-glass ${currentActiveSubId === id ? 'active' : ''}`;
    btn.style.padding = '6px 15px';
    btn.style.position = 'relative';

    let displayName = id === 'all' ? t.allServersTab : name;
    if (id !== 'all') {
      const sub = allSubscriptions.find((s) => s.id === id);
      if (sub && sub.loading) {
        displayName += ' ⏳';
      }
    }
    btn.textContent = displayName;

    btn.onclick = () => {
      currentActiveSubId = id;
      onTabChange();
    };

    if (id !== 'all') {
      btn.draggable = true;

      btn.ondragstart = (e) => {
        e.dataTransfer?.setData('text/plain', id);
        btn.classList.add('dragging');
        btn.style.opacity = '0.5';
      };

      btn.ondragend = () => {
        btn.classList.remove('dragging');
        btn.style.opacity = '1';
      };

      btn.ondragover = (e) => {
        e.preventDefault();
      };

      btn.ondrop = async (e) => {
        e.preventDefault();
        const draggedId = e.dataTransfer?.getData('text/plain');
        if (draggedId && draggedId !== id && draggedId !== 'all') {
          const draggedIndex = allSubscriptions.findIndex((s) => s.id === draggedId);
          const targetIndex = allSubscriptions.findIndex((s) => s.id === id);
          if (draggedIndex !== -1 && targetIndex !== -1) {
            const [draggedSub] = allSubscriptions.splice(draggedIndex, 1);
            allSubscriptions.splice(targetIndex, 0, draggedSub);
            await window.api.saveSubscriptions(allSubscriptions);
            await reload();
          }
        }
      };

      btn.oncontextmenu = (e) => {
        e.preventDefault();
        document.querySelectorAll('.tab-context-menu').forEach((m) => m.remove());

        const menu = document.createElement('div');
        menu.className = 'tab-context-menu';
        menu.style.position = 'fixed';
        menu.style.top = `${e.clientY}px`;
        menu.style.left = `${e.clientX}px`;
        menu.style.background = 'var(--card-bg)';
        menu.style.border = '1px solid var(--glass-border)';
        menu.style.borderRadius = '8px';
        menu.style.padding = '5px';
        menu.style.zIndex = '1000';
        menu.style.boxShadow = '0 10px 25px rgb(var(--shadow-rgb) / 0.5)';

        const renameItem = styleMenuItem(iconSvg('edit', 13), t.renameItem);

        renameItem.onclick = async () => {
          menu.remove();
          const newName = await showPrompt(t.renamePromptTitle, name);
          if (newName && newName.trim() !== '') {
            const sub = allSubscriptions.find((s) => s.id === id);
            if (sub) {
              sub.name = newName.trim();
              await window.api.saveSubscriptions(allSubscriptions);
              await reload();
            }
          }
        };

        const deleteItem = styleMenuItem(iconSvg('trash', 13), t.deleteItem);
        deleteItem.classList.add('tab-context-item--danger');

        deleteItem.onclick = async () => {
          menu.remove();
          const confirmed = await showConfirm(t.deleteConfirm.replace('{name}', name));
          if (confirmed) {
            allSubscriptions = allSubscriptions.filter((s) => s.id !== id);
            await window.api.saveSubscriptions(allSubscriptions);
            if (currentActiveSubId === id) currentActiveSubId = 'all';
            await reload();
          }
        };

        // «Обновить сейчас» и «Период обновления» — рядом с переименованием, а
        // не в шапке экрана: оба действия относятся к одной подписке, и
        // выбирать её всё равно приходится этим же щелчком.
        const sub = allSubscriptions.find((s) => s.id === id);
        const updateItem = sub?.url ? styleMenuItem(iconSvg('refresh', 13), t.subUpdateNow) : null;
        if (updateItem) {
          updateItem.onclick = async () => {
            menu.remove();
            await window.api.updateSubscriptionNow(id);
            // Бэкенд шлёт 'subscriptions-updated' по завершении, но список в
            // памяти обновляет только перечитывание — на него подписан
            // renderer, а здесь достаточно дождаться вызова.
            await reload();
          };
        }

        const intervalItem = sub?.url ? styleMenuItem(iconSvg('clock', 13), t.subIntervalItem) : null;
        if (intervalItem) {
          intervalItem.onclick = async () => {
            menu.remove();
            const current = String(sub?.intervalHours || 24);
            const answer = await showPrompt(t.subIntervalPrompt, current);
            if (answer === null) return;
            const hours = Number(answer.trim());
            // Границы те же, что в Go: интерфейс не должен уметь записать то,
            // что бэкенд всё равно подрежет молча.
            if (!Number.isFinite(hours) || hours < 1 || hours > 720) return;
            const target = allSubscriptions.find((s) => s.id === id);
            if (!target) return;
            target.intervalHours = Math.round(hours);
            await window.api.saveSubscriptions(allSubscriptions);
            await reload();
          };
        }

        if (updateItem) menu.appendChild(updateItem);
        if (intervalItem) menu.appendChild(intervalItem);
        menu.appendChild(renameItem);
        menu.appendChild(deleteItem);
        document.body.appendChild(menu);

        const closeMenu = () => {
          menu.remove();
          document.removeEventListener('click', closeMenu);
        };
        setTimeout(() => document.addEventListener('click', closeMenu), 10);
      };
    }

    return btn;
  };

  container.appendChild(createTab('all', t.allServersTab));
  container.appendChild(createTab('favorites', t.favoritesTab));
  allSubscriptions.forEach((sub) => {
    container.appendChild(createTab(sub.id, sub.name));
  });
}

/** Период обновления по умолчанию. Зеркалит defaultUpdateIntervalHours в Go. */
const DEFAULT_INTERVAL_HOURS = 24;

/**
 * «12 минут назад» словами. Огрублённо намеренно: точное время последней
 * загрузки никому не нужно, а нужен ответ на вопрос «список свежий или нет».
 */
function humanAge(ms: number, t: Translations): string {
  const minutes = Math.max(0, Math.round((Date.now() - ms) / 60000));
  if (minutes < 1) return t.subAgeJustNow;
  if (minutes < 60) return t.subAgeMinutes.replace('{n}', String(minutes));
  const hours = Math.round(minutes / 60);
  if (hours < 24) return t.subAgeHours.replace('{n}', String(hours));
  return t.subAgeDays.replace('{n}', String(Math.round(hours / 24)));
}

/**
 * Рисует строку состояния автообновления для выбранной подписки.
 *
 * До неё провалившаяся загрузка не оставляла следа нигде: пользователь видел
 * устаревший список серверов и никакого объяснения. Это ровно то, что принцип
 * «когда приложение чего-то не сделало, оно говорит, чего и почему» запрещает.
 *
 * На псевдо-вкладках «Все» и «Избранное» строка пуста: они не подписки, и
 * говорить об их обновлении нечего.
 */
function formatBytes(bytes: number): string {
  if (bytes <= 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  const safeI = Math.min(sizes.length - 1, Math.max(0, i));
  const val = bytes / Math.pow(k, safeI);
  return `${val.toFixed(safeI > 1 ? 1 : 0)} ${sizes[safeI]}`;
}

export function renderSubStatus(
  element: HTMLElement | null,
  translations: Record<Language, Translations>,
  currentLanguage: Language,
): void {
  if (!element) return;
  const t = translations[currentLanguage];

  element.innerHTML = '';
  element.classList.remove('sub-status-line--error');
  element.classList.remove('sub-status-line--warning');

  const sub = allSubscriptions.find((s) => s.id === currentActiveSubId);
  if (!sub || !sub.url) {
    return;
  }

  const every = t.subEvery.replace('{n}', String(sub.intervalHours || DEFAULT_INTERVAL_HOURS));

  if (sub.lastError) {
    if (sub.links && sub.links.length > 0) {
      const errSpan = document.createElement('span');
      errSpan.className = 'sub-error-text';
      errSpan.textContent = t.subUpdateFailed.replace('{reason}', sub.lastError);

      const cacheBadge = document.createElement('span');
      cacheBadge.className = 'sub-cache-badge';
      cacheBadge.textContent = t.subUsingCache.replace('{n}', String(sub.links.length));

      element.appendChild(errSpan);
      element.appendChild(cacheBadge);
      element.classList.add('sub-status-line--warning');
      return;
    }

    // Ошибка вытесняет всё остальное: рядом с ней «обновлено вчера» только
    // отвлекает от того, что обновиться не удалось.
    element.textContent = t.subUpdateFailed.replace('{reason}', sub.lastError);
    element.classList.add('sub-status-line--error');
    return;
  }

  const metaRow = document.createElement('div');
  metaRow.className = 'sub-status-meta';

  const updateSpan = document.createElement('span');
  updateSpan.className = 'sub-update-time';
  updateSpan.textContent = sub.updatedAt
    ? `${t.subUpdatedAgo.replace('{age}', humanAge(sub.updatedAt, t))} · ${every}`
    : `${t.subNeverUpdated} · ${every}`;
  metaRow.appendChild(updateSpan);

  // Expiration badge if expire is specified
  if (sub.userInfo && sub.userInfo.expire > 0) {
    const expireSec = sub.userInfo.expire;
    const nowSec = Math.floor(Date.now() / 1000);
    const diffSec = expireSec - nowSec;
    const diffDays = Math.ceil(diffSec / 86400);

    const badge = document.createElement('span');
    badge.className = 'sub-expire-badge';

    if (diffSec <= 0) {
      badge.classList.add('is-expired');
      badge.textContent = t.subExpired;
    } else if (diffDays <= 1) {
      badge.classList.add('is-warning');
      badge.textContent = t.subExpiresToday;
    } else {
      badge.textContent = t.subExpiresIn.replace('{days}', String(diffDays));
    }
    metaRow.appendChild(badge);
  }

  element.appendChild(metaRow);

  // Traffic quota box if userInfo is specified
  if (sub.userInfo) {
    const usedBytes = (sub.userInfo.upload || 0) + (sub.userInfo.download || 0);
    const totalBytes = sub.userInfo.total || 0;

    const quotaBox = document.createElement('div');
    quotaBox.className = 'sub-quota-box';

    const labelsRow = document.createElement('div');
    labelsRow.className = 'sub-quota-labels';

    if (totalBytes > 0) {
      const pct = Math.min(100, Math.max(0, (usedBytes / totalBytes) * 100));
      const remainingBytes = Math.max(0, totalBytes - usedBytes);

      const usedSpan = document.createElement('span');
      usedSpan.textContent =
        t.subQuotaUsed
          .replace('{used}', formatBytes(usedBytes))
          .replace('{total}', formatBytes(totalBytes)) + ` (${pct.toFixed(1)}%)`;
      labelsRow.appendChild(usedSpan);

      const remSpan = document.createElement('span');
      remSpan.textContent = t.subQuotaRemaining.replace('{remaining}', formatBytes(remainingBytes));
      labelsRow.appendChild(remSpan);

      quotaBox.appendChild(labelsRow);

      const track = document.createElement('div');
      track.className = 'sub-quota-track';

      const fill = document.createElement('div');
      fill.className = 'sub-quota-fill';
      if (pct >= 95) {
        fill.classList.add('danger');
      } else if (pct >= 80) {
        fill.classList.add('warning');
      } else {
        fill.classList.add('normal');
      }
      fill.style.width = `${pct.toFixed(1)}%`;
      track.appendChild(fill);
      quotaBox.appendChild(track);
    } else if (usedBytes > 0) {
      const usedSpan = document.createElement('span');
      usedSpan.textContent = t.subQuotaUnlimited.replace('{used}', formatBytes(usedBytes));
      labelsRow.appendChild(usedSpan);
      quotaBox.appendChild(labelsRow);
    }

    if (labelsRow.hasChildNodes()) {
      element.appendChild(quotaBox);
    }
  }
}

export function setActiveSubId(id: SubscriptionTabId): void {
  currentActiveSubId = id;
}

export function setSubscriptions(subs: Subscription[]): void {
  allSubscriptions = subs;
}
