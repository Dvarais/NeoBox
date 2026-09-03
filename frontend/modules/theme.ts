// ── ТЕМА ИНТЕРФЕЙСА ─────────────────────────────────────────────────────────
//
// Пользователь задаёт два цвета — акцент и фон. Всё остальное выводится здесь.
//
// Почему именно так, а не «дайте покрасить каждый токен». Палитра в style.css
// это не набор независимых цветов, а лестница ролей, у которой есть несущее
// свойство: любой текст читается на любой поверхности с коэффициентом не ниже
// 4.5:1. Это обязательство продукта (PRODUCT.md, «Доступность»), и оно
// держится не сложением цветов, а их отношениями. Отдать роли по одной значило
// бы отдать и отношения — то есть предложить человеку собрать нечитаемый
// интерфейс и назвать это свободой.
//
// Поэтому свобода здесь в другом месте: тон акцента и тон фона принадлежат
// пользователю целиком и никогда не меняются. Двигается только светлота, и
// только когда без этого текст уходит под порог. Приложение говорит, что
// подвинуло и на сколько (см. describeTheme) — молча оно ничего не правит.
//
// Ровно это раньше делалось вручную. В style.css лежат комментарии, объясняющие,
// почему --danger-rgb пришлось поднять с #ef4444, а --text-faint с #7887a0: обе
// правки — измеренный контраст на самой невыгодной поверхности. Дальше это
// делает не человек с калькулятором, а код, и делает на любой теме.

import {
  type RGB,
  capLuminance,
  composite,
  contrast,
  floorLuminance,
  hslToRgb,
  liftToContrast,
  luminance,
  parseHex,
  rgbToHsl,

  toChannels,
  toHex,
} from './color';

export type BackgroundMode = 'solid' | 'linear' | 'radial';

/** Тёмное приложение со светлым текстом или наоборот. */
export type Scheme = 'dark' | 'light';

export interface ThemeSpec {
  /**
   * Тёмный режим или светлый. Выбор человека, а не вывод из цвета фона.
   *
   * Сначала здесь ничего не было: движок определял режим сам, сравнивая, во
   * сколько обойдётся привести фон к тёмному ряду и во сколько — к светлому, и
   * шёл туда, где правка меньше. Считалось это в единицах светлоты HSL, а та
   * не имеет отношения к воспринимаемой яркости и врёт по-разному для разных
   * тонов. Результат оказывался произвольным с точки зрения человека: синий
   * #3b82f6 и бирюзовый #0891b2 имеют ровно одну яркость (0.235), но первый
   * уводил приложение в белое, а второй оставлял тёмным.
   *
   * Но дело даже не в мере. Светлая тема — это намерение, а не свойство цвета:
   * из тона нельзя узнать, хочет человек тёмное приложение с синим отливом или
   * светлое. Движок угадывал там, где обязан был спросить. Теперь спрашивает.
   */
  scheme: Scheme;
  /** Цвет действия. Тон и насыщенность сохраняются как есть. */
  accent: string;
  bg: {
    mode: BackgroundMode;
    /** Направление линейного градиента в градусах. 90 — слева направо. */
    angle: number;
    /** От одного до трёх стопов. Один стоп = сплошная заливка. */
    stops: string[];
  };
}

/**
 * Стандартный мир NeoBox: почти чёрный → сливовый → индиго, акцент голубой.
 *
 * Это не «одна из тем», а обязательство бренда — то, что человек видит из
 * коробки. Значения совпадают с токенами :root в style.css буква в букву, и
 * движок на них ничего не правит: они и были посчитаны под тот же порог.
 */
export const DEFAULT_THEME: ThemeSpec = {
  scheme: 'dark',
  accent: '#38bdf8',
  bg: { mode: 'linear', angle: 90, stops: ['#080909', '#2b0d33', '#1a1748'] },
};

export interface ThemePreset {
  id: string;
  /** Ключ в translations; название показывается на обоих языках. */
  labelKey: string;
  spec: ThemeSpec;
}

