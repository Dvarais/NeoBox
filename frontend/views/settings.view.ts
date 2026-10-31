import { el, optionalEl, query, setText, syncCustomSelectLabels } from '../modules/dom';
import { errorText, trapFocus } from '../modules/ui-utils';
import { translations, translate } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import type { LanIPInfo } from '../modules/api';
import {
  DEFAULT_THEME,
  PRESETS,
  applyTheme,
  describeTheme,
  deriveTokens,
  parseTheme,
} from '../modules/theme';
import type { BackgroundMode, Scheme, ThemeSpec } from '../modules/theme';

export const THEME_STORAGE_KEY = 'neobox-theme';

export interface SettingsViewOptions {
  getCurrentLanguage: () => Language;
  isCoreActive: () => boolean;
  onSaveSettings: () => Promise<unknown> | unknown;
  onMarkRulesPending: () => void;
  onRefreshKillSwitch?: () => void;
  onUpdateInterceptionGuidance?: () => void;
  showAlert: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void;
  showConfirm: (message: string) => Promise<boolean>;
}

let getLang: () => Language = () => 'RU';
let isCoreActiveFn: () => boolean = () => false;
let onSaveSettingsFn: () => Promise<unknown> | unknown = () => {};
let onMarkRulesPendingFn: () => void = () => {};
let onRefreshKillSwitchFn: () => void = () => {};
let onUpdateInterceptionGuidanceFn: () => void = () => {};
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void = () => {};
let showConfirmFn: (message: string) => Promise<boolean> = async () => false;

let currentTheme: ThemeSpec = DEFAULT_THEME;
let themeFrame = 0;
let themeSaveTimer: ReturnType<typeof setTimeout> | null = null;
let appliedDns = '';
let dnsCheckTimer: ReturnType<typeof setTimeout> | null = null;
let isInitialized = false;

let lanSharingRelease: (() => void) | null = null;
let lanIPs: LanIPInfo[] = [];
let currentLanIP = '127.0.0.1';
let currentLanProtocol: 'socks5' | 'http' = 'socks5';
const LAN_PROXY_PORT = 20809;

export function closeLanSharingModal(): void {
  const overlay = optionalEl('lanSharingModalOverlay');
  if (overlay) overlay.style.display = 'none';
  lanSharingRelease?.();
  lanSharingRelease = null;
}

export async function openLanSharingModal(): Promise<void> {
  const overlay = optionalEl('lanSharingModalOverlay');
  if (!overlay) return;

  const currentLang = getLang();
  const t = translations[currentLang];

  overlay.style.display = 'flex';
  lanSharingRelease = trapFocus(overlay, optionalEl('lanSharingCloseBtn'), closeLanSharingModal);

  const select = optionalEl<HTMLSelectElement>('lanInterfaceSelect');
  const qrLoading = optionalEl('lanQrLoading');
  if (qrLoading) qrLoading.style.display = 'flex';

  try {
    lanIPs = await window.api.getLanIPs();
  } catch (err) {
    console.error('Failed to get LAN IPs:', err);
    lanIPs = [];
  }

  if (select) {
    select.innerHTML = '';
    if (lanIPs.length === 0) {
      const opt = document.createElement('option');
      opt.value = '127.0.0.1';
      opt.textContent = t.lanNoInterfaces;
      select.appendChild(opt);
      currentLanIP = '127.0.0.1';
    } else {
      for (const info of lanIPs) {
        const opt = document.createElement('option');
        opt.value = info.ip;
        opt.textContent = `${info.name} (${info.ip})`;
        select.appendChild(opt);
      }
      currentLanIP = lanIPs[0].ip;
    }
  }

  const protoSelect = optionalEl<HTMLSelectElement>('lanProtocolSelect');
  if (protoSelect) {
    currentLanProtocol = protoSelect.value as 'socks5' | 'http';
  }

  await updateLanSharingDisplay();
}

