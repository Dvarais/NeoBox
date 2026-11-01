import { optionalEl, setFieldLabel, setText, setTitle } from '../modules/dom';
import { errorText, trapFocus } from '../modules/ui-utils';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import {
  allSubscriptions,
  currentActiveSubId,
  loadSubscriptions as loadSubsBase,
  renderSubStatus,
  renderSubTabs,
} from '../modules/subscription-manager';

export interface SubscriptionsViewOptions {
  getCurrentLanguage: () => Language;
  onUpdateCards: () => void;
  onSubscriptionsLoaded?: () => void;
  showAlert: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void;
}

let getLang: () => Language = () => 'RU';
let onUpdateCardsFn: () => void = () => {};
let onSubscriptionsLoadedFn: (() => void) | undefined;
let showAlertFn: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void = () => {};

let isInitialized = false;
let qrStream: MediaStream | null = null;
let qrAnimationId: number | null = null;
let qrModalRelease: (() => void) | null = null;

export function setSubManagePanelCollapsed(collapsed: boolean): void {
  const panel = optionalEl('subManagePanel');
  const header = optionalEl('subManageHeader');
  const toggleBtn = optionalEl<HTMLButtonElement>('toggleSubPanelBtn');
  if (!panel || !header) return;

  panel.classList.toggle('is-collapsed', collapsed);
  header.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
  if (toggleBtn) {
    toggleBtn.setAttribute('aria-expanded', collapsed ? 'false' : 'true');
  }
}

export function syncSubManagePanelBadge(): void {
  const badge = optionalEl('subManageBadge');
  if (badge) {
    const subCount = allSubscriptions.length;
    badge.textContent =
      subCount > 0 ? (getLang() === 'RU' ? `${subCount} подп.` : `${subCount} subs`) : '';
  }
  const stored = localStorage.getItem('neobox_sub_panel_collapsed');
  if (stored === null && allSubscriptions.length === 0) {
    setSubManagePanelCollapsed(false);
  }
}

export function initSubManagePanel(): void {
  const panel = optionalEl('subManagePanel');
  const header = optionalEl('subManageHeader');
  const toggleBtn = optionalEl<HTMLButtonElement>('toggleSubPanelBtn');
  if (!panel || !header) return;

  const toggle = () => {
    const isCollapsed = panel.classList.contains('is-collapsed');
    const next = !isCollapsed;
    setSubManagePanelCollapsed(next);
    localStorage.setItem('neobox_sub_panel_collapsed', next ? '1' : '0');
  };

  header.addEventListener('click', (e) => {
    if ((e.target as HTMLElement).closest('input, button, a')) return;
    toggle();
  });

  header.addEventListener('keydown', (e: KeyboardEvent) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      toggle();
    }
  });

  if (toggleBtn) {
    toggleBtn.addEventListener('click', (e) => {
      e.stopPropagation();
      toggle();
    });
  }

  const stored = localStorage.getItem('neobox_sub_panel_collapsed');
  if (stored !== null) {
    setSubManagePanelCollapsed(stored === '1');
  } else {
    setSubManagePanelCollapsed(allSubscriptions.length > 0);
  }
}

export async function loadSubscriptions(): Promise<void> {
  const currentLang = getLang();
  const subTabsContainer =
    optionalEl('subscription-tabs') ||
    optionalEl('subTabsContainer') ||
    document.querySelector<HTMLElement>('.subscription-tabs');
  const subStatusLine = optionalEl('subStatusLine');

  await loadSubsBase(() => {
    renderSubTabs(
      subTabsContainer,
      translations,
      currentLang,
      () => {
        onUpdateCardsFn();
        renderSubStatus(subStatusLine, translations, currentLang);
      },
      () => void loadSubscriptions(),
    );
    onUpdateCardsFn();
    renderSubStatus(subStatusLine, translations, currentLang);
    syncSubManagePanelBadge();
  });

  onSubscriptionsLoadedFn?.();
}

export async function addSubscriptionByUrl(name: string, url: string): Promise<void> {
  const newSubId = Date.now().toString();
  const newSub = { id: newSubId, name, url, links: [], loading: true };
  allSubscriptions.push(newSub);

  await window.api.saveSubscriptions(allSubscriptions);
  await loadSubscriptions(); // Renders the tab instantly with hourglass spinner

  // Background fetch
  (async () => {
    try {
      const res = await window.api.fetchSubscriptionWithInfo(url);
      const sub = allSubscriptions.find((s) => s.id === newSubId);
      if (sub) {
        sub.links = res.links || [];
        if (res.userInfo) {
          sub.userInfo = res.userInfo;
        }
        sub.loading = false;
        sub.updatedAt = Date.now();
        sub.lastError = undefined;
        await window.api.saveSubscriptions(allSubscriptions);
        await loadSubscriptions(); // Refresh tabs to remove hourglass spinner
        if (currentActiveSubId === newSubId || currentActiveSubId === 'all') {
          onUpdateCardsFn();
        }
      }
    } catch (e) {
      console.error('Failed to fetch subscription:', e);
      const sub = allSubscriptions.find((s) => s.id === newSubId);
      if (sub) {
        sub.loading = false;
        await window.api.saveSubscriptions(allSubscriptions);
        await loadSubscriptions();
      }
      const t = translations[getLang()];
      void showAlertFn(t.errorDialogTitle, errorText(e), true, t);
    }
  })().catch((err) => console.error('Background subscription fetch unhandled failure:', err));
}

