import { optionalEl, setText } from '../modules/dom';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';

export interface KillSwitchViewOptions {
  getCurrentLanguage: () => Language;
  getAppState: () => 'on' | 'off' | 'connecting';
  showAlert: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void;
}

let getLang: () => Language = () => 'RU';
let getAppStateFn: () => 'on' | 'off' | 'connecting' = () => 'off';
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void = () => {};

let killSwitchArmed = false;
let killSwitchStuckReported = false;
let isInitialized = false;

export function isKillSwitchArmed(): boolean {
  return killSwitchArmed;
}

export async function refreshKillSwitchStatus(): Promise<void> {
  const box = optionalEl<HTMLInputElement>('killSwitchCheckbox');
  const badge = optionalEl('killSwitchStatusBadge');
  const textEl = optionalEl('killSwitchStatusText');
  if (!box || !badge || !textEl) return;

  const t = translations[getLang()];
  let state = { active: false, stuck: false };
  try {
    state = await window.api.getKillSwitchState();
  } catch (e) {
    console.error('getKillSwitchState failed:', e);
    return;
  }

  badge.className = 'security-status-pill';

  if (state.stuck) {
    badge.classList.add('security-status-pill--danger');
    textEl.textContent = t.killSwitchStatusStuck;
    badge.title = t.killSwitchStuckAlert;
    return;
  }

  if (!box.checked) {
    badge.classList.add('security-status-pill--off');
    textEl.textContent = t.killSwitchStatusOff;
    badge.title = '';
    return;
  }

  const appState = getAppStateFn();
  if (appState === 'on') {
    if (state.active) {
      badge.classList.add('security-status-pill--active');
      textEl.textContent = t.killSwitchStatusActive;
      badge.title = t.killSwitchBadgeTitle;
    } else {
      badge.classList.add('security-status-pill--pending');
      textEl.textContent = t.killSwitchStatusPending;
      badge.title = t.killSwitchBadgeTitle;
    }
  } else {
    badge.classList.add('security-status-pill--standby');
    textEl.textContent = t.killSwitchStatusStandby;
    badge.title = '';
  }
}

export function updateInterceptionGuidance(): void {
  const tunBox = optionalEl<HTMLInputElement>('tunModeCheckbox');
  const proxyBox = optionalEl<HTMLInputElement>('systemProxyCheckbox');
  const banner = optionalEl('networkInterceptionHint');
  const textEl = optionalEl('networkInterceptionText');
  if (!tunBox || !proxyBox || !banner || !textEl) return;

  const t = translations[getLang()];
  const isTun = tunBox.checked;
  const isProxy = proxyBox.checked;

  banner.classList.remove('settings-interception-banner--warning');

  if (isTun && isProxy) {
    textEl.textContent = t.interceptionBoth;
  } else if (isTun && !isProxy) {
    textEl.textContent = t.interceptionTunOnly;
  } else if (!isTun && isProxy) {
    textEl.textContent = t.interceptionProxyOnly;
  } else {
    banner.classList.add('settings-interception-banner--warning');
    textEl.textContent = t.interceptionNone;
  }
}

export async function refreshKillSwitchBadge(): Promise<void> {
  void refreshKillSwitchStatus();
  const badge = optionalEl('killSwitchBadge');
  if (!badge) return;

  let state = { active: false, stuck: false };
  try {
    state = await window.api.getKillSwitchState();
  } catch (e) {
    console.error('getKillSwitchState failed:', e);
    return;
  }

  const t = translations[getLang()];
  killSwitchArmed = state.active;

  badge.style.display = state.active ? 'flex' : 'none';
  badge.classList.toggle('status-badge--danger', state.stuck);
  badge.classList.toggle('status-badge--warning', !state.stuck);
  setText('killSwitchBadgeText', state.stuck ? t.killSwitchStuck : t.killSwitchBadge);
  badge.title = state.stuck ? t.killSwitchStuckAlert : t.killSwitchBadgeTitle;

  if (state.stuck && !killSwitchStuckReported) {
    killSwitchStuckReported = true;
    badge.style.display = 'flex';
    void showAlertFn(t.errorDialogTitle, t.killSwitchStuckAlert, true, t);
  }
}

export function initKillSwitchView(options: KillSwitchViewOptions): void {
  getLang = options.getCurrentLanguage;
  getAppStateFn = options.getAppState;
  showAlertFn = options.showAlert;

  if (isInitialized) return;
  isInitialized = true;

  void refreshKillSwitchBadge();
  window.api.onStarted(() => void refreshKillSwitchBadge());
  window.api.onStopped(() => void refreshKillSwitchBadge());
}
