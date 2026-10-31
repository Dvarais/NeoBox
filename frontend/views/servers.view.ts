import { optionalEl, setText, setTitle } from '../modules/dom';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import {
  allSubscriptions,
  currentActiveSubId,
} from '../modules/subscription-manager';
import {
  currentSortMode,
  parseBasicInfo,
  pingData,
  renderCards,
  setPingData,
  setSortMode,
  updateCardPing,
} from '../modules/server-manager';
import type { SortMode } from '../modules/server-manager';

export interface ServersViewOptions {
  getCurrentLanguage: () => Language;
  getActiveServerLink: () => string | null;
  setActiveServer: (link: string, name: string, type: string, address: string) => void;
  getAppState: () => 'on' | 'off' | 'connecting';
  restartCore: () => void;
  startCore: (link: string) => Promise<void>;
  onSaveSettings: () => Promise<unknown> | unknown;
  showAlert: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void;
  showConfirm: (message: string) => Promise<boolean>;
}

let getLang: () => Language = () => 'RU';
let getActiveServerLinkFn: () => string | null = () => null;
let setActiveServerFn: (link: string, name: string, type: string, address: string) => void = () => {};
let getAppStateFn: () => 'on' | 'off' | 'connecting' = () => 'off';
let restartCoreFn: () => void = () => {};
let startCoreFn: (link: string) => Promise<void> = async () => {};
let onSaveSettingsFn: () => Promise<unknown> | unknown = () => {};
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void = () => {};
let showConfirmFn: (message: string) => Promise<boolean> = async () => true;

let favoriteLinks = new Set<string>();
let serverSearchQuery = '';
let isInitialized = false;

export function getFavoriteLinks(): string[] {
  return Array.from(favoriteLinks);
}

export function setFavoriteLinks(links: string[]): void {
  favoriteLinks = new Set(Array.isArray(links) ? links : []);
  updateCards();
}

export function getServerSearchQuery(): string {
  return serverSearchQuery;
}

export function onToggleFavorite(link: string): void {
  if (favoriteLinks.has(link)) {
    favoriteLinks.delete(link);
  } else {
    favoriteLinks.add(link);
  }
  updateCards();
  Promise.resolve(onSaveSettingsFn()).catch((err) =>
    console.error('Failed to save settings on toggle favorite:', err),
  );
}

export function updateCards(): void {
  const serversGrid = optionalEl('serversGrid');
  if (!serversGrid) return;

  let servers: string[] = [];
  if (currentActiveSubId === 'all') {
    allSubscriptions.forEach((s) => servers.push(...s.links));
  } else if (currentActiveSubId === 'favorites') {
    servers = Array.from(favoriteLinks);
  } else {
    const sub = allSubscriptions.find((s) => s.id === currentActiveSubId);
    if (sub) servers = sub.links;
  }

  const currentLang = getLang();
  const t = translations[currentLang];
  const activeLink = getActiveServerLinkFn();

  renderCards(
    serversGrid,
    servers,
    activeLink,
    pingData,
    currentSortMode,
    (link, name, type, address) => {
      const isNewServer = activeLink !== link;
      setActiveServerFn(link, name, type, address);
      updateCards();
      Promise.resolve(onSaveSettingsFn()).catch((err) =>
        console.error('Failed to save settings on server select:', err),
      );
      const appState = getAppStateFn();
      if (isNewServer && (appState === 'on' || appState === 'connecting')) {
        if (appState === 'on' && window.api?.switchServer) {
          void (async () => {
            try {
              const res = await window.api.switchServer(link);
              if (res && res.hotSwitched) {
                return;
              }
            } catch (e) {
              console.warn('Hot switch failed on server select, falling back to restart:', e);
            }
            restartCoreFn();
          })();
          return;
        }
        restartCoreFn();
      }
    },
    serverSearchQuery,
    favoriteLinks,
    onToggleFavorite,
    t,
  );

  const countBadge = optionalEl('serversCountBadge');
  if (countBadge) {
    const count = serversGrid.children.length;
    countBadge.textContent =
      count > 0 ? (currentLang === 'RU' ? `${count} серв.` : `${count} servers`) : '';
  }

  const emptyEl = optionalEl('serversEmpty');
  if (emptyEl) {
    const isEmpty = serversGrid.children.length === 0;
    emptyEl.style.display = isEmpty ? 'flex' : 'none';
    if (isEmpty) {
      const textEl = optionalEl('serversEmptyText');
      if (textEl) {
        textEl.textContent = allSubscriptions.length === 0 ? t.noSubscriptions : t.noSearchResults;
      }
    }
  }
}

