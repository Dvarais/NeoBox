import { optionalEl, setText } from '../modules/dom';
import { iconSvg } from '../modules/icons';
import { errorText, trapFocus } from '../modules/ui-utils';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';

export interface DnsLeakViewOptions {
  getCurrentLanguage: () => Language;
  getCurrentDnsSetting: () => string;
  getAppliedDns: () => string;
}

interface IpifyResponse {
  ip?: string;
}

interface EdnsResponse {
  dns?: {
    ip?: string;
    geo?: string;
  };
}

let getLang: () => Language = () => 'RU';
let getCurrentDnsSettingFn: () => string = () => '';
let getAppliedDnsFn: () => string = () => '';

let dnsLeakRelease: (() => void) | null = null;
let activeAbortController: AbortController | null = null;
let isTestRunning = false;
let isInitialized = false;

export function closeDnsLeakModal(): void {
  const overlay = optionalEl('dnsLeakModalOverlay');
  if (overlay) overlay.style.display = 'none';
  if (activeAbortController) {
    activeAbortController.abort();
    activeAbortController = null;
  }
  isTestRunning = false;
  dnsLeakRelease?.();
  dnsLeakRelease = null;
}

export async function runDnsLeakTest(): Promise<void> {
  if (isTestRunning) return;

  const currentLang = getLang();
  const t = translations[currentLang];
  const overlay = optionalEl('dnsLeakModalOverlay');
  const loading = optionalEl('dnsLeakLoading');
  const result = optionalEl('dnsLeakResult');
  const iconWrap = optionalEl('dnsLeakIconWrap');
  const banner = optionalEl('dnsLeakStatusBanner');
  const statusTxt = optionalEl('dnsLeakStatusText');
  const statusIcon = optionalEl('dnsLeakStatusIcon');
  const ipEl = optionalEl('dnsLeakIp');
  const dnsList = optionalEl('dnsLeakDnsList');
  const retryBtn = optionalEl('dnsLeakRetryBtn');
  const closeBtn = optionalEl('dnsLeakCloseBtn');

  if (!overlay || !loading || !result || !iconWrap || !banner || !statusTxt || !statusIcon || !ipEl || !dnsList || !retryBtn) {
    return;
  }

  // Показываем модалку, сбрасываем состояние
  overlay.style.display = 'flex';
  if (!dnsLeakRelease && closeBtn) {
    dnsLeakRelease = trapFocus(overlay, closeBtn, closeDnsLeakModal);
  }
  loading.style.display = 'flex';
  result.style.display = 'none';
  retryBtn.style.display = 'none';
  iconWrap.className = 'dns-leak-icon-wrap';
  banner.className = 'dns-leak-status-banner';

  isTestRunning = true;
  if (activeAbortController) {
    activeAbortController.abort();
  }
  const controller = new AbortController();
  activeAbortController = controller;
  const timeoutId = setTimeout(() => controller.abort(), 10000);

  try {
    // 1. Получаем внешний IP через ipify
    const ipRes = await fetch('https://api.ipify.org?format=json', {
      signal: controller.signal,
      cache: 'no-store',
    }).then((r) => {
      if (!r.ok) throw new Error(`ipify error: HTTP ${r.status}`);
      return r.json() as Promise<IpifyResponse>;
    });
    const myIp = ipRes?.ip || '?';

    // 2. Спрашиваем, какой резолвер на самом деле обслужил запрос.
    const edns = await fetch('https://edns.ip-api.com/json', {
      signal: controller.signal,
      cache: 'no-store',
    }).then((r) => {
      if (!r.ok) throw new Error(`ip-api error: HTTP ${r.status}`);
      return r.json() as Promise<EdnsResponse>;
    });
    const resolverIp: string = edns?.dns?.ip ?? '';
    const resolverGeo: string = edns?.dns?.geo ?? '';

    // Владелец сети резолвера для каждого встроенного варианта из dnsSelect.
    const resolverOwners = await window.api.dnsResolverOwners();

    // Сверять надо с тем DNS, который ядро использует сейчас, а не с тем, что
    // выбран в списке: настройка вступает в силу только при перезапуске ядра.
    const appliedDns = getAppliedDnsFn();
    const currentDns = getCurrentDnsSettingFn();
    const expectedOwner = resolverOwners[appliedDns || currentDns];
    const seen = `${resolverGeo} ${resolverIp}`.toLowerCase();

    // Если модалку закрыли во время запроса — не рисуем результат поверх закрытого окна
    if (controller.signal.aborted || overlay.style.display === 'none') {
      return;
    }

    // Отображаем результат
    loading.style.display = 'none';
    result.style.display = 'block';
    retryBtn.style.display = 'flex';
    ipEl.textContent = myIp;

    // textContent, а не innerHTML: строка приходит из ответа стороннего сервиса.
    dnsList.innerHTML = '';
    const div = document.createElement('div');
    div.className = 'dns-leak-dns-entry';
    if (resolverIp) {
      div.textContent = resolverGeo ? `${resolverIp} (${resolverGeo})` : resolverIp;
    } else {
      div.style.color = 'var(--text-dim)';
      div.textContent = t.dnsLeakUnknown;
    }
    dnsList.appendChild(div);

    if (!resolverIp) {
      iconWrap.className = 'dns-leak-icon-wrap';
      banner.className = 'dns-leak-status-banner';
      statusIcon.innerHTML = iconSvg('shield', 18);
      statusTxt.textContent = t.dnsLeakUnknown;
    } else if (!expectedOwner) {
      iconWrap.className = 'dns-leak-icon-wrap';
      banner.className = 'dns-leak-status-banner';
      statusIcon.innerHTML = iconSvg('shield', 18);
      statusTxt.textContent = t.dnsLeakUnverified;
    } else if (seen.includes(expectedOwner)) {
      iconWrap.className = 'dns-leak-icon-wrap safe';
      banner.className = 'dns-leak-status-banner';
      statusIcon.innerHTML = iconSvg('checkCircle', 18);
      statusTxt.textContent = t.dnsLeakSafe;
    } else {
      iconWrap.className = 'dns-leak-icon-wrap leak';
      banner.className = 'dns-leak-status-banner leak';
      statusIcon.innerHTML = iconSvg('alertTriangle', 18);
      statusTxt.textContent = t.dnsLeakDetected;
    }
  } catch (err) {
    if (controller.signal.aborted || overlay.style.display === 'none') {
      return;
    }
    console.error('DNS leak test failed:', err);
    loading.style.display = 'none';
    result.style.display = 'block';
    retryBtn.style.display = 'flex';
    iconWrap.className = 'dns-leak-icon-wrap leak';
    banner.className = 'dns-leak-status-banner leak';
    statusIcon.innerHTML = iconSvg('xCircle', 18);
    statusTxt.textContent = t.dnsLeakTestError;
    ipEl.textContent = '—';
    dnsList.innerHTML = '';
    const errDiv = document.createElement('div');
    errDiv.className = 'dns-leak-dns-entry';
    errDiv.style.color = 'var(--danger)';
    errDiv.textContent = errorText(err);
    dnsList.appendChild(errDiv);
  } finally {
    clearTimeout(timeoutId);
    if (activeAbortController === controller) {
      activeAbortController = null;
    }
    isTestRunning = false;
  }
}