async function updateLanSharingDisplay(): Promise<void> {
  const qrImg = optionalEl<HTMLImageElement>('lanQrImage');
  const qrLoading = optionalEl('lanQrLoading');
  const addrVal = optionalEl('lanAddressValue');

  const address = `${currentLanIP}:${LAN_PROXY_PORT}`;
  if (addrVal) addrVal.textContent = address;

  const proxyURI = `${currentLanProtocol}://${currentLanIP}:${LAN_PROXY_PORT}#NeoBox-LAN`;

  if (qrLoading) qrLoading.style.display = 'flex';
  try {
    const dataUri = await window.api.generateQRCodePNG(proxyURI);
    if (qrImg) {
      qrImg.src = dataUri;
      qrImg.alt = `QR Code: ${proxyURI}`;
    }
  } catch (err) {
    console.error('Failed to generate QR code:', err);
  } finally {
    if (qrLoading) qrLoading.style.display = 'none';
  }
}

function copyTextWithFeedback(text: string, buttonTextElId: string, originalText: string): void {
  void navigator.clipboard.writeText(text).then(() => {
    const textEl = optionalEl(buttonTextElId);
    const t = translations[getLang()];
    if (textEl) {
      textEl.textContent = t.lanCopied;
      setTimeout(() => {
        textEl.textContent = originalText;
      }, 1500);
    }
  }).catch((err) => {
    console.error('Failed to copy to clipboard:', err);
  });
}

const DNS_CHECK_DEBOUNCE_MS = 400;

export function readMirroredTheme(): ThemeSpec | null {
  try {
    const raw = localStorage.getItem(THEME_STORAGE_KEY);
    if (!raw) return null;
    return parseTheme(JSON.parse(raw));
  } catch {
    return null;
  }
}

export function getCurrentTheme(): ThemeSpec {
  return currentTheme;
}

export function getAppliedDns(): string {
  return appliedDns;
}

export function setAppliedDns(dns: string): void {
  appliedDns = dns;
}

function applyPendingTheme(spec: ThemeSpec): void {
  currentTheme = spec;
  applyTheme(spec);
  try {
    localStorage.setItem(THEME_STORAGE_KEY, JSON.stringify(spec));
  } catch (e) {
    console.debug('Failed to mirror theme to localStorage:', e);
  }
}

export function setTheme(
  spec: ThemeSpec,
  options: { persist?: boolean; syncControls?: boolean } = {},
): void {
  const { persist = true, syncControls = true } = options;

  currentTheme = spec;
  cancelAnimationFrame(themeFrame);
  themeFrame = requestAnimationFrame(() => applyPendingTheme(spec));

  if (syncControls) renderThemeControls();
  renderThemeReport();
  markActivePreset();

  if (!persist) return;
  if (themeSaveTimer) clearTimeout(themeSaveTimer);
  themeSaveTimer = setTimeout(() => {
    themeSaveTimer = null;
    Promise.resolve(onSaveSettingsFn()).catch((err) =>
      console.error('Failed to save settings on theme change:', err),
    );
  }, 500);
}

function presetPreview(spec: ThemeSpec): { background: string; accent: string } {
  const tokens = deriveTokens(spec);
  return {
    background: tokens['--bg-image'] === 'none' ? tokens['--bg-color'] : tokens['--bg-image'],
    accent: `rgb(${tokens['--accent-rgb']})`,
  };
}

export function renderThemePresets(): void {
  const host = optionalEl('themePresets');
  if (!host) return;
  const t = translations[getLang()];

  host.textContent = '';
  for (const preset of PRESETS) {
    const preview = presetPreview(preset.spec);
    const button = document.createElement('button');
    button.type = 'button';
    button.className = 'theme-swatch';
    button.dataset.preset = preset.id;
    button.setAttribute('role', 'radio');
    button.setAttribute('aria-checked', 'false');
    button.style.background = preview.background;

    const accent = document.createElement('span');
    accent.className = 'theme-swatch-accent';
    accent.style.background = preview.accent;
    button.appendChild(accent);

    const name = document.createElement('span');
    name.className = 'theme-swatch-name';
    name.textContent = translate(t, preset.labelKey as keyof Translations);
    button.appendChild(name);

    button.onclick = () => setTheme(structuredClone(preset.spec));
    host.appendChild(button);
  }

  markActivePreset();
}