function stopQrCamera(): void {
  if (qrAnimationId) {
    cancelAnimationFrame(qrAnimationId);
    qrAnimationId = null;
  }
  if (qrStream) {
    qrStream.getTracks().forEach((track) => track.stop());
    qrStream = null;
  }

  const qrVideo = optionalEl<HTMLVideoElement>('qrVideo');
  const qrReticle = optionalEl('qrScannerReticle');
  const qrPlaceholder = optionalEl('qrScannerPlaceholder');

  if (qrVideo) {
    qrVideo.pause();
    qrVideo.srcObject = null;
    qrVideo.style.display = 'none';
  }
  if (qrReticle) qrReticle.style.display = 'none';
  if (qrPlaceholder) qrPlaceholder.style.display = 'flex';
}

export function closeQrModal(): void {
  stopQrCamera();
  const qrModalOverlay = optionalEl('qrModalOverlay');
  if (qrModalOverlay) qrModalOverlay.style.display = 'none';
  qrModalRelease?.();
  qrModalRelease = null;
}

function scanQrFrame(): void {
  const qrVideo = optionalEl<HTMLVideoElement>('qrVideo');
  const qrCanvas = optionalEl<HTMLCanvasElement>('qrCanvas');
  if (!qrVideo || !qrCanvas) return;

  if (qrVideo.readyState === qrVideo.HAVE_ENOUGH_DATA) {
    const canvasCtx = qrCanvas.getContext('2d');
    if (canvasCtx) {
      qrCanvas.width = qrVideo.videoWidth;
      qrCanvas.height = qrVideo.videoHeight;
      canvasCtx.drawImage(qrVideo, 0, 0, qrCanvas.width, qrCanvas.height);

      const imageData = canvasCtx.getImageData(0, 0, qrCanvas.width, qrCanvas.height);
      if (typeof jsQR !== 'undefined') {
        const code = jsQR(imageData.data, imageData.width, imageData.height);
        if (code && code.data) {
          void handleQrImport(code.data);
          return;
        }
      }
    }
  }

  if (qrStream) {
    qrAnimationId = requestAnimationFrame(scanQrFrame);
  }
}

async function handleQrImport(link: string): Promise<void> {
  const t = translations[getLang()];
  const trimmed = link.trim();

  // Protocols matched with backend proxySchemes
  const qrSchemes = [
    'vless',
    'vmess',
    'ss',
    'trojan',
    'tuic',
    'hysteria',
    'hysteria2',
    'hy2',
    'anytls',
    'socks',
    'socks5',
    'wireguard',
    'wg',
  ];

  if (qrSchemes.some((scheme) => trimmed.toLowerCase().startsWith(`${scheme}://`))) {
    let qrSub = allSubscriptions.find((s) => s.url === 'qrcode');
    if (qrSub) {
      const existing = new Set(qrSub.links);
      existing.add(trimmed);
      qrSub.links = Array.from(existing);
    } else {
      const name = t.qrSubName;
      qrSub = {
        id: Date.now().toString(),
        name,
        url: 'qrcode',
        links: [trimmed],
      };
      allSubscriptions.push(qrSub);
    }

    await window.api.saveSubscriptions(allSubscriptions);
    closeQrModal();
    await loadSubscriptions();
    void showAlertFn(t.alertDialogTitle, t.qrSuccessImport, false, t);
    return;
  }

  if (/^https?:\/\//i.test(trimmed)) {
    closeQrModal();
    let name = trimmed;
    try {
      name = new URL(trimmed).hostname || trimmed;
    } catch {
      // Fallback to raw text
    }
    await addSubscriptionByUrl(name, trimmed);
    return;
  }

  void showAlertFn(
    t.errorDialogTitle,
    t.qrUnsupportedContent.replace('{content}', trimmed.slice(0, 120)),
    true,
    t,
  );
}