export const PRESETS: ThemePreset[] = [
  { id: 'neobox', labelKey: 'themePresetNeobox', spec: DEFAULT_THEME },
  {
    id: 'midnight',
    labelKey: 'themePresetMidnight',
    spec: { scheme: 'dark', accent: '#60a5fa', bg: { mode: 'linear', angle: 90, stops: ['#05070c', '#0b1220', '#111c33'] } },
  },
  {
    id: 'malachite',
    labelKey: 'themePresetMalachite',
    spec: { scheme: 'dark', accent: '#34d399', bg: { mode: 'linear', angle: 90, stops: ['#04100c', '#07231a', '#0b3327'] } },
  },
  {
    id: 'amber',
    labelKey: 'themePresetAmber',
    spec: { scheme: 'dark', accent: '#fbbf24', bg: { mode: 'linear', angle: 90, stops: ['#100a04', '#23150a', '#33200e'] } },
  },
  {
    id: 'rose',
    labelKey: 'themePresetRose',
    spec: { scheme: 'dark', accent: '#fb7185', bg: { mode: 'linear', angle: 90, stops: ['#100408', '#260b18', '#360f26'] } },
  },
  {
    id: 'graphite',
    labelKey: 'themePresetGraphite',
    spec: { scheme: 'dark', accent: '#a78bfa', bg: { mode: 'radial', angle: 90, stops: ['#0b0b0c', '#151517', '#1f1f22'] } },
  },
  {
    id: 'paper',
    labelKey: 'themePresetPaper',
    spec: { scheme: 'light', accent: '#0369a1', bg: { mode: 'linear', angle: 90, stops: ['#ffffff', '#f3f4f6', '#e7e9ee'] } },
  },
];

// ── Опорные ряды ─────────────────────────────────────────────────────────────
//
// Тёмный ряд — сегодняшние значения style.css без единой правки. Из-за этого
// стандартная тема проходит через движок и выходит из него собой: ни один
// байт палитры по умолчанию не меняется. Светлый ряд — его зеркало.

const RAMP = {
  dark: {
    textMain: '#f1f5f9',
    textDim: '#94a3b8',
    textFaint: '#939fb3',
    logMsg: '#cbd5e1',
    /** Плёнки стекла: белые поверх тёмного. */
    surface: '#ffffff',
    shadow: '#000000',
    /**
     * Насыщенность и светлота панелей; тон берётся у фона. Числа — разобранные
     * на HSL сегодняшние #1a1024, #120a1a, #17102a и #0d0a14 из style.css,
     * поэтому на стандартной теме движок возвращает ровно их.
     */
    panel: { s: 38.5, l: 10.2 },
    panelDeep: { s: 44.4, l: 7.1 },
    panelSolid: { s: 44.8, l: 11.4 },
    chip: { s: 33.3, l: 5.9 },
  },
  light: {
    textMain: '#0f172a',
    textDim: '#475569',
    textFaint: '#4b586e',
    logMsg: '#334155',
    /** Плёнки стекла: тёмные поверх светлого — иначе их попросту не видно. */
    surface: '#0b1020',
    shadow: '#1e293b',
    panel: { s: 40, l: 97.5 },
    panelDeep: { s: 30, l: 94 },
    panelSolid: { s: 34, l: 96 },
    chip: { s: 26, l: 99 },
  },
} as const;

/**
 * Роли состояний. Тон у каждой закреплён и за акцентом не ходит: зелёный
 * означает «идёт как надо», красный — «сломалось», и перекрашивать их вслед за
 * вкусом значило бы стереть единственное, что они сообщают. Движок трогает у
 * них только светлоту, и только чтобы вытащить над порогом.
 */
const SIGNAL_BASE = {
  success: '#22c55e',
  warning: '#eab308',
  danger: '#f47c7c',
  debug: '#bf81f9',
  attention: '#f59e0b',
  trafficDown: '#10b981',
  trafficUp: '#c58efc',
  logComponent: '#f57fbd',
} as const;

/** Порог AA для обычного текста. Продукт обязался держать его везде. */
const AA = 4.5;

