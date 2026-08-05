import type { NeoBoxApi } from '../modules/api';
import type * as WailsRuntime from '../wailsjs/runtime/runtime';

declare global {
  interface Window {
    /** Мост к Go-бэкенду. Определяется в wails-bridge.ts до загрузки renderer. */
    api: NeoBoxApi;

    /**
     * Рантайм Wails, который тот сам внедряет в страницу. Partial: набор
     * функций зависит от версии рантайма, поэтому вызовы стоит защищать
     * проверкой, как это делает wails-bridge.
     */
    runtime?: Partial<typeof WailsRuntime>;

    /**
     * Счётчики трафика текущей сессии. Живут на window, потому что их пишет
     * обработчик traffic-stats в wails-bridge, а читает история в renderer.
     * Суммирует их Go: пока окно свёрнуто в трей, события сюда не приходят
     * вовсе, и сложение на этой стороне потеряло бы весь трафик за это время.
     */
    sessionBytesDown: number;
    sessionBytesUp: number;
  }

  /**
   * jsQR грузится обычным (не module) тегом script из public/lib и объявляет
   * себя глобально. Возвращает null, если в кадре кода нет.
   */
  function jsQR(
    data: Uint8ClampedArray,
    width: number,
    height: number,
  ): { data: string } | null;
}

export {};
