// Доступ к элементам разметки.
//
// document.getElementById возвращает HTMLElement | null, и под strict каждое
// обращение пришлось бы сопровождать проверкой — сотни проверок ради случая,
// которого в собранном приложении быть не может: разметка статическая и лежит
// в том же репозитории. Поэтому проверка делается один раз здесь и один раз
// падает с внятным сообщением, если id в index.html переименовали, а код
// забыли поправить.

/**
 * Возвращает элемент по id. Бросает, если элемента нет: отсутствующий id — это
 * рассинхронизация кода и разметки, а не ситуация, которую можно обработать.
 */
export function el<T extends HTMLElement = HTMLElement>(id: string): T {
  const node = document.getElementById(id);
  if (node === null) {
    throw new Error(`element #${id} is missing from index.html`);
  }
  return node as T;
}

/**
 * Возвращает элемент по id или null. Для узлов, которых в некоторых состояниях
 * интерфейса действительно нет.
 */
export function optionalEl<T extends HTMLElement = HTMLElement>(id: string): T | null {
  return document.getElementById(id) as T | null;
}

/** То же, что el, но по CSS-селектору. */
export function query<T extends HTMLElement = HTMLElement>(selector: string): T {
  const node = document.querySelector<T>(selector);
  if (node === null) {
    throw new Error(`no element matches ${selector} in index.html`);
  }
  return node;
}
export function setText(id: string, value: string): void {
  const node = optionalEl(id);
  if (node) node.textContent = value;
}

export function setTitle(id: string, value: string): void {
  const node = optionalEl(id);
  if (node) node.title = value;
}

export function setPlaceholder(id: string, value: string): void {
  const node = optionalEl<HTMLInputElement | HTMLTextAreaElement>(id);
  if (node) node.placeholder = value;
}

export function setAria(id: string, value: string): void {
  const node = optionalEl(id);
  if (node) node.setAttribute('aria-label', value);
}

export function setLabel(id: string, value: string): void {
  const node = optionalEl(id);
  if (!node) return;
  node.title = value;
  node.setAttribute('aria-label', value);
}

export function setFieldLabel(id: string, value: string): void {
  const node = optionalEl<HTMLInputElement | HTMLTextAreaElement>(id);
  if (!node) return;
  node.placeholder = value;
  node.setAttribute('aria-label', value);
}

/**
 * Экранирует спецсимволы HTML для защиты от XSS при вставке в innerHTML.
 */
export function escapeHtml(str: unknown): string {
  return String(str ?? '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    .replace(/'/g, '&#39;');
}

/**
 * Синхронизирует подпись кастомного дропдауна с текущим выбранным <option>.
 */
export function syncCustomSelectLabels(selectId: string): boolean {
  const select = optionalEl<HTMLSelectElement>(selectId);
  if (!select) return false;

  const wrapper = select.nextElementSibling;
  if (!wrapper || !wrapper.classList.contains('custom-select-wrapper')) return false;

  const trigger = wrapper.querySelector('.custom-select-trigger');
  const selectedOption = select.options[select.selectedIndex];
  if (trigger && selectedOption) {
    trigger.textContent = selectedOption.textContent;
  }

  wrapper.querySelectorAll('.custom-select-option').forEach((optDiv, idx) => {
    const opt = select.options[idx];
    if (opt) optDiv.textContent = opt.textContent;
  });
  return true;
}

/**
 * Преобразует нативный <select> в стеклянный кастомный выпадающий список
 * для предотвращения артефактов прозрачности в WebView2.
 */
export function makeSelectCustom(selectId: string): void {
  const select = optionalEl<HTMLSelectElement>(selectId);
  if (!select) return;

  if (syncCustomSelectLabels(selectId)) return;

  const wrapper = document.createElement('div');
  wrapper.className = 'custom-select-wrapper';

  select.style.display = 'none';
  select.parentNode?.insertBefore(wrapper, select.nextSibling);

  const trigger = document.createElement('div');
  trigger.className = 'custom-select-trigger';
  const selectedOption = select.options[select.selectedIndex];
  trigger.textContent = selectedOption ? selectedOption.textContent : '';
  wrapper.appendChild(trigger);

  const optionsContainer = document.createElement('div');
  optionsContainer.className = 'custom-select-options';
  wrapper.appendChild(optionsContainer);

  const buildOptions = () => {
    optionsContainer.innerHTML = '';
    Array.from(select.options).forEach((opt) => {
      const optDiv = document.createElement('div');
      optDiv.className = 'custom-select-option';
      if (opt.value === select.value) {
        optDiv.classList.add('selected');
      }
      optDiv.textContent = opt.textContent;
      optDiv.onclick = (e) => {
        e.stopPropagation();
        select.value = opt.value;
        trigger.textContent = opt.textContent;
        select.dispatchEvent(new Event('change'));
        wrapper.classList.remove('open');
        buildOptions();
      };
      optionsContainer.appendChild(optDiv);
    });
  };

  buildOptions();

  trigger.onclick = (e) => {
    e.stopPropagation();
    document.querySelectorAll('.custom-select-wrapper').forEach((w) => {
      if (w !== wrapper) w.classList.remove('open');
    });
    wrapper.classList.toggle('open');
  };

  document.addEventListener('click', () => {
    wrapper.classList.remove('open');
  });

  select.addEventListener('change', () => {
    const selected = select.options[select.selectedIndex];
    if (selected) {
      trigger.textContent = selected.textContent;
    }
    Array.from(optionsContainer.children).forEach((child, idx) => {
      if (select.options[idx] && select.options[idx].value === select.value) {
        child.classList.add('selected');
      } else {
        child.classList.remove('selected');
      }
    });
  });
}