/**
 * Плёнка, на которой меряется контраст.
 *
 * PRODUCT.md называет самой невыгодной поверхностью «правый край градиента под
 * плёнкой --glass-01» (3%). Здесь берётся 5% — на ступень плотнее: карточки
 * стоят на --glass-01, но панели и строки таблиц уходят до --glass-02, и запас
 * в одну ступень стоит дешевле, чем разбирательство, какая именно поверхность
 * оказалась под конкретной строкой.
 */
const MEASURE_FILM_ALPHA = 0.05;

interface Palette {
  scheme: Scheme;
  /** Стопы фона после того, как их привели к пригодной яркости. */
  stops: RGB[];
  /** Самая невыгодная поверхность: худший стоп под измерительной плёнкой. */
  worst: RGB;
  accent: RGB;
  /** Насколько пришлось подвинуть светлоту акцента, в процентных пунктах. */
  accentShift: number;
  /** Насколько пришлось подвинуть самый неудобный стоп фона. */
  bgShift: number;
}

function hexOr(value: string, fallback: string): RGB {
  return parseHex(value) ?? (parseHex(fallback) as RGB);
}

/** Сдвигает светлоту и насыщенность, оставляя тон нетронутым. */
function shiftTone(c: RGB, deltaL: number, deltaS: number): RGB {
  const hsl = rgbToHsl(c);
  return hslToRgb({ h: hsl.h, s: hsl.s + deltaS, l: hsl.l + deltaL });
}

/**
 * Предел яркости подложки: ярче — и самый тусклый уровень текста уходит под
 * порог. Выводится из ряда, а не задаётся числом, поэтому правка ряда не может
 * втихую разойтись с проверкой.
 */
function backgroundLuminanceBound(ramp: typeof RAMP.dark | typeof RAMP.light, dark: boolean): number {
  const faint = luminance(hexOr(ramp.textFaint, '#939fb3'));
  return dark
    ? (faint + 0.05) / AA - 0.05 // подложка должна быть не ярче
    : AA * (faint + 0.05) - 0.05; // …или не темнее
}

/**
 * Приводит один стоп фона к пригодной яркости с учётом плёнки, которая на нём
 * лежит. Тон и насыщенность сохраняются целиком — двигается светлота.
 */
function constrainStop(stop: RGB, film: RGB, bound: number, dark: boolean): RGB {
  const surface = composite(film, MEASURE_FILM_ALPHA, stop);
  if (dark ? luminance(surface) <= bound : luminance(surface) >= bound) return stop;

  // Плёнка смещает результат, поэтому ограничение ставится на сам стоп с
  // поправкой: пересчитываем предел так, будто плёнки нет, и добираем в
  // несколько проходов — сходится за два-три.
  let result = stop;
  for (let pass = 0; pass < 6; pass++) {
    const withFilm = composite(film, MEASURE_FILM_ALPHA, result);
    const excess = dark ? luminance(withFilm) - bound : bound - luminance(withFilm);
    if (excess <= 0.0005) break;
    const own = luminance(result);
    const target = dark ? Math.max(0, own - excess) : Math.min(1, own + excess);
    result = dark ? capLuminance(result, target) : floorLuminance(result, target);
  }
  return result;
}

/** Средний тон фона, взвешенный по насыщенности: серый стоп не тянет одеяло. */
function backgroundHue(stops: RGB[]): number {
  let x = 0;
  let y = 0;
  for (const stop of stops) {
    const hsl = rgbToHsl(stop);
    const weight = hsl.s;
    x += Math.cos((hsl.h * Math.PI) / 180) * weight;
    y += Math.sin((hsl.h * Math.PI) / 180) * weight;
  }
  if (x === 0 && y === 0) return 0;
  return ((Math.atan2(y, x) * 180) / Math.PI + 360) % 360;
}

