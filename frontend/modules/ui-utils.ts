import { el } from './dom';
import type { Translations } from './translations';

// Модальные окна и определение внешнего IP.
//
// Идентификаторы узлов раньше передавались аргументами: showPrompt и showConfirm
// принимали пять id перед самим текстом, а showConfirm вдобавок несла шим,
// подставлявший те же id, если аргумент был всего один. Смысла в этом не было —
// оба диалога всегда работали ровно с одним набором элементов, и все вызовы
// передавали одни и те же пять строк. Теперь id живут здесь, в одном месте.

/** Общая модалка «введите значение» / «подтвердите». */
const MODAL = {
  overlay: 'modalOverlay',
  title: 'modalTitle',
  input: 'modalInput',
  cancel: 'modalCancel',
  confirm: 'modalConfirm',
} as const;

/**
 * Определяет внешний IP и пишет его в переданный элемент.
 *
 * Запрос повторяется: сразу после подключения маршрут ещё перестраивается, и
 * первая попытка почти всегда попадает в этот промежуток.
 */
export async function fetchIP(
  currentIpElement: HTMLElement,
  t: Translations,
  retryCount = 5,
): Promise<void> {
  currentIpElement.textContent = t.ipDetermining;
  for (let i = 0; i < retryCount; i++) {
    try {
      const controller = new AbortController();
      const timeoutId = setTimeout(() => controller.abort(), 5000);

      const res = await fetch('https://api.ipify.org?format=json', {
        signal: controller.signal,
        cache: 'no-store',
      });
      clearTimeout(timeoutId);

      const data = await res.json();
      if (data && data.ip) {
        currentIpElement.textContent = data.ip;
        return;
      }
    } catch (e) {
      console.error(`IP fetch attempt ${i + 1} failed:`, e);
      if (i < retryCount - 1) {
        await new Promise((res) => setTimeout(res, 3000)); // Ждем 3 сек
      }
    }
  }
  currentIpElement.textContent = t.ipError;
}

/** Спрашивает строку. Резолвится в null, если пользователь отменил. */
export function showPrompt(title: string, defaultValue = ''): Promise<string | null> {
  return new Promise((resolve) => {
    const overlay = el(MODAL.overlay);
    const titleEl = el(MODAL.title);
    const inputEl = el<HTMLInputElement>(MODAL.input);
    const cancelBtn = el<HTMLButtonElement>(MODAL.cancel);
    const confirmBtn = el<HTMLButtonElement>(MODAL.confirm);

    titleEl.innerText = title;
    inputEl.value = defaultValue;
    inputEl.style.display = 'block';

    overlay.style.display = 'flex';
    inputEl.focus();
    inputEl.select();

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      cancelBtn.onclick = null;
      inputEl.onkeydown = null;
    };

    confirmBtn.onclick = () => {
      const val = inputEl.value;
      cleanup();
      resolve(val);
    };

    cancelBtn.onclick = () => {
      cleanup();
      resolve(null);
    };

    inputEl.onkeydown = (e) => {
      if (e.key === 'Enter') confirmBtn.click();
      if (e.key === 'Escape') cancelBtn.click();
    };
  });
}

/** Спрашивает подтверждение. Та же модалка, но поле ввода скрыто. */
export function showConfirm(message: string): Promise<boolean> {
  return new Promise((resolve) => {
    const overlay = el(MODAL.overlay);
    const titleEl = el(MODAL.title);
    const inputEl = el<HTMLInputElement>(MODAL.input);
    const cancelBtn = el<HTMLButtonElement>(MODAL.cancel);
    const confirmBtn = el<HTMLButtonElement>(MODAL.confirm);

    titleEl.innerText = message;
    inputEl.style.display = 'none';

    overlay.style.display = 'flex';

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      cancelBtn.onclick = null;
    };

    confirmBtn.onclick = () => {
      cleanup();
      resolve(true);
    };

    cancelBtn.onclick = () => {
      cleanup();
      resolve(false);
    };
  });
}

/**
 * Показывает сообщение с одной кнопкой. Длинные ошибки (сообщения sing-box)
 * уезжают в моноширинный блок с кнопкой «копировать».
 */
export function showAlert(
  title: string,
  message: string,
  isError = false,
  t: Partial<Translations> = {},
): Promise<true> {
  return new Promise((resolve) => {
    const overlay = el('alertOverlay');
    const titleEl = el('alertTitleText');
    const msgEl = el('alertMessageText');
    const termContainer = el('errorTerminalContainer');
    const termText = el('errorTerminalText');
    const copyBtn = el<HTMLButtonElement>('alertCopyBtn');
    const copyBtnText = el('alertCopyBtnText');
    const confirmBtn = el<HTMLButtonElement>('alertConfirmBtn');

    titleEl.innerText = title;

    // Style icon or container according to isError
    const iconContainer = overlay.querySelector('.alert-icon-container');
    if (isError) {
      overlay.classList.add('error-mode');
      if (iconContainer) iconContainer.classList.add('error');
    } else {
      overlay.classList.remove('error-mode');
      if (iconContainer) iconContainer.classList.remove('error');
    }

    // Check if the message is extremely long (like a sing-box initialize error) and put it into the terminal container
    const isLongError =
      isError &&
      (message.length > 80 ||
        message.includes('\n') ||
        message.includes('failed') ||
        message.includes('uTLS'));

    if (isLongError) {
      msgEl.innerText = isError ? t.errorDialogTitle || 'Error' : message;
      termText.innerText = message;
      termContainer.style.display = 'block';
    } else {
      msgEl.innerText = message;
      termContainer.style.display = 'none';
      termText.innerText = '';
    }

    // Copy to clipboard setup
    if (isError) {
      copyBtn.style.display = 'flex';
      copyBtnText.innerText = t.errorDialogCopy || 'Copy';
      copyBtn.onclick = () => {
        navigator.clipboard.writeText(message).then(() => {
          copyBtnText.innerText = t.errorDialogCopied || 'Copied!';
          setTimeout(() => {
            copyBtnText.innerText = t.errorDialogCopy || 'Copy';
          }, 1500);
        });
      };
    } else {
      copyBtn.style.display = 'none';
      copyBtn.onclick = null;
    }

    confirmBtn.innerText = t.errorDialogClose || 'OK';
    overlay.style.display = 'flex';
    confirmBtn.focus();

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      copyBtn.onclick = null;
      document.onkeydown = null;
    };

    confirmBtn.onclick = () => {
      cleanup();
      resolve(true);
    };

    document.onkeydown = (e) => {
      if (e.key === 'Enter' || e.key === 'Escape') {
        confirmBtn.click();
      }
    };
  });
}