export function applySubscriptionsLanguage(t: Translations): void {
  setText('addSubBtn', t.addBtn);
  setText('updateSubBtn', t.updateCurrentBtn);
  setTitle('updateSubBtn', t.updateCurrentBtnTitle);
  setText('importClipboardBtn', t.importClipboardBtn);

  setText('subManagementTitle', t.subManagement);
  setFieldLabel('subName', t.subNamePlaceholder);
  setFieldLabel('subUrl', t.subUrlPlaceholder);

  setText('importQrBtn', t.importQrBtn);
  setText('qrModalTitle', t.qrModalTitle);
  setText('qrStartCameraBtnText', t.qrStartCameraBtn);
  setText('qrUploadFileBtnText', t.qrUploadFileBtn);
  setText('qrPlaceholderText', t.qrPlaceholderText);
  setText('qrModalClose', t.errorDialogClose);

  const currentLang = getLang();
  const subTabsContainer =
    optionalEl('subscription-tabs') ||
    optionalEl('subTabsContainer') ||
    document.querySelector<HTMLElement>('.subscription-tabs');
  renderSubTabs(
    subTabsContainer,
    translations,
    currentLang,
    () => {
      onUpdateCardsFn();
      renderSubStatus(optionalEl('subStatusLine'), translations, currentLang);
    },
    () => void loadSubscriptions(),
  );
  renderSubStatus(optionalEl('subStatusLine'), translations, currentLang);
  syncSubManagePanelBadge();
}