function buildPalette(spec: ThemeSpec): Palette {
  const rawStops = spec.bg.stops
    .map((s) => parseHex(s))
    .filter((c): c is RGB => c !== null);
  const stops = rawStops.length > 0 ? rawStops : (DEFAULT_THEME.bg.stops.map((s) => parseHex(s)) as RGB[]);

  // Режим берётся из темы и ничем не пересматривается. Здесь стоял расчёт,
  // который выбирал его сам, — см. ThemeSpec.scheme о том, почему его больше
  // нет: он менял приложение на белое от выбора цвета, которым человек этого
  // не просил.
  const scheme: Scheme = spec.scheme === 'light' ? 'light' : 'dark';
  const dark = scheme === 'dark';
  const ramp = dark ? RAMP.dark : RAMP.light;
  const film = hexOr(ramp.surface, '#ffffff');
  const bound = backgroundLuminanceBound(ramp, dark);

  const fixedStops = stops.map((stop) => constrainStop(stop, film, bound, dark));
  const bgShift = fixedStops.reduce(
    (max, fixed, i) => Math.max(max, Math.abs(rgbToHsl(fixed).l - rgbToHsl(stops[i]).l)),
    0,
  );

  // Самая невыгодная поверхность: тот стоп, что ближе всего к цвету текста,
  // уже под плёнкой.
  const worst = fixedStops
    .map((stop) => composite(film, MEASURE_FILM_ALPHA, stop))
    .reduce((a, b) => (dark ? (luminance(a) > luminance(b) ? a : b) : luminance(a) < luminance(b) ? a : b));

  const rawAccent = hexOr(spec.accent, DEFAULT_THEME.accent);
  const accent = liftToContrast(rawAccent, worst, AA);

  return {
    scheme,
    stops: fixedStops,
    worst,
    accent,
    accentShift: Math.abs(rgbToHsl(accent).l - rgbToHsl(rawAccent).l),
    bgShift,
  };
}

/** Пересобирает шеврон выпадающего списка в заданном цвете. */
function selectChevron(hex: string): string {
  const stroke = encodeURIComponent(hex);
  return (
    `url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='24' height='24' ` +
    `viewBox='0 0 24 24' fill='none' stroke='${stroke}' stroke-width='2' stroke-linecap='round' ` +
    `stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'%3E%3C/polyline%3E%3C/svg%3E")`
  );
}

/** Собирает CSS-значение фона из режима, угла и стопов. */
function backgroundImage(spec: ThemeSpec, stops: RGB[]): string {
  const list = stops.map(toHex);
  if (spec.bg.mode === 'solid' || list.length < 2) return 'none';

  if (spec.bg.mode === 'radial') {
    // Из центра наружу: центр — самый тёмный стоп, как в модальных окнах.
    return `radial-gradient(circle at 50% 45%, ${list.join(', ')})`;
  }

  // Голые проценты между стопами — подсказки интерполяции, они же смягчают
  // излом на стыке секций. Ровно те же, что стояли в style.css.
  if (list.length === 3) {
    return `linear-gradient(${spec.bg.angle}deg, ${list[0]} 0%, 30%, ${list[1]} 50%, 70%, ${list[2]} 100%)`;
  }
  return `linear-gradient(${spec.bg.angle}deg, ${list.join(', ')})`;
}

/**
 * Считает все токены палитры. Чистая функция: её же прогоняет проверка
 * контраста, не поднимая браузер.
 */