export function applyDnsLeakLanguage(t: Translations): void {
  setText('dnsLeakBtnText', t.dnsLeakBtn);
  setText('dnsLeakModalTitle', t.dnsLeakModalTitle);
  setText('dnsLeakModalSubtitle', t.dnsLeakModalSubtitle);
  setText('dnsLeakLoadingText', t.dnsLeakLoadingText);
  setText('dnsLeakIpLabel', t.dnsLeakIpLabel);
  setText('dnsLeakDnsLabel', t.dnsLeakDnsLabel);
  setText('dnsLeakRetryBtnText', t.dnsLeakRetryBtn);
}

export function initDnsLeakView(options: DnsLeakViewOptions): void {
  getLang = options.getCurrentLanguage;
  getCurrentDnsSettingFn = options.getCurrentDnsSetting;
  getAppliedDnsFn = options.getAppliedDns;

  if (isInitialized) return;
  isInitialized = true;

  const overlay = optionalEl('dnsLeakModalOverlay');
  if (overlay) {
    overlay.onclick = (e) => {
      if (e.target === overlay) {
        closeDnsLeakModal();
      }
    };
  }

  const leakBtn = optionalEl('dnsLeakBtn');
  if (leakBtn) leakBtn.onclick = () => void runDnsLeakTest();

  const closeBtn = optionalEl('dnsLeakCloseBtn');
  if (closeBtn) closeBtn.onclick = closeDnsLeakModal;

  const retryBtn = optionalEl('dnsLeakRetryBtn');
  if (retryBtn) retryBtn.onclick = () => void runDnsLeakTest();
}