function sameTheme(a: ThemeSpec, b: ThemeSpec): boolean {
  if (!a || !b || !a.bg || !b.bg) return false;
  if (!Array.isArray(a.bg.stops) || !Array.isArray(b.bg.stops)) return false;
  const hex = (v: string) => (v ? v.trim().toLowerCase() : '');
  return (
    a.scheme === b.scheme &&
    hex(a.accent) === hex(b.accent) &&
    a.bg.mode === b.bg.mode &&
    a.bg.angle === b.bg.angle &&
    a.bg.stops.length === b.bg.stops.length &&
    a.bg.stops.every((stop, i) => hex(stop) === hex(b.bg.stops[i]))
  );
}

function markActivePreset(): void {
  const host = optionalEl('themePresets');
  if (!host) return;
  for (const button of Array.from(host.querySelectorAll<HTMLButtonElement>('.theme-swatch'))) {
    const preset = PRESETS.find((p) => p.id === button.dataset.preset);
    button.setAttribute('aria-checked', preset && sameTheme(preset.spec, currentTheme) ? 'true' : 'false');
  }
}

export function renderThemeControls(): void {
  const accentInput = optionalEl<HTMLInputElement>('themeAccentInput');
  const accentHex = optionalEl<HTMLInputElement>('themeAccentHex');
  if (accentInput) accentInput.value = currentTheme.accent;
  if (accentHex && accentHex !== document.activeElement) accentHex.value = currentTheme.accent.toUpperCase();

  for (const button of Array.from(document.querySelectorAll<HTMLButtonElement>('[data-scheme]'))) {
    button.setAttribute('aria-checked', button.dataset.scheme === currentTheme.scheme ? 'true' : 'false');
  }
  for (const button of Array.from(document.querySelectorAll<HTMLButtonElement>('[data-mode]'))) {
    button.setAttribute('aria-checked', button.dataset.mode === currentTheme.bg.mode ? 'true' : 'false');
  }

  const angleField = optionalEl('themeAngleField');
  if (angleField) angleField.style.display = currentTheme.bg.mode === 'linear' ? 'flex' : 'none';
  const angle = optionalEl<HTMLInputElement>('themeAngle');
  if (angle) angle.value = String(currentTheme.bg.angle);
  setText('themeAngleValue', `${currentTheme.bg.angle}°`);

  renderThemeStops();

  const add = optionalEl<HTMLButtonElement>('themeStopAdd');
  const remove = optionalEl<HTMLButtonElement>('themeStopRemove');
  if (add) add.disabled = currentTheme.bg.stops.length >= 3 || currentTheme.bg.mode === 'solid';
  if (remove) remove.disabled = currentTheme.bg.stops.length <= 1;
}

export function renderThemeStops(): void {
  const host = optionalEl('themeStops');
  if (!host) return;
  const t = translations[getLang()];

  const shown = currentTheme.bg.mode === 'solid' ? 1 : currentTheme.bg.stops.length;

  host.textContent = '';
  for (let i = 0; i < shown; i++) {
    const input = document.createElement('input');
    input.type = 'color';
    input.className = 'color-well';
    input.value = currentTheme.bg.stops[i];
    input.setAttribute('aria-label', translate(t, 'themeStopAria').replace('{n}', String(i + 1)));
    input.oninput = () => {
      const stops = [...currentTheme.bg.stops];
      stops[i] = input.value;
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, stops } }, { syncControls: false });
    };
    host.appendChild(input);
  }
}

export function renderThemeReport(): void {
  const line = optionalEl('themeContrast');
  if (!line) return;
  const t = translations[getLang()];
  const report = describeTheme(currentTheme);
  const round = (v: number) => v.toFixed(2);

  const parts = [
    translate(t, report.passesAA ? 'themeContrastOk' : 'themeContrastLow')
      .replace('{text}', round(report.textContrast))
      .replace('{accent}', round(report.accentContrast))
      .replace('{signal}', round(report.signalContrast)),
  ];
  if (report.backgroundAdjusted) {
    parts.push(translate(t, report.scheme === 'dark' ? 'themeAdjustedBgDark' : 'themeAdjustedBgLight'));
  }
  if (report.accentAdjusted) {
    parts.push(translate(t, 'themeAdjustedAccent').replace('{hex}', report.accentApplied.toUpperCase()));
  }

  line.textContent = parts.join(' ');
  line.classList.toggle(
    'theme-contrast--adjusted',
    !report.passesAA || report.backgroundAdjusted || report.accentAdjusted,
  );
}

