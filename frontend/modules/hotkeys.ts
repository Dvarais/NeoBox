import { el, optionalEl, query } from './dom';
import { isDialogOpen } from './ui-utils';
import { translations } from './translations';
import type { Language, Translations } from './translations';

export interface HotkeysOptions {
  getCurrentLanguage: () => Language;
  onSelectTab: (index: number) => void;
  onOpenServerSearch: () => void;
  onToggleConnection: () => void;
  onSaveSettings: () => void;
  showAlert: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void;
}

let getLang: () => Language = () => 'RU';
let onSelectTabFn: (index: number) => void = () => {};
let onOpenServerSearchFn: () => void = () => {};
let onToggleConnectionFn: () => void = () => {};
let onSaveSettingsFn: () => void = () => {};
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void = () => {};

let isInitialized = false;
const modifierKeys = new Set(['Control', 'Shift', 'Alt', 'Meta']);

export function syncHotkeysDisabled(): void {
  const checkbox = optionalEl<HTMLInputElement>('globalHotkeysCheckbox');
  if (!checkbox) return;
  const isEnabled = checkbox.checked;
  const row = query('.hotkey-row');
  const toggleInput = optionalEl<HTMLInputElement>('hotkeyToggleInput');
  const showInput = optionalEl<HTMLInputElement>('hotkeyShowInput');
  if (row) row.classList.toggle('hotkey-row--disabled', !isEnabled);
  if (toggleInput) toggleInput.disabled = !isEnabled;
  if (showInput) showInput.disabled = !isEnabled;
}

export async function applyGlobalHotkeys(enable: boolean, announce: boolean): Promise<void> {
  const toggleInput = optionalEl<HTMLInputElement>('hotkeyToggleInput');
  const showInput = optionalEl<HTMLInputElement>('hotkeyShowInput');
  const toggleVal = toggleInput ? toggleInput.value : '';
  const showVal = showInput ? showInput.value : '';

  const failure = await window.api.setGlobalHotkeys(enable, toggleVal, showVal);
  if (!failure) {
    syncHotkeysDisabled();
    return;
  }

  const t = translations[getLang()];
  const checkbox = optionalEl<HTMLInputElement>('globalHotkeysCheckbox');
  if (checkbox) checkbox.checked = false;
  syncHotkeysDisabled();

  if (announce) {
    void showAlertFn(t.errorDialogTitle, t.globalHotkeysFailed.replace('{reason}', failure), false, t);
  }
  onSaveSettingsFn();
}

export function bindHotkeyInput(id: string): void {
  const input = optionalEl<HTMLInputElement>(id);
  if (!input) return;

  input.addEventListener('keydown', (e: KeyboardEvent) => {
    if (modifierKeys.has(e.key)) return;

    const parts: string[] = [];
    if (e.ctrlKey) parts.push('Ctrl');
    if (e.shiftKey) parts.push('Shift');
    if (e.altKey) parts.push('Alt');
    if (e.metaKey) parts.push('Win');

    const key = /^Key[A-Z]$/.test(e.code)
      ? e.code.slice(3)
      : /^Digit[0-9]$/.test(e.code)
        ? e.code.slice(5)
        : /^F([1-9]|1[0-2])$/.test(e.code)
          ? e.code
          : '';

    if (!key) return;
    e.preventDefault();

    parts.push(key);
    input.value = parts.join('+');
    input.blur();
    onSaveSettingsFn();

    const checkbox = optionalEl<HTMLInputElement>('globalHotkeysCheckbox');
    void applyGlobalHotkeys(checkbox ? checkbox.checked : false, true);
  });
}

function handleAppShortcut(e: KeyboardEvent): void {
  if (!e.ctrlKey || e.altKey || e.metaKey || e.repeat) return;
  if (isDialogOpen()) return;

  // Ctrl+1..7
  if (e.key >= '1' && e.key <= '7') {
    e.preventDefault();
    onSelectTabFn(Number(e.key) - 1);
    return;
  }

  const key = e.key.toLowerCase();

  // Ctrl+K — search
  if (key === 'k') {
    e.preventDefault();
    onOpenServerSearchFn();
    return;
  }

  // Ctrl+Enter — connect / disconnect
  if (e.key === 'Enter') {
    const active = document.activeElement;
    if (active instanceof HTMLTextAreaElement) return;
    e.preventDefault();
    onToggleConnectionFn();
  }
}

export function initHotkeys(options: HotkeysOptions): void {
  getLang = options.getCurrentLanguage;
  onSelectTabFn = options.onSelectTab;
  onOpenServerSearchFn = options.onOpenServerSearch;
  onToggleConnectionFn = options.onToggleConnection;
  onSaveSettingsFn = options.onSaveSettings;
  showAlertFn = options.showAlert;

  if (isInitialized) return;
  isInitialized = true;

  document.addEventListener('keydown', handleAppShortcut);

  const globalCheckbox = optionalEl<HTMLInputElement>('globalHotkeysCheckbox');
  if (globalCheckbox) {
    globalCheckbox.addEventListener('change', (e) => {
      syncHotkeysDisabled();
      onSaveSettingsFn();
      void applyGlobalHotkeys((e.target as HTMLInputElement).checked, true);
    });
  }

  bindHotkeyInput('hotkeyToggleInput');
  bindHotkeyInput('hotkeyShowInput');

  if (window.api?.onHotkeyToggleConnection) {
    window.api.onHotkeyToggleConnection(() => onToggleConnectionFn());
  }
  if (window.api?.onHotkeyShowWindow) {
    window.api.onHotkeyShowWindow(() => void window.api.bringToFront());
  }
}