export async function runAutoBestServer(): Promise<void> {
  const currentLang = getLang();
  const t = translations[currentLang];
  const bestServerBtn = optionalEl<HTMLButtonElement>('bestServerBtn');
  const bestServerBtnText = optionalEl('bestServerBtnText');

  const links: string[] = [];
  allSubscriptions.forEach((s) => links.push(...s.links));
  const uniqueLinks = Array.from(new Set(links));
  if (uniqueLinks.length === 0) return;

  uniqueLinks.forEach((l) => {
    setPingData(l, 'pinging');
    window.api.pingServer(l);
  });
  updateCards();

  if (bestServerBtn) bestServerBtn.disabled = true;
  const originalText = bestServerBtnText ? bestServerBtnText.textContent : '';
  if (bestServerBtnText) bestServerBtnText.textContent = t.bestServerSearching;

  setTimeout(async () => {
    if (bestServerBtn) bestServerBtn.disabled = false;
    if (bestServerBtnText && originalText) bestServerBtnText.textContent = originalText;

    let bestLink: string | null = null;
    let minPing = Infinity;
    uniqueLinks.forEach((l) => {
      const ping = pingData[l];
      if (typeof ping === 'number' && ping > 0 && ping < minPing) {
        minPing = ping;
        bestLink = l;
      }
    });

    if (bestLink) {
      const activeLink = getActiveServerLinkFn();
      const isNewServer = activeLink !== bestLink;
      const appState = getAppStateFn();
      const wasActive = appState === 'on' || appState === 'connecting';

      if (wasActive && isNewServer) {
        const info = parseBasicInfo(bestLink);
        const confirmMsg = t.bestServerSwitchConfirm.replace('{name}', info.name);
        const confirmed = await showConfirmFn(confirmMsg);
        if (!confirmed) return;
      } else if (wasActive && !isNewServer) {
        void showAlertFn(t.alertDialogTitle, t.bestServerAlready, false, t);
        return;
      }

      const info = parseBasicInfo(bestLink);
      setActiveServerFn(bestLink, info.name, info.type.toUpperCase(), info.address);
      updateCards();
      Promise.resolve(onSaveSettingsFn()).catch((err) =>
        console.error('Failed to save settings on auto best server:', err),
      );

      if (wasActive) {
        if (appState === 'on' && window.api?.switchServer) {
          try {
            const res = await window.api.switchServer(bestLink);
            if (res && res.hotSwitched) {
              return;
            }
          } catch (e) {
            console.warn('Hot switch failed on best server, falling back to restart:', e);
          }
        }
        restartCoreFn();
      } else {
        await startCoreFn(bestLink);
      }
    } else {
      void showAlertFn(t.alertDialogTitle, t.bestServerFailed, false, t);
    }
  }, 2000);
}

export function pingAllServers(): void {
  const links: string[] = [];
  allSubscriptions.forEach((s) => links.push(...s.links));
  Array.from(new Set(links)).forEach((l) => {
    setPingData(l, 'pinging');
    window.api.pingServer(l);
  });
  updateCards();
}

const CARDS_UPDATE_COALESCE_MS = 250;
let cardsUpdateTimer: ReturnType<typeof setTimeout> | null = null;

export function scheduleCardsUpdate(): void {
  if (cardsUpdateTimer !== null) return;
  cardsUpdateTimer = setTimeout(() => {
    cardsUpdateTimer = null;
    updateCards();
  }, CARDS_UPDATE_COALESCE_MS);
}

export function handlePingResult(link: string, latency: number): void {
  setPingData(link, latency);
  if (currentSortMode === 'ping') {
    scheduleCardsUpdate();
  } else {
    updateCardPing(link, latency);
  }
}

