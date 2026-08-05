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