export function initSubscriptionsView(options: SubscriptionsViewOptions): void {
  getLang = options.getCurrentLanguage;
  onUpdateCardsFn = options.onUpdateCards;
  onSubscriptionsLoadedFn = options.onSubscriptionsLoaded;
  showAlertFn = options.showAlert;

  if (isInitialized) return;
  isInitialized = true;

  initSubManagePanel();

  const addSubBtn = optionalEl<HTMLButtonElement>('addSubBtn');
  if (addSubBtn) {
    addSubBtn.onclick = async () => {
      const nameInput = optionalEl<HTMLInputElement>('subName');
      const urlInput = optionalEl<HTMLInputElement>('subUrl');
      if (!nameInput || !urlInput) return;

      const name = nameInput.value.trim();
      const url = urlInput.value.trim();
      if (!name || !url) return;

      nameInput.value = '';
      urlInput.value = '';

      await addSubscriptionByUrl(name, url);
    };
  }

  const importClipboardBtn = optionalEl<HTMLButtonElement>('importClipboardBtn');
  if (importClipboardBtn) {
    importClipboardBtn.onclick = () => {
      window.api.importFromClipboard().catch((err) =>
        console.error('importFromClipboard failed:', err),
      );
    };
  }

  const updateSubBtn = optionalEl<HTMLButtonElement>('updateSubBtn');
  if (updateSubBtn) {
    updateSubBtn.onclick = async () => {
      const t = translations[getLang()];
      const originalText = updateSubBtn.textContent || '';
      updateSubBtn.textContent = t.updateSubBtnBusy;

      try {
        if (currentActiveSubId === 'all') {
          const failures: string[] = [];
          const remoteSubs = allSubscriptions.filter(
            (s) => s.url && s.url !== 'clipboard' && s.url !== 'qrcode' && /^https?:\/\//i.test(s.url),
          );
          for (const sub of remoteSubs) {
            try {
              const res = await window.api.fetchSubscriptionWithInfo(sub.url);
              if (res && res.links && res.links.length > 0) {
                sub.links = res.links;
                if (res.userInfo) {
                  sub.userInfo = res.userInfo;
                }
                sub.lastError = undefined;
                sub.updatedAt = Date.now();
              }
            } catch (e) {
              console.error('Failed to update subscription:', sub.name, e);
              failures.push(`${sub.name}: ${errorText(e)}`);
            }
          }
          await window.api.saveSubscriptions(allSubscriptions);
          await loadSubscriptions();
          if (failures.length > 0) {
            void showAlertFn(t.errorDialogTitle, failures.join('\n\n'), true, t);
          }
        } else {
          const sub = allSubscriptions.find((s) => s.id === currentActiveSubId);
          if (
            sub &&
            sub.url &&
            sub.url !== 'clipboard' &&
            sub.url !== 'qrcode' &&
            /^https?:\/\//i.test(sub.url)
          ) {
            try {
              const res = await window.api.fetchSubscriptionWithInfo(sub.url);
              if (res && res.links && res.links.length > 0) {
                sub.links = res.links;
                if (res.userInfo) {
                  sub.userInfo = res.userInfo;
                }
                sub.lastError = undefined;
                sub.updatedAt = Date.now();
                await window.api.saveSubscriptions(allSubscriptions);
                await loadSubscriptions();
              }
            } catch (e) {
              console.error('Failed to update subscription:', sub.name, e);
              void showAlertFn(t.errorDialogTitle, errorText(e), true, t);
            }
          }
        }
      } finally {
        updateSubBtn.textContent = originalText;
      }
    };
  }

  // QR Modal setup
  const importQrBtn = optionalEl<HTMLButtonElement>('importQrBtn');
  const qrModalOverlay = optionalEl('qrModalOverlay');
  const qrModalClose = optionalEl<HTMLButtonElement>('qrModalClose');
  const qrStartCameraBtn = optionalEl<HTMLButtonElement>('qrStartCameraBtn');
  const qrUploadFileBtn = optionalEl<HTMLButtonElement>('qrUploadFileBtn');
  const qrFileInput = optionalEl<HTMLInputElement>('qrFileInput');
  const qrVideo = optionalEl<HTMLVideoElement>('qrVideo');
  const qrPlaceholder = optionalEl('qrScannerPlaceholder');
  const qrPlaceholderText = optionalEl('qrPlaceholderText');
  const qrReticle = optionalEl('qrScannerReticle');

  if (importQrBtn && qrModalOverlay) {
    importQrBtn.onclick = () => {
      const t = translations[getLang()];
      if (qrPlaceholderText) qrPlaceholderText.textContent = t.qrPlaceholderText;
      qrModalOverlay.style.display = 'flex';
      const focusTarget = qrStartCameraBtn || qrModalOverlay;
      qrModalRelease = trapFocus(qrModalOverlay, focusTarget, closeQrModal);
    };
  }

  if (qrModalClose) {
    qrModalClose.onclick = closeQrModal;
  }

  if (qrUploadFileBtn && qrFileInput) {
    qrUploadFileBtn.onclick = () => {
      stopQrCamera();
      qrFileInput.click();
    };

    qrFileInput.onchange = (e) => {
      const input = e.target as HTMLInputElement;
      const file = input.files?.[0];
      if (!file) return;

      const t = translations[getLang()];
      const reader = new FileReader();
      reader.onload = (event) => {
        const img = new Image();
        img.onload = async () => {
          const tempCanvas = document.createElement('canvas');
          const ctx = tempCanvas.getContext('2d');
          if (!ctx) return;
          tempCanvas.width = img.width;
          tempCanvas.height = img.height;
          ctx.drawImage(img, 0, 0);

          const imageData = ctx.getImageData(0, 0, tempCanvas.width, tempCanvas.height);
          if (typeof jsQR !== 'undefined') {
            const code = jsQR(imageData.data, imageData.width, imageData.height);
            if (code && code.data) {
              await handleQrImport(code.data);
            } else {
              void showAlertFn(t.errorDialogTitle, t.qrNoCodeError, true, t);
            }
          } else {
            console.error('jsQR is not loaded yet');
            void showAlertFn(t.errorDialogTitle, 'QR scanner engine is not loaded yet', true, t);
          }
        };
        img.src = String(event.target?.result ?? '');
      };
      reader.readAsDataURL(file);
      input.value = '';
    };
  }

  if (qrStartCameraBtn && qrVideo && qrReticle && qrPlaceholder) {
    qrStartCameraBtn.onclick = async () => {
      stopQrCamera();
      const t = translations[getLang()];

      try {
        qrStream = await navigator.mediaDevices.getUserMedia({
          video: { facingMode: 'environment' },
        });
        qrVideo.srcObject = qrStream;
        qrVideo.setAttribute('playsinline', 'true');
        qrVideo.style.display = 'block';
        qrReticle.style.display = 'block';
        qrPlaceholder.style.display = 'none';
        await qrVideo.play();

        qrAnimationId = requestAnimationFrame(scanQrFrame);
      } catch (err) {
        console.error('Camera access failed:', err);
        void showAlertFn(t.errorDialogTitle, t.qrCameraError, true, t);
      }
    };
  }

  // Background auto-update events listener
  window.api.onSubscriptionsUpdated(() => {
    void loadSubscriptions();
  });

  // Clipboard import listener
  window.api.onSubscriptionResult(async (links) => {
    const t = translations[getLang()];
    if (!links || links.length === 0) {
      void showAlertFn(t.alertDialogTitle, t.clipboardNoLinks, false, t);
      return;
    }

    let clipSub = allSubscriptions.find((s) => s.url === 'clipboard');
    if (clipSub) {
      const existing = new Set(clipSub.links);
      links.forEach((l) => existing.add(l));
      clipSub.links = Array.from(existing);
    } else {
      const name = t.clipboardSubName;
      clipSub = {
        id: Date.now().toString(),
        name,
        url: 'clipboard',
        links,
      };
      allSubscriptions.push(clipSub);
    }

    await window.api.saveSubscriptions(allSubscriptions);
    await loadSubscriptions();
  });
}