export function deriveTokens(spec: ThemeSpec): Record<string, string> {
  const palette = buildPalette(spec);
  const dark = palette.scheme === 'dark';
  const ramp = dark ? RAMP.dark : RAMP.light;
  const { worst, accent } = palette;

  const hue = backgroundHue(palette.stops);
  const panel = (role: { s: number; l: number }) => hslToRgb({ h: hue, s: role.s, l: role.l });

  // Текст: ряд берётся опорным и поднимается только если не дотягивает.
  const text = (hex: string) => toHex(liftToContrast(hexOr(hex, '#ffffff'), worst, AA));
  // Роли состояний: тон закреплён, светлота подтягивается.
  const signal = (hex: string) => liftToContrast(hexOr(hex, '#ffffff'), worst, AA);

  const onAccent =
    contrast({ r: 0, g: 0, b: 0 }, accent) >= contrast({ r: 255, g: 255, b: 255 }, accent)
      ? '#000000'
      : '#ffffff';

  const tokens: Record<string, string> = {
    // Фон
    '--bg-image': backgroundImage(spec, palette.stops),
    '--bg-color': toHex(palette.stops[0]),
    '--grad-start': toHex(palette.stops[0]),
    '--grad-mid': toHex(palette.stops[Math.min(1, palette.stops.length - 1)]),
    '--grad-end': toHex(palette.stops[palette.stops.length - 1]),

    // Примитивы
    '--accent-rgb': toChannels(accent),
    '--success-rgb': toChannels(signal(SIGNAL_BASE.success)),
    '--warning-rgb': toChannels(signal(SIGNAL_BASE.warning)),
    '--danger-rgb': toChannels(signal(SIGNAL_BASE.danger)),
    '--debug-rgb': toChannels(signal(SIGNAL_BASE.debug)),
    '--panel-rgb': toChannels(panel(ramp.panel)),
    '--bg-rgb': toChannels(palette.stops[0]),
    '--surface-rgb': toChannels(hexOr(ramp.surface, '#ffffff')),
    '--shadow-rgb': toChannels(hexOr(ramp.shadow, '#000000')),
    '--chip-rgb': toChannels(panel(ramp.chip)),

    // Поверхности
    '--panel-deep': toHex(panel(ramp.panelDeep)),
    '--panel-solid': toHex(panel(ramp.panelSolid)),

    // Текст
    '--text-main': text(ramp.textMain),
    '--text-dim': text(ramp.textDim),
    '--text-faint': text(ramp.textFaint),
    // Шеврон <select>: единственная картинка в CSS с вписанным цветом. Фоновая
    // картинка не наследует currentColor, поэтому цвет вписывается сюда.
    '--select-chevron': selectChevron(text(ramp.textFaint)),

    // Действие. Смещения — расстояние между сегодняшними #38bdf8, #0ea5e9 и
    // #7dd3fc: низ градиентов на одиннадцать пунктов темнее акцента и чуть
    // спокойнее по насыщенности, наведение на четырнадцать светлее и чуть
    // звонче. Тон не двигается ни там, ни там.
    '--accent-deep': toHex(shiftTone(accent, -11.2, -4.5)),
    '--accent-soft': toHex(shiftTone(accent, 14.3, 2.3)),
    '--on-accent': onAccent,

    // Состояния и данные
    '--attention': toHex(signal(SIGNAL_BASE.attention)),
    '--log-msg': text(ramp.logMsg),
    '--log-component': toHex(signal(SIGNAL_BASE.logComponent)),
    '--traffic-down': toHex(signal(SIGNAL_BASE.trafficDown)),
    '--traffic-up': toHex(signal(SIGNAL_BASE.trafficUp)),
  };

  return tokens;
}

export interface ThemeReport {
  scheme: Scheme;
  /** Худший контраст среди всех текстовых ролей. */
  textContrast: number;
  accentContrast: number;
  /** Худший контраст среди ролей состояния. */
  signalContrast: number;
  /** Движок опустил или поднял стопы фона ради читаемости. */
  backgroundAdjusted: boolean;
  /** Движок подвинул светлоту акцента. */
  accentAdjusted: boolean;
  /** Итоговый акцент — то, что реально увидит глаз. */
  accentApplied: string;
  /** Всё ли уложилось в порог AA. */
  passesAA: boolean;
}

/**
 * Отчёт для интерфейса настроек. Приложение обязано сказать, что оно поправило
 * и с каким результатом: правка палитры без объяснения выглядит как поломка.
 */
export function describeTheme(spec: ThemeSpec): ThemeReport {
  const palette = buildPalette(spec);
  const tokens = deriveTokens(spec);
  const { worst } = palette;

  const ratio = (hex: string) => contrast(hexOr(hex, '#ffffff'), worst);
  const textContrast = Math.min(
    ratio(tokens['--text-main']),
    ratio(tokens['--text-dim']),
    ratio(tokens['--text-faint']),
  );
  const signalContrast = Math.min(
    ...[
      tokens['--success-rgb'],
      tokens['--warning-rgb'],
      tokens['--danger-rgb'],
      tokens['--debug-rgb'],
    ].map((channels) => {
      const [r, g, b] = channels.split(' ').map(Number);
      return contrast({ r, g, b }, worst);
    }),
  );
  const accentContrast = contrast(palette.accent, worst);

  return {
    scheme: palette.scheme,
    textContrast,
    accentContrast,
    signalContrast,
    // Полпроцентного пункта светлоты — ниже порога различимости, и объявлять
    // такое правкой значит поднимать шум на ровном месте.
    backgroundAdjusted: palette.bgShift > 0.5,
    accentAdjusted: palette.accentShift > 0.5,
    accentApplied: toHex(palette.accent),
    passesAA: Math.min(textContrast, accentContrast, signalContrast) >= AA - 0.005,
  };
}

