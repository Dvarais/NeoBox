import { optionalEl, query } from './dom';

let isScrollScheduled = false;

/**
 * Обновляет позицию и размер бегунка интерактивного виджета прокрутки с rAF-дросселированием.
 */
export function updateCustomScroll(): void {
  if (isScrollScheduled) return;
  isScrollScheduled = true;
  requestAnimationFrame(() => {
    isScrollScheduled = false;
    performUpdateCustomScroll();
  });
}

function performUpdateCustomScroll(): void {
  const scrollWidget = optionalEl('serversScrollWidget');
  const scrollTrack = optionalEl('scrollTrack');
  const scrollThumb = optionalEl('scrollThumb');
  const mainContainer = optionalEl('main') || query('main');
  if (!scrollWidget || !mainContainer || !scrollTrack || !scrollThumb) return;

  const scrollHeight = mainContainer.scrollHeight;
  const clientHeight = mainContainer.clientHeight;
  const scrollTop = mainContainer.scrollTop;

  // Если весь контент помещается на экране, скрываем бегунок и трек
  if (scrollHeight <= clientHeight) {
    scrollThumb.style.display = 'none';
    scrollTrack.style.opacity = '0';
    return;
  }

  scrollThumb.style.display = 'block';
  scrollTrack.style.opacity = '';

  const trackHeight = scrollTrack.clientHeight;
  const thumbHeight = Math.max(40, Math.min(150, (clientHeight / scrollHeight) * trackHeight));
  scrollThumb.style.height = `${thumbHeight}px`;

  const maxScrollTop = scrollHeight - clientHeight;
  const scrollRatio = scrollTop / maxScrollTop;
  const maxThumbTop = trackHeight - thumbHeight;
  const thumbTop = scrollRatio * maxThumbTop;

  scrollThumb.style.transform = `translateY(${thumbTop}px)`;
}

let isInitialized = false;

/**
 * Инициализирует специальный интерактивный виджет прокрутки для сетки серверов и основного окна.
 */
export function initScrollWidget(): void {
  if (isInitialized) return;
  isInitialized = true;

  let isDraggingScroll = false;
  let startScrollY = 0;
  let startScrollTop = 0;

  const scrollWidget = optionalEl('serversScrollWidget');
  const scrollTrack = optionalEl('scrollTrack');
  const scrollThumb = optionalEl('scrollThumb');
  const scrollUpBtn = optionalEl('scrollUpBtn');
  const scrollDownBtn = optionalEl('scrollDownBtn');
  const mainContainer = optionalEl('main') || query('main');
  const serversGrid = optionalEl('serversGrid');

  if (!scrollWidget || !mainContainer || !scrollTrack || !scrollThumb) return;

  mainContainer.addEventListener('scroll', updateCustomScroll, { passive: true });
  window.addEventListener('resize', updateCustomScroll);

  scrollThumb.addEventListener('mousedown', (e) => {
    isDraggingScroll = true;
    startScrollY = e.clientY;
    startScrollTop = mainContainer.scrollTop;
    scrollThumb.style.transition = 'none';
    document.body.style.cursor = 'grabbing';
    document.body.style.userSelect = 'none';
    e.preventDefault();
  });

  document.addEventListener('mousemove', (e) => {
    if (!isDraggingScroll) return;
    const trackHeight = scrollTrack.clientHeight;
    const thumbHeight = scrollThumb.clientHeight;
    const maxThumbTop = trackHeight - thumbHeight;
    if (maxThumbTop <= 0) return;

    const deltaY = e.clientY - startScrollY;
    const scrollHeight = mainContainer.scrollHeight;
    const clientHeight = mainContainer.clientHeight;
    const maxScrollTop = scrollHeight - clientHeight;
    if (maxScrollTop <= 0) return;

    const scrollDelta = (deltaY / maxThumbTop) * maxScrollTop;
    mainContainer.scrollTop = Math.max(0, Math.min(maxScrollTop, startScrollTop + scrollDelta));
  });

  document.addEventListener('mouseup', () => {
    if (isDraggingScroll) {
      isDraggingScroll = false;
      scrollThumb.style.transition = '';
      document.body.style.cursor = '';
      document.body.style.userSelect = '';
    }
  });

  scrollTrack.addEventListener('click', (e) => {
    if (e.target === scrollThumb) return;
    const trackRect = scrollTrack.getBoundingClientRect();
    const clickY = e.clientY - trackRect.top;
    const thumbHeight = scrollThumb.clientHeight;
    const trackHeight = scrollTrack.clientHeight;
    const maxThumbTop = trackHeight - thumbHeight;
    if (maxThumbTop <= 0) return;

    const targetRatio = (clickY - thumbHeight / 2) / maxThumbTop;
    const scrollHeight = mainContainer.scrollHeight;
    const clientHeight = mainContainer.clientHeight;
    const maxScrollTop = scrollHeight - clientHeight;
    if (maxScrollTop <= 0) return;

    mainContainer.scrollTo({
      top: Math.max(0, Math.min(maxScrollTop, targetRatio * maxScrollTop)),
      behavior: 'smooth',
    });
  });

  if (scrollUpBtn) {
    scrollUpBtn.addEventListener('click', () => {
      mainContainer.scrollBy({ top: -200, behavior: 'smooth' });
    });
  }

  if (scrollDownBtn) {
    scrollDownBtn.addEventListener('click', () => {
      mainContainer.scrollBy({ top: 200, behavior: 'smooth' });
    });
  }

  if (serversGrid) {
    const observer = new MutationObserver(() => {
      setTimeout(updateCustomScroll, 50);
    });
    observer.observe(serversGrid, { childList: true, subtree: true });
  }

  updateCustomScroll();
}