// ── DNS Настройки ────────────────────────────────────────────────────────────

export function currentDnsSetting(): string {
  const select = optionalEl<HTMLSelectElement>('dnsSelect');
  if (!select) return '';
  if (select.value !== 'custom') return select.value;
  return optionalEl<HTMLInputElement>('customDnsInput')?.value.trim() ?? '';
}

export function showCustomDnsField(custom: boolean): void {
  const input = optionalEl<HTMLInputElement>('customDnsInput');
  const hint = optionalEl('customDnsHint');
  if (input) input.style.display = custom ? 'block' : 'none';
  if (hint) hint.style.display = custom ? 'block' : 'none';
}

export function setDnsSetting(value: string): void {
  const select = optionalEl<HTMLSelectElement>('dnsSelect');
  const input = optionalEl<HTMLInputElement>('customDnsInput');
  if (!select) return;

  if (dnsCheckTimer !== null) {
    clearTimeout(dnsCheckTimer);
    dnsCheckTimer = null;
  }

  const standard = Array.from(select.options).some((o) => o.value === value && o.value !== 'custom');
  if (standard) {
    select.value = value;
    showCustomDnsField(false);
  } else {
    select.value = 'custom';
    showCustomDnsField(true);
    if (input) input.value = value;
  }
  syncCustomSelectLabels('dnsSelect');
}

export async function checkCustomDns(): Promise<void> {
  const input = optionalEl<HTMLInputElement>('customDnsInput');
  const hint = optionalEl('customDnsHint');
  if (!input || !hint) return;

  const t = translations[getLang()];
  const value = input.value.trim();

  if (value === '') {
    hint.textContent = t.dnsCustomHint;
    hint.classList.remove('field-hint--invalid');
    return;
  }

  let problem = '';
  try {
    problem = await window.api.validateDns(value);
  } catch (e) {
    console.error('validateDns failed:', e);
    hint.textContent = errorText(e) || 'DNS validation error';
    hint.classList.add('field-hint--invalid');
    return;
  }

  hint.textContent = problem || t.dnsCustomHint;
  hint.classList.toggle('field-hint--invalid', problem !== '');
  if (!problem && value) {
    const select = optionalEl<HTMLSelectElement>('dnsSelect');
    if (select && select.value === 'custom') {
      try {
        await onSaveSettingsFn();
      } catch (err) {
        console.error('onSaveSettings failed after validateDns:', err);
      }
      if (currentDnsSetting() !== appliedDns) onMarkRulesPendingFn();
    }
  }
}