/**
 * Разбирает тему из того, что пришло с диска, и достраивает недостающее.
 *
 * Возвращает null, если это вообще не тема: настройки правятся блокнотом
 * (settings.json намеренно оставлен читаемым), да и зеркало в localStorage
 * может оказаться от прошлой версии.
 *
 * Отдельная забота — темы, сохранённые до появления поля scheme. Дописывать им
 * режим по яркости фона было бы повторением той же ошибки, от которой поле и
 * завелось, но здесь случай другой: тема уже существует, её фон уже приведён к
 * своему ряду, и вопрос не «чего хочет человек», а «в каком ряду это лежит».
 * На такой вопрос яркость отвечает верно. Порог взят с большим запасом в
 * сторону тёмного: приложение тёмное по умолчанию, и ошибиться в эту сторону
 * дешевле.
 */
export function parseTheme(value: unknown): ThemeSpec | null {
  if (typeof value !== 'object' || value === null) return null;
  const spec = value as Partial<ThemeSpec>;
  if (typeof spec.accent !== 'string' || parseHex(spec.accent) === null) return null;
  if (typeof spec.bg !== 'object' || spec.bg === null) return null;
  const { mode, angle, stops } = spec.bg as ThemeSpec['bg'];
  if (mode !== 'solid' && mode !== 'linear' && mode !== 'radial') return null;
  if (typeof angle !== 'number' || !Number.isFinite(angle)) return null;
  if (!Array.isArray(stops) || stops.length < 1 || stops.length > 3) return null;
  if (!stops.every((s) => typeof s === 'string' && parseHex(s) !== null)) return null;

  let scheme: Scheme;
  if (spec.scheme === 'light' || spec.scheme === 'dark') {
    scheme = spec.scheme;
  } else {
    const brightest = Math.max(...stops.map((s) => luminance(parseHex(s) as RGB)));
    scheme = brightest > 0.5 ? 'light' : 'dark';
  }

  return { scheme, accent: spec.accent, bg: { mode, angle, stops: [...stops] } };
}

/**
 * Применяет тему к документу.
 *
 * Токены пишутся в инлайновый стиль корня, поэтому побеждают :root из
 * style.css и не требуют ни второго листа стилей, ни перезагрузки. Значения по
 * умолчанию остаются в CSS: если этот модуль почему-либо не отработает, мир
 * приложения выглядит ровно так, как выглядел всегда.
 */
export function applyTheme(spec: ThemeSpec): void {
  const tokens = deriveTokens(spec);
  const root = document.documentElement;
  for (const [name, value] of Object.entries(tokens)) {
    root.style.setProperty(name, value);
  }

  const report = describeTheme(spec);
  // Окно шире страницы: у безрамочного окна остаются резайз-гаттеры, и их
  // красит Wails своим цветом фона (main.go задаёт левый стоп). Без этой
  // строки на любой чужой теме по краям окна оставалась бы полоска старого
  // почти чёрного.
  const edge = parseHex(tokens['--bg-color']);
  const runtime = window.runtime;
  if (edge && runtime?.WindowSetBackgroundColour) {
    try {
      runtime.WindowSetBackgroundColour(Math.round(edge.r), Math.round(edge.g), Math.round(edge.b), 255);
    } catch {
      // Рантайм Wails ещё не подставлен — тема применится к странице, а
      // гаттеры догонят при следующем применении. Ронять из-за этого нечего.
    }
  }

  // Нативная обвязка WebView2 — полосы прокрутки и контекстное меню — своей
  // палитре не подчиняется и слушает только это.
  try {
    if (report.scheme === 'light') runtime?.WindowSetLightTheme?.();
    else runtime?.WindowSetDarkTheme?.();
  } catch {
    /* см. выше */
  }
}
