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

// ─── Удержание фокуса в диалоге ──────────────────────────────────────────────
//
// Раньше ни одна модалка фокус не удерживала: Tab уводил за оверлей, в кнопки
// подложки, которые визуально закрыты затемнением, а после закрытия фокус
// терялся на <body> и следующий Tab начинал обход с начала страницы.
// showAlert вдобавок вешала обработчик присваиванием document.onkeydown —
// глобальное поле, единственное на весь документ: диалог, открытый поверх
// другого, затирал чужой обработчик, а при закрытии обнулял его совсем.
//
// Стек ниже решает обе задачи. Слушатель всегда один, добавлен через
// addEventListener, и реагирует только верхний диалог стека.

interface DialogEntry {
  container: HTMLElement;
  onEscape: (() => void) | null;
  restoreTo: HTMLElement | null;
}

const dialogStack: DialogEntry[] = [];

const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ');

/**
 * Видимые узлы диалога, до которых Tab может дойти.
 *
 * offsetParent для проверки видимости не годится: оверлей позиционирован
 * fixed, и у всего внутри offsetParent равен null. getClientRects пуст только
 * у действительно скрытых элементов, а скрытых здесь много — кнопка
 * «Копировать» и поле ввода то показываются, то нет.
 */
function focusableWithin(container: HTMLElement): HTMLElement[] {
  return Array.from(container.querySelectorAll<HTMLElement>(FOCUSABLE)).filter(
    (node) => node.getClientRects().length > 0,
  );
}

/**
 * Открыт ли сейчас хоть один модальный диалог.
 *
 * Нужно горячим клавишам приложения: пока диалог на экране, он и есть весь
 * интерфейс, и Ctrl+3 не должен уводить на другую вкладку из-под него.
 * Проверять по стилю оверлея нельзя — модалок шесть, у каждой свой узел, — а
 * стек здесь ровно один и знает про все.
 */
export function isDialogOpen(): boolean {
  return dialogStack.length > 0;
}

function onDialogKeydown(e: KeyboardEvent): void {
  const top = dialogStack[dialogStack.length - 1];
  if (!top) return;

  if (e.key === 'Escape') {
    if (top.onEscape) {
      e.preventDefault();
      top.onEscape();
    }
    return;
  }

  if (e.key !== 'Tab') return;

  const items = focusableWithin(top.container);
  if (items.length === 0) {
    // Фокусировать нечего — но и выпускать наружу нельзя.
    e.preventDefault();
    return;
  }

  const first = items[0];
  const last = items[items.length - 1];
  const active = document.activeElement as HTMLElement | null;

  // Фокус мог оказаться вне диалога (например, после того как элемент, на
  // котором он стоял, скрыли). Возвращаем его на край списка.
  if (!active || !top.container.contains(active)) {
    e.preventDefault();
    (e.shiftKey ? last : first).focus();
    return;
  }

  if (e.shiftKey && active === first) {
    e.preventDefault();
    last.focus();
  } else if (!e.shiftKey && active === last) {
    e.preventDefault();
    first.focus();
  }
}

/**
 * Делает диалог модальным для клавиатуры: запоминает, откуда пришёл фокус,
 * переводит его внутрь и замыкает Tab по кругу.
 *
 * Возвращает функцию закрытия — она снимает удержание и возвращает фокус на
 * элемент, с которого диалог открыли. Показ и скрытие самого оверлея остаются
 * за вызывающим кодом: у разных модалок это разные классы и стили.
 */
export function trapFocus(
  container: HTMLElement,
  initialFocus?: HTMLElement | null,
  onEscape?: () => void,
): () => void {
  const active = document.activeElement;
  const entry: DialogEntry = {
    container,
    onEscape: onEscape ?? null,
    restoreTo: active instanceof HTMLElement ? active : null,
  };

  if (dialogStack.length === 0) {
    document.addEventListener('keydown', onDialogKeydown, true);
  }
  dialogStack.push(entry);

  const target = initialFocus ?? focusableWithin(container)[0] ?? null;
  target?.focus();

  let released = false;
  return () => {
    if (released) return;
    released = true;

    const index = dialogStack.indexOf(entry);
    if (index !== -1) dialogStack.splice(index, 1);
    if (dialogStack.length === 0) {
      document.removeEventListener('keydown', onDialogKeydown, true);
    }

    // Возвращаем фокус только если он всё ещё внутри закрываемого диалога:
    // иначе можно отобрать его у того, кто уже успел забрать.
    const current = document.activeElement;
    if (!current || current === document.body || container.contains(current)) {
      entry.restoreTo?.focus();
    }
  };
}


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
    const release = trapFocus(overlay, inputEl, () => cancelBtn.click());
    inputEl.select();

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      cancelBtn.onclick = null;
      inputEl.onkeydown = null;
      release();
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

    // Enter внутри поля подтверждает — Escape ловится общим обработчиком стека.
    inputEl.onkeydown = (e) => {
      if (e.key === 'Enter') confirmBtn.click();
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
    // Подтверждение разрушающего действия открывалось вообще без клавиатуры:
    // ни Enter, ни Escape, ни перевода фокуса внутрь. Мышью — единственный
    // способ было и подтвердить, и отменить.
    //
    // Фокус ставится на «Отмену»: у диалога, который спрашивает разрешения
    // что-то удалить, безопасный вариант должен быть тем, что сработает на
    // случайный Enter.
    const release = trapFocus(overlay, cancelBtn, () => cancelBtn.click());

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      cancelBtn.onclick = null;
      release();
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
  t: Translations,
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
      msgEl.innerText = isError ? t.errorDialogTitle : message;
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
      copyBtnText.innerText = t.errorDialogCopy;
      copyBtn.onclick = () => {
        navigator.clipboard.writeText(message).then(() => {
          copyBtnText.innerText = t.errorDialogCopied;
          setTimeout(() => {
            copyBtnText.innerText = t.errorDialogCopy;
          }, 1500);
        });
      };
    } else {
      copyBtn.style.display = 'none';
      copyBtn.onclick = null;
    }

    confirmBtn.innerText = t.errorDialogClose;
    overlay.style.display = 'flex';
    const release = trapFocus(overlay, confirmBtn, () => confirmBtn.click());

    const cleanup = () => {
      overlay.style.display = 'none';
      confirmBtn.onclick = null;
      copyBtn.onclick = null;
      overlay.onkeydown = null;
      release();
    };

    confirmBtn.onclick = () => {
      cleanup();
      resolve(true);
    };

    // Enter закрывает откуда угодно внутри диалога. Обработчик висит на самом
    // оверлее, а не на document: глобальное document.onkeydown затирало чужие
    // обработчики и обнулялось при закрытии.
    overlay.onkeydown = (e) => {
      if (e.key === 'Enter') {
        e.preventDefault();
        confirmBtn.click();
      }
    };
  });
}