export function applySettingsLanguage(t: Translations): void {
  setText('appSettingsTitle', t.appSettingsTitle);
  setText('dnsServerLabel', t.dnsServerLabel);
  const customOpt = query<HTMLOptionElement>('#dnsSelect option[value="custom"]');
  if (customOpt) customOpt.textContent = t.placeholderDns;
  setText('tunModeLabel', t.tunModeLabel);
  setText('tunModeDesc', t.tunModeDesc);
  setText('systemProxyLabel', t.systemProxyLabel);
  setText('allowLanLabel', t.allowLanLabel);
  setText('allowLanDesc', t.allowLanDesc);
  setText('openLanSharingBtnText', t.openLanSharingBtn);
  setText('lanSharingTitle', t.lanSharingTitle);
  setText('lanSharingSubtitle', t.lanSharingSubtitle);
  setText('lanSelectInterfaceLabel', t.lanSelectInterface);
  setText('lanProtocolLabel', t.lanProtocolLabel);
  setText('lanAddressLabel', t.lanAddressLabel);
  setText('lanCopyAddressBtnText', t.lanCopyAddress);
  setText('lanCopyLinkBtnText', t.lanCopyLink);
  setText('lanManualGuideTitle', t.lanManualGuideTitle);
  setText('lanManualGuideDesc', t.lanManualGuideDesc);
  setText('lanSharingCloseBtn', t.closeBtnTitle);

  setText('appearanceTitle', t.appearanceTitle);
  setText('appearanceDesc', t.appearanceDesc);
  setText('themeSchemeLabel', t.themeSchemeLabel);
  setText('themeSchemeDark', t.themeSchemeDark);
  setText('themeSchemeLight', t.themeSchemeLight);
  setText('themeAccentLabel', t.themeAccentLabel);
  const themeAccentHex = optionalEl('themeAccentHex');
  if (themeAccentHex) themeAccentHex.setAttribute('aria-label', t.themeAccentHexAria);
  setText('themeBgLabel', t.themeBgLabel);
  setText('themeStopsLabel', t.themeStopsLabel);
  setText('themeAngleLabel', t.themeAngleLabel);
  setText('themeModeSolid', t.themeModeSolid);
  setText('themeModeLinear', t.themeModeLinear);
  setText('themeModeRadial', t.themeModeRadial);
  setText('themeStopAdd', t.themeStopAdd);
  setText('themeStopRemove', t.themeStopRemove);
  setText('themeResetBtnText', t.themeReset);

  setText('transferTitle', t.transferTitle);
  setText('transferDesc', t.transferDesc);
  setText('exportSettingsBtnText', t.exportSettingsBtn);
  setText('importSettingsBtnText', t.importSettingsBtn);

  setText('networkSectionTitle', t.networkSectionTitle);
  setText('networkSectionDesc', t.networkSectionDesc);
  setText('systemIntegrationTitle', t.systemIntegrationTitle);
  setText('systemIntegrationDesc', t.systemIntegrationDesc);
  setText('autoConnectLabel', t.autoConnectLabel);
  setText('autoUpdateSubsLabel', t.autoUpdateSubsLabel);
  setText('rememberServerLabel', t.rememberServerLabel);
  setText('openAtLoginLabel', t.openAtLoginLabel);
  setText('startMinimizedLabel', t.startMinimizedLabel);
  setText('globalHotkeysLabel', t.globalHotkeysLabel);
  setText('globalHotkeysDesc', t.globalHotkeysDesc);
  setText('hotkeyToggleLabel', t.hotkeyToggleLabel);
  setText('hotkeyShowLabel', t.hotkeyShowLabel);
  setText('hotkeyHint', t.hotkeyHint);

  setText('securityTitle', t.securityTitle);
  setText('securityDesc', t.securityDesc);
  setText('killSwitchLabel', t.killSwitchLabel);
  setText('killSwitchDesc', t.killSwitchDesc);
  setText('killSwitchFailsafeNotice', t.killSwitchFailsafeNotice);
  setText('transferSafeNotice', t.transferSafeNotice);
  setText('dnsLeakLabel', t.dnsLeakLabel);
  setText('ipv6LeakLabel', t.ipv6LeakLabel);
  setText('fakeDnsLabel', t.fakeDnsLabel);
  setText('fakeDnsDesc', t.fakeDnsDesc);
  setText('verboseLoggingLabel', t.verboseLoggingLabel);
  setText('verboseLoggingDesc', t.verboseLoggingDesc);
  setText('tlsFragmentLabel', t.tlsFragmentLabel);
  setText('tlsFragmentDesc', t.tlsFragmentDesc);

  const autoSaveText = optionalEl('settingsAutoSaveText');
  if (autoSaveText) autoSaveText.textContent = t.statusSaved;

  const customDnsInput = optionalEl<HTMLInputElement>('customDnsInput');
  if (customDnsInput) {
    customDnsInput.placeholder = t.dnsCustomPlaceholder;
    customDnsInput.setAttribute('aria-label', t.customDnsLabel);
  }
  setText('customDnsHint', t.dnsCustomHint);

  renderThemePresets();
  renderThemeControls();
  renderThemeReport();
}

