// ── ЦВЕТОВАЯ МАТЕМАТИКА ──────────────────────────────────────────────────────
//
// Всё, что нужно, чтобы посчитать контраст и подвинуть цвет, не спрашивая
// браузер. Спрашивать было бы дешевле по коду и дороже по существу:
// getComputedStyle возвращает то, что уже отрисовано, а решать надо до
// отрисовки — иначе первый кадр уходит с непроверенной палитрой.
//
// Модуль намеренно чистый: ни одного обращения к DOM. Отсюда же берётся его
// проверяемость — то же самое можно прогнать в node без браузера вовсе.

export interface RGB {
  r: number;
  g: number;
  b: number;
}

export interface HSL {
  h: number; // 0..360
  s: number; // 0..100
  l: number; // 0..100
}

/**
 * Разбирает #rgb, #rrggbb и то же самое без решётки.
 *
 * Возвращает null, а не чёрный по умолчанию: цвет приходит из поля ввода и из
 * сохранённых настроек, то есть может быть мусором. Молчаливая подстановка
 * чёрного превратила бы опечатку в тему, которую нельзя объяснить.
 */
export function parseHex(input: string): RGB | null {
  const hex = input.trim().replace(/^#/, '');
  if (!/^([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(hex)) return null;

  const full =
    hex.length === 3
      ? hex[0] + hex[0] + hex[1] + hex[1] + hex[2] + hex[2]
      : hex;

  return {
    r: parseInt(full.slice(0, 2), 16),
    g: parseInt(full.slice(2, 4), 16),
    b: parseInt(full.slice(4, 6), 16),
  };
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

const byteOf = (v: number) => clamp(Math.round(v), 0, 255);

export function toHex(c: RGB): string {
  const part = (v: number) => byteOf(v).toString(16).padStart(2, '0');
  return `#${part(c.r)}${part(c.g)}${part(c.b)}`;
}

/** Каналы через пробел — форма, которую ждут токены вида `rgb(var(--x) / α)`. */
export function toChannels(c: RGB): string {
  return `${byteOf(c.r)} ${byteOf(c.g)} ${byteOf(c.b)}`;
}

export function rgbToHsl(c: RGB): HSL {
  const r = c.r / 255;
  const g = c.g / 255;
  const b = c.b / 255;
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  const l = (max + min) / 2;

  if (max === min) return { h: 0, s: 0, l: l * 100 };

  const d = max - min;
  const s = l > 0.5 ? d / (2 - max - min) : d / (max + min);

  let h: number;
  if (max === r) h = ((g - b) / d + (g < b ? 6 : 0)) / 6;
  else if (max === g) h = ((b - r) / d + 2) / 6;
  else h = ((r - g) / d + 4) / 6;

  return { h: h * 360, s: s * 100, l: l * 100 };
}

/**
 * Округляет каналы до того, чем они станут в CSS.
 *
 * Без этого поиск контраста мерил бы дробные каналы, которых на экране не
 * бывает: значение уходит в токен через toChannels, а тот округляет. Разница
 * ровно в сотую, и её хватало, чтобы цвет, посчитанный как 4.500, оказался на
 * экране 4.49 — то есть под порогом, который движок только что объявил взятым.
 */
function quantize(c: RGB): RGB {
  return { r: byteOf(c.r), g: byteOf(c.g), b: byteOf(c.b) };
}

export function hslToRgb(c: HSL): RGB {
  const h = ((c.h % 360) + 360) % 360 / 360;
  const s = clamp(c.s, 0, 100) / 100;
  const l = clamp(c.l, 0, 100) / 100;

  if (s === 0) {
    const v = l * 255;
    return { r: v, g: v, b: v };
  }

  const q = l < 0.5 ? l * (1 + s) : l + s - l * s;
  const p = 2 * l - q;
  const channel = (t: number) => {
    let x = t;
    if (x < 0) x += 1;
    if (x > 1) x -= 1;
    if (x < 1 / 6) return p + (q - p) * 6 * x;
    if (x < 1 / 2) return q;
    if (x < 2 / 3) return p + (q - p) * (2 / 3 - x) * 6;
    return p;
  };

  return {
    r: channel(h + 1 / 3) * 255,
    g: channel(h) * 255,
    b: channel(h - 1 / 3) * 255,
  };
}

/**
 * Относительная светлота по WCAG 2.1.
 *
 * Это не «светлота» из HSL: та описывает положение в цветовом круге, эта —
 * сколько света глаз действительно получает. Зелёный и синий одной светлоты по
 * HSL различаются здесь в шесть раз, и весь контраст считается только так.
 */
export function luminance(c: RGB): number {
  const channel = (v: number) => {
    const x = clamp(v, 0, 255) / 255;
    return x <= 0.03928 ? x / 12.92 : Math.pow((x + 0.055) / 1.055, 2.4);
  };
  return 0.2126 * channel(c.r) + 0.7152 * channel(c.g) + 0.0722 * channel(c.b);
}

/** Коэффициент контраста по WCAG: от 1 (нет разницы) до 21 (чёрное на белом). */
export function contrast(a: RGB, b: RGB): number {
  const la = luminance(a);
  const lb = luminance(b);
  const [hi, lo] = la > lb ? [la, lb] : [lb, la];
  return (hi + 0.05) / (lo + 0.05);
}

/**
 * Кладёт полупрозрачную плёнку на подложку и возвращает то, что увидит глаз.
 *
 * Без этого контраст мерился бы по фону, которого на экране нет: весь
 * интерфейс стоит на стеклянных плёнках, и текст лежит уже на смеси.
 */
export function composite(film: RGB, alpha: number, base: RGB): RGB {
  const a = clamp(alpha, 0, 1);
  return {
    r: film.r * a + base.r * (1 - a),
    g: film.g * a + base.g * (1 - a),
    b: film.b * a + base.b * (1 - a),
  };
}

/**
 * Подтягивает цвет до нужного контраста с подложкой, не трогая тон.
 *
 * Тон — это то, что выбрал человек, и то, что несёт смысл у ролей состояния:
 * зелёный означает «хорошо», красный «опасно». Двигать можно только светлоту,
 * и только в сторону от подложки: к ней — значит сделать хуже.
 *
 * Шаг в один процент, потолок в сто шагов: это доли миллисекунды, зато
 * результат воспроизводим и его можно проверить прогоном, а не на глаз.
 * Возвращает лучшее, что удалось найти, даже если цель недостижима — решать,
 * что с этим делать, будет вызывающий: он единственный знает, есть ли ему чем
 * пожертвовать.
 */
export function liftToContrast(color: RGB, base: RGB, target: number): RGB {
  const start = quantize(color);
  if (contrast(start, base) >= target) return start;

  // Подложка тёмная — уходим вверх, светлая — вниз.
  const up = luminance(base) < 0.5;
  const hsl = rgbToHsl(start);

  let best = start;
  let bestRatio = contrast(start, base);

  // Шаг в полпроцента, а не в целый: на тёмных подложках целый шаг
  // перепрыгивал порог заметно выше нужного и уводил цвет дальше, чем требует
  // читаемость.
  for (let step = 1; step <= 200; step++) {
    const l = up ? hsl.l + step / 2 : hsl.l - step / 2;
    if (l < 0 || l > 100) break;

    const candidate = quantize(hslToRgb({ h: hsl.h, s: hsl.s, l }));
    const ratio = contrast(candidate, base);
    if (ratio > bestRatio) {
      best = candidate;
      bestRatio = ratio;
    }
    if (ratio >= target) return candidate;
  }

  return best;
}

/**
 * Опускает (или поднимает) цвет так, чтобы его собственная светлота по WCAG не
 * превышала предела. Используется для стопов фона: там ограничение стоит не на
 * контраст с чем-то, а на саму яркость — весь текстовый ряд рассчитан на
 * подложку не ярче определённой.
 */
export function capLuminance(c: RGB, maxLum: number): RGB {
  if (luminance(c) <= maxLum) return c;

  const hsl = rgbToHsl(c);
  let lo = 0;
  let hi = hsl.l;

  // Светлота по HSL монотонна по светлоте по WCAG при постоянных тоне и
  // насыщенности, поэтому двоичный поиск сходится и делает это за два десятка
  // шагов вместо сотни.
  for (let i = 0; i < 24; i++) {
    const mid = (lo + hi) / 2;
    if (luminance(hslToRgb({ h: hsl.h, s: hsl.s, l: mid })) > maxLum) hi = mid;
    else lo = mid;
  }

  return hslToRgb({ h: hsl.h, s: hsl.s, l: lo });
}

/** Зеркало capLuminance для светлых тем: подложка не должна быть темнее предела. */
export function floorLuminance(c: RGB, minLum: number): RGB {
  if (luminance(c) >= minLum) return c;

  const hsl = rgbToHsl(c);
  let lo = hsl.l;
  let hi = 100;

  for (let i = 0; i < 24; i++) {
    const mid = (lo + hi) / 2;
    if (luminance(hslToRgb({ h: hsl.h, s: hsl.s, l: mid })) < minLum) lo = mid;
    else hi = mid;
  }

  return hslToRgb({ h: hsl.h, s: hsl.s, l: hi });
}