export function applyServersLanguage(t: Translations): void {
  setText('myLocationsTitle', t.myLocations);
  const pingAllText = optionalEl('pingAllBtnText');
  if (pingAllText) {
    pingAllText.textContent = t.pingAllBtn;
  } else {
    setText('pingAllBtn', t.pingAllBtn);
  }
  setText('sortBtnText', t.sortBtn);

  setTitle('serverSearchInput', `${t.serverSearchPlaceholder} (Ctrl+K)`);
  setTitle('bestServerBtn', t.bestServerBtnTitle);
  setText('bestServerBtnText', t.bestServerBtn);
  const searchInput = optionalEl<HTMLInputElement>('serverSearchInput');
  if (searchInput) searchInput.placeholder = t.serverSearchPlaceholder;

  document.querySelectorAll<HTMLElement>('.sort-item, .sort-dropdown-item').forEach((item) => {
    const mode = item.dataset.sort ?? '';
    const label = item.querySelector('span');
    const sortKey = `sort${mode.charAt(0).toUpperCase() + mode.slice(1)}` as keyof Translations;
    if (label && t[sortKey]) label.textContent = t[sortKey] as string;
  });

  updateCards();
}

export function initServersView(options: ServersViewOptions): void {
  getLang = options.getCurrentLanguage;
  getActiveServerLinkFn = options.getActiveServerLink;
  setActiveServerFn = options.setActiveServer;
  getAppStateFn = options.getAppState;
  restartCoreFn = options.restartCore;
  startCoreFn = options.startCore;
  onSaveSettingsFn = options.onSaveSettings;
  showAlertFn = options.showAlert;
  showConfirmFn = options.showConfirm;

  if (isInitialized) return;
  isInitialized = true;

  const serverSearchInput = optionalEl<HTMLInputElement>('serverSearchInput');
  if (serverSearchInput) {
    serverSearchInput.addEventListener('input', (e) => {
      serverSearchQuery = (e.target as HTMLInputElement).value;
      updateCards();
    });
  }

  const bestServerBtn = optionalEl<HTMLButtonElement>('bestServerBtn');
  if (bestServerBtn) {
    bestServerBtn.onclick = () => void runAutoBestServer();
  }

  const pingAllBtn = optionalEl<HTMLButtonElement>('pingAllBtn');
  if (pingAllBtn) {
    pingAllBtn.onclick = () => pingAllServers();
  }

  // Keyboard navigation and custom sort dropdown
  const sortDropdown = document.querySelector<HTMLElement>('.sort-dropdown');
  const sortMenu = document.querySelector<HTMLElement>('.sort-menu');
  const sortBtn = optionalEl<HTMLButtonElement>('sortBtn');
  const sortItems = Array.from(document.querySelectorAll<HTMLElement>('.sort-item'));
  let sortMenuTimeout: ReturnType<typeof setTimeout> | null = null;

  function setSortMenuOpen(open: boolean): void {
    if (!sortMenu || !sortBtn) return;
    sortMenu.classList.toggle('show', open);
    sortBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
  }

  if (sortDropdown && sortMenu && sortBtn) {
    sortDropdown.addEventListener('mouseenter', () => {
      if (sortMenuTimeout) clearTimeout(sortMenuTimeout);
      setSortMenuOpen(true);
    });

    sortDropdown.addEventListener('mouseleave', () => {
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
  }

  sortItems.forEach((item, index) => {
    item.addEventListener('click', () => {
      sortItems.forEach((i) => {
        i.classList.remove('active');
        i.setAttribute('aria-checked', 'false');
      });
      item.classList.add('active');
      item.setAttribute('aria-checked', 'true');
      const mode = item.dataset.sort as SortMode;
      if (mode) {
        setSortMode(mode);
        updateCards();
      }
      setSortMenuOpen(false);
      if (sortBtn) sortBtn.focus();
    });

    item.addEventListener('keydown', (e: KeyboardEvent) => {
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') return;
      e.preventDefault();
      const delta = e.key === 'ArrowDown' ? 1 : -1;
      sortItems[(index + delta + sortItems.length) % sortItems.length]?.focus();
    });
  });
}
