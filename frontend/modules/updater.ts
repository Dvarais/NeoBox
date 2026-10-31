import { el } from './dom';
import { errorText, trapFocus } from './ui-utils';
import type { UpdateInfo } from './api';
import type { Translations } from './translations';

export function showUpdateModal(update: UpdateInfo, t: Translations): Promise<boolean> {
  return new Promise<boolean>((resolve) => {
    const overlay = el('updateModalOverlay');
    const versionEl = el('updateModalVersion');
    const changelogEl = el('updateModalChangelog');
    const cancelBtn = el<HTMLButtonElement>('updateModalCancel');
    const confirmBtn = el<HTMLButtonElement>('updateModalConfirm');
    const progressSection = el('updateProgressSection');
    const progressStatus = el('updateProgressStatus');
    const progressPercent = el('updateProgressPercent');
    const progressBar = el('updateProgressBar');
    const actions = el('updateModalActions');

    versionEl.textContent = `v${update.version}`;
    changelogEl.textContent = update.body || t.updateNoChangelog;

    progressSection.style.display = 'none';
    progressBar.style.transform = 'scaleX(0)';
    progressPercent.textContent = '0%';
    overlay.querySelector('.update-modal-content')?.classList.remove('downloading');
    progressStatus.textContent = t.updateProgressStatusDownloading;
    progressStatus.style.color = 'var(--text-dim)';

    actions.style.display = 'flex';
    cancelBtn.style.display = 'block';
    cancelBtn.disabled = false;
    confirmBtn.style.display = 'block';
    confirmBtn.disabled = false;
    confirmBtn.textContent = t.updateModalConfirm;

    overlay.style.display = 'flex';
    const release = trapFocus(overlay, cancelBtn, () => cancelBtn.click());

    cancelBtn.onclick = () => {
      overlay.style.display = 'none';
      release();
      resolve(false);
    };

    confirmBtn.onclick = async () => {
      if (!update.downloadUrl) {
        if (update.url) window.api.openUpdateLink(update.url);
        overlay.style.display = 'none';
        release();
        resolve(true);
        return;
      }

      cancelBtn.style.display = 'none';
      confirmBtn.disabled = true;
      confirmBtn.textContent = t.updateDownloading;
      progressSection.style.display = 'block';
      overlay.querySelector('.update-modal-content')?.classList.add('downloading');

      const cleanupEvents: Array<() => void> = [];

      const onProgress = (percent: number) => {
        progressBar.style.transform = `scaleX(${percent / 100})`;
        progressPercent.textContent = `${percent}%`;
        progressBar.parentElement?.setAttribute('aria-valuenow', String(percent));
      };

      const onComplete = () => {
        progressBar.style.transform = 'scaleX(1)';
        progressPercent.textContent = '100%';
        progressStatus.textContent = t.updateProgressStatusComplete;
        progressStatus.style.color = 'var(--success)';

        overlay.querySelector('.update-modal-content')?.classList.remove('downloading');
        progressBar.parentElement?.setAttribute('aria-valuenow', '100');
        cleanupEvents.forEach((dereg) => dereg());
        setTimeout(() => {
          overlay.style.display = 'none';
          release();
          resolve(true);
        }, 1500);
      };

      const onError = (errMsg: string) => {
        progressStatus.textContent = `${t.updateProgressStatusError}: ${errMsg}`;
        progressStatus.style.color = 'var(--danger)';
        overlay.querySelector('.update-modal-content')?.classList.remove('downloading');

        cancelBtn.style.display = 'block';
        cancelBtn.disabled = false;
        confirmBtn.style.display = 'none';

        cleanupEvents.forEach((dereg) => dereg());
      };

      const unsubProgress = window.api.onUpdateProgress(onProgress);
      const unsubComplete = window.api.onUpdateComplete(onComplete);
      const unsubError = window.api.onUpdateError(onError);
      cleanupEvents.push(unsubProgress, unsubComplete, unsubError);

      try {
        await window.api.downloadAndInstallUpdate(update.downloadUrl, update.signatureHex || '');
      } catch (err) {
        onError(errorText(err));
      }
    };
  });
}

export async function checkForUpdates(t: Translations): Promise<void> {
  try {
    const update = await window.api.checkUpdates();
    if (update && update.available) {
      await showUpdateModal(update, t);
    }
  } catch (e) {
    console.error('Update check failed:', e);
  }
}