export function initSettingsView(options: SettingsViewOptions): void {
  getLang = options.getCurrentLanguage;
  isCoreActiveFn = options.isCoreActive;
  onSaveSettingsFn = options.onSaveSettings;
  onMarkRulesPendingFn = options.onMarkRulesPending;
  if (options.onRefreshKillSwitch) onRefreshKillSwitchFn = options.onRefreshKillSwitch;
  if (options.onUpdateInterceptionGuidance) onUpdateInterceptionGuidanceFn = options.onUpdateInterceptionGuidance;
  showAlertFn = options.showAlert;
  showConfirmFn = options.showConfirm;

  if (isInitialized) return;
  isInitialized = true;

  // Theme Controls
  const accentInput = optionalEl<HTMLInputElement>('themeAccentInput');
  if (accentInput) {
    accentInput.oninput = () => {
      setTheme({ ...currentTheme, accent: accentInput.value }, { syncControls: false });
      const hex = optionalEl<HTMLInputElement>('themeAccentHex');
      if (hex) hex.value = accentInput.value.toUpperCase();
    };
  }

  const accentHex = optionalEl<HTMLInputElement>('themeAccentHex');
  if (accentHex) {
    accentHex.oninput = () => {
      const value = accentHex.value.trim();
      if (!/^#?([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(value)) return;
      setTheme({ ...currentTheme, accent: value.startsWith('#') ? value : `#${value}` }, { syncControls: false });
      const well = optionalEl<HTMLInputElement>('themeAccentInput');
      if (well) well.value = currentTheme.accent;
    };
    accentHex.onblur = () => renderThemeControls();
  }

  document.querySelectorAll<HTMLButtonElement>('[data-scheme]').forEach((button) => {
    button.onclick = () => {
      setTheme({ ...currentTheme, scheme: button.dataset.scheme as Scheme });
    };
  });

  document.querySelectorAll<HTMLButtonElement>('[data-mode]').forEach((button) => {
    button.onclick = () => {
      const mode = button.dataset.mode as BackgroundMode;
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, mode } });
    };
  });

  const angle = optionalEl<HTMLInputElement>('themeAngle');
  if (angle) {
    angle.oninput = () => {
      setTheme(
        { ...currentTheme, bg: { ...currentTheme.bg, angle: Number(angle.value) } },
        { syncControls: false },
      );
      setText('themeAngleValue', `${angle.value}°`);
    };
  }

  const add = optionalEl<HTMLButtonElement>('themeStopAdd');
  if (add) {
    add.onclick = () => {
      const stops = [...currentTheme.bg.stops];
      if (stops.length >= 3) return;
      stops.push(stops[stops.length - 1]);
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, stops } });
    };
  }

  const remove = optionalEl<HTMLButtonElement>('themeStopRemove');
  if (remove) {
    remove.onclick = () => {
      const stops = currentTheme.bg.stops.slice(0, -1);
      if (stops.length < 1) return;
      setTheme({ ...currentTheme, bg: { ...currentTheme.bg, stops } });
    };
  }

  const reset = optionalEl<HTMLButtonElement>('themeResetBtn');
  if (reset) reset.onclick = () => setTheme(structuredClone(DEFAULT_THEME));

  // DNS Controls
  const dnsSelect = optionalEl<HTMLSelectElement>('dnsSelect');
  if (dnsSelect) {
    dnsSelect.onchange = (e) => {
      const custom = (e.target as HTMLSelectElement).value === 'custom';
      if (dnsCheckTimer !== null) {
        clearTimeout(dnsCheckTimer);
        dnsCheckTimer = null;
      }
      showCustomDnsField(custom);
      if (custom) {
        void checkCustomDns();
      } else {
        Promise.resolve(onSaveSettingsFn()).catch((err) =>
          console.error('Failed to save settings on DNS select change:', err),
        );
        if (currentDnsSetting() !== appliedDns) onMarkRulesPendingFn();
      }
    };
  }

  const customDnsInput = optionalEl<HTMLInputElement>('customDnsInput');
  if (customDnsInput) {
    customDnsInput.addEventListener('input', () => {
      if (dnsCheckTimer !== null) clearTimeout(dnsCheckTimer);
      dnsCheckTimer = setTimeout(() => {
        dnsCheckTimer = null;
        void checkCustomDns();
      }, DNS_CHECK_DEBOUNCE_MS);
    });
  }

  // TUN mode checkbox with elevation request
  const tunBox = optionalEl<HTMLInputElement>('tunModeCheckbox');
  if (tunBox) {
    tunBox.onchange = async (e) => {
      onUpdateInterceptionGuidanceFn();
      try {
        if ((e.target as HTMLInputElement).checked) {
          const isAdmin = await window.api.checkAdmin();
          if (!isAdmin) {
            await onSaveSettingsFn();
            window.api.requestAdmin();
          } else {
            await onSaveSettingsFn();
          }
        } else {
          await onSaveSettingsFn();
        }
      } catch (err) {
        console.error('Failed to update TUN mode:', err);
      }
    };
  }

  // System Proxy checkbox
  const sysProxyBox = optionalEl<HTMLInputElement>('systemProxyCheckbox');
  if (sysProxyBox) {
    sysProxyBox.onchange = () => {
      onUpdateInterceptionGuidanceFn();
      Promise.resolve(onSaveSettingsFn()).catch((err) =>
        console.error('Failed to save settings on systemProxy change:', err),
      );
    };
  }

  // Allow LAN checkbox & sharing button
  const allowLanBox = optionalEl<HTMLInputElement>('allowLanCheckbox');
  if (allowLanBox) {
    allowLanBox.onchange = () => {
      const openBtn = optionalEl('openLanSharingBtn');
      if (openBtn) openBtn.style.display = allowLanBox.checked ? 'inline-flex' : 'none';
      Promise.resolve(onSaveSettingsFn()).catch((err) =>
        console.error('Failed to save settings on allowLan change:', err),
      );
      if (isCoreActiveFn()) {
        onMarkRulesPendingFn();
      }
    };
  }

  const openLanBtn = optionalEl('openLanSharingBtn');
  if (openLanBtn) {
    openLanBtn.onclick = () => {
      void openLanSharingModal();
    };
  }

  const lanCloseBtn = optionalEl('lanSharingCloseBtn');
  if (lanCloseBtn) {
    lanCloseBtn.onclick = () => {
      closeLanSharingModal();
    };
  }

  const lanOverlay = optionalEl('lanSharingModalOverlay');
  if (lanOverlay) {
    lanOverlay.addEventListener('click', (e) => {
      if (e.target === lanOverlay) {
        closeLanSharingModal();
      }
    });
  }

  const lanInterfaceSel = optionalEl<HTMLSelectElement>('lanInterfaceSelect');
  if (lanInterfaceSel) {
    lanInterfaceSel.onchange = () => {
      currentLanIP = lanInterfaceSel.value;
      void updateLanSharingDisplay();
    };
  }

  const lanProtoSel = optionalEl<HTMLSelectElement>('lanProtocolSelect');
  if (lanProtoSel) {
    lanProtoSel.onchange = () => {
      currentLanProtocol = lanProtoSel.value as 'socks5' | 'http';
      void updateLanSharingDisplay();
    };
  }

  const copyAddrBtn = optionalEl('lanCopyAddressBtn');
  if (copyAddrBtn) {
    copyAddrBtn.onclick = () => {
      const addr = `${currentLanIP}:${LAN_PROXY_PORT}`;
      const t = translations[getLang()];
      copyTextWithFeedback(addr, 'lanCopyAddressBtnText', t.lanCopyAddress);
    };
  }

  const copyLinkBtn = optionalEl('lanCopyLinkBtn');
  if (copyLinkBtn) {
    copyLinkBtn.onclick = () => {
      const link = `${currentLanProtocol}://${currentLanIP}:${LAN_PROXY_PORT}#NeoBox-LAN`;
      const t = translations[getLang()];
      copyTextWithFeedback(link, 'lanCopyLinkBtnText', t.lanCopyLink);
    };
  }

  // Bypass RU checkbox
  const bypassRuBox = optionalEl<HTMLInputElement>('bypassRuCheckbox');
  if (bypassRuBox) {
    bypassRuBox.onchange = () => {
      Promise.resolve(onSaveSettingsFn()).catch((err) =>
        console.error('Failed to save settings on bypassRu change:', err),
      );
      onMarkRulesPendingFn();
    };
  }

  // General auto-save checkboxes
  const autoSaveGeneralCheckboxes = [
    'autoConnectCheckbox',
    'autoUpdateSubsCheckbox',
    'rememberServerCheckbox',
    'openAtLoginCheckbox',
    'startMinimizedCheckbox',
  ];
  autoSaveGeneralCheckboxes.forEach((id) => {
    const checkbox = optionalEl<HTMLInputElement>(id);
    if (checkbox) {
      checkbox.onchange = () => {
        Promise.resolve(onSaveSettingsFn()).catch((err) =>
          console.error(`Failed to save settings on ${id} change:`, err),
        );
      };
    }
  });

  // Security auto-save checkboxes
  const autoSaveSecurityCheckboxes = [
    'killSwitchCheckbox',
    'dnsLeakCheckbox',
    'ipv6LeakCheckbox',
    'fakeDnsCheckbox',
    'verboseLoggingCheckbox',
    'tlsFragmentCheckbox',
  ];
  autoSaveSecurityCheckboxes.forEach((id) => {
    const checkbox = optionalEl<HTMLInputElement>(id);
    if (checkbox) {
      checkbox.onchange = () => {
        Promise.resolve(onSaveSettingsFn()).catch((err) =>
          console.error(`Failed to save settings on ${id} change:`, err),
        );
        if (id === 'killSwitchCheckbox') {
          onRefreshKillSwitchFn();
        }
        if (isCoreActiveFn()) onMarkRulesPendingFn();
      };
    }
  });

  // Export Settings
  const exportBtn = optionalEl<HTMLButtonElement>('exportSettingsBtn');
  if (exportBtn) {
    exportBtn.onclick = async () => {
      const t = translations[getLang()];
      const btnText = optionalEl('exportSettingsBtnText');
      const prevText = btnText ? btnText.textContent : '';
      try {
        exportBtn.disabled = true;
        if (btnText) btnText.textContent = t.statusConnecting;
        await onSaveSettingsFn();
        const path = await window.api.exportSettings();
        if (!path) return;
        showAlertFn(t.alertDialogTitle, t.exportSettingsDone.replace('{path}', path), false, t);
      } catch (e) {
        console.error('exportSettings failed:', e);
        showAlertFn(t.errorDialogTitle, `${t.exportSettingsFailed}: ${errorText(e)}`, true, t);
      } finally {
        exportBtn.disabled = false;
        if (btnText && prevText) btnText.textContent = prevText;
      }
    };
  }

  // Import Settings
  const importBtn = optionalEl<HTMLButtonElement>('importSettingsBtn');
  if (importBtn) {
    importBtn.onclick = async () => {
      const t = translations[getLang()];
      try {
        if (!(await showConfirmFn(t.importSettingsConfirm))) return;
      } catch (e) {
        console.error('showConfirm failed:', e);
        return;
      }

      const btnText = optionalEl('importSettingsBtnText');
      const prevText = btnText ? btnText.textContent : '';
      let shouldReload = false;
      try {
        importBtn.disabled = true;
        if (btnText) btnText.textContent = t.statusConnecting;
        const path = await window.api.importSettings();
        if (!path) return;
        await showAlertFn(t.alertDialogTitle, t.importSettingsDone, false, t);
        shouldReload = true;
        window.location.reload();
      } catch (e) {
        console.error('importSettings failed:', e);
        showAlertFn(t.errorDialogTitle, `${t.importSettingsFailed}: ${errorText(e)}`, true, t);
      } finally {
        if (!shouldReload) {
          importBtn.disabled = false;
          if (btnText && prevText) btnText.textContent = prevText;
        }
      }
    };
  }
}
