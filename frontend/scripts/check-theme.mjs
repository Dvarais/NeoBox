// Проверка движка тем: контраст на нескольких тысячах случайных палитр.
//
// Зачем отдельный прогон, а не обычный тест. Обязательство продукта —
// «любой текст читается с коэффициентом не ниже 4.5:1» (PRODUCT.md,
// «Доступность») — раньше держалось замерами вручную и комментариями в
// style.css. С того момента, как палитру задаёт пользователь, вручную его
// удержать невозможно: тем бесконечно много. Проверить можно только прогоном.
//
// Тест-раннера у фронтенда нет и заводить его ради одного файла незачем:
// modules/theme.ts — чистые функции без DOM, поэтому достаточно скомпилировать
// их tsc во временный каталог и вызвать из node. Запуск: npm run check:theme
//
// Прогон возвращает ненулевой код, если хотя бы одна тема провалила порог, —
// то есть годится и как шаг сборки, и как ручная проверка после правки движка.

import { execFileSync } from 'node:child_process';
import { existsSync, mkdtempSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { pathToFileURL } from 'node:url';

const AA = 4.5;
const THEME_COUNT = 4000;

const out = mkdtempSync(join(tmpdir(), 'neobox-theme-'));

try {
  // Прямо через node, а не через npx: на Windows запуск .cmd из execFile
  // блокируется начиная с Node 18.20, и вызов молча падал бы ещё до tsc.
  execFileSync(
    process.execPath,
    [
      join('node_modules', 'typescript', 'bin', 'tsc'),
      'modules/color.ts', 'modules/theme.ts',
      // tsconfig.json проверяет весь проект и отказывается работать, если ему
      // передать файлы списком; здесь нужен только этот перевод.
      '--ignoreConfig',
      '--outDir', out,
      '--module', 'esnext', '--target', 'es2020',
      '--moduleResolution', 'bundler', '--strict', '--skipLibCheck',
      '--lib', 'ES2020,DOM',
    ],
    { stdio: ['ignore', 'ignore', 'ignore'] },
  );
} catch {
  // tsc ругается на window.runtime: globals.d.ts в этот перевод не входит.
  // На выпущенные файлы это не влияет, а типы целиком проверяет
  // npm run typecheck — здесь важен только исполнимый результат, и его
  // отсутствие поймает чтение ниже.
}

// Node требует расширение в пути импорта, tsc его не дописывает.
const themePath = join(out, 'theme.js');
if (!existsSync(themePath)) {
  rmSync(out, { recursive: true, force: true });
  console.error('tsc не выпустил modules/theme.js — прогон невозможен.');
  process.exit(1);
}
writeFileSync(themePath, readFileSync(themePath, 'utf8').replace("from './color'", "from './color.js'"));

const { PRESETS, DEFAULT_THEME, describeTheme, deriveTokens, parseTheme } =
  await import(pathToFileURL(themePath).href);
const { parseHex, contrast, luminance } = await import(pathToFileURL(join(out, 'color.js')).href);

// Детерминированный генератор: провалившийся прогон должен воспроизводиться
// с точностью до темы, иначе чинить его нечем.
let seed = 12345;
const rand = () => ((seed = (seed * 1103515245 + 12345) & 0x7fffffff) / 0x7fffffff);
const randHex = () =>
  '#' + Array.from({ length: 3 }, () => Math.floor(rand() * 256).toString(16).padStart(2, '0')).join('');

const specs = [
  ...PRESETS.map((p) => p.spec),
  // Края, до которых случайные темы доходят редко.
  { scheme: 'light', accent: '#ffffff', bg: { mode: 'solid', angle: 90, stops: ['#ffffff'] } },
  { scheme: 'dark', accent: '#000000', bg: { mode: 'solid', angle: 90, stops: ['#000000'] } },
  { scheme: 'dark', accent: '#ff0000', bg: { mode: 'linear', angle: 45, stops: ['#000000', '#ffffff'] } },
  { scheme: 'light', accent: '#ff0000', bg: { mode: 'linear', angle: 45, stops: ['#000000', '#ffffff'] } },
  { scheme: 'dark', accent: '#00ff00', bg: { mode: 'radial', angle: 90, stops: ['#808080', '#7f7f7f', '#818181'] } },
  { scheme: 'light', accent: '#0000ff', bg: { mode: 'linear', angle: 0, stops: ['#ff00ff', '#00ffff', '#ffff00'] } },
];
for (let i = 0; i < THEME_COUNT; i++) {
  specs.push({
    // Оба режима проверяются на одних и тех же цветах: это и есть суть режима —
    // один и тот же выбор человека приводится к разным рядам.
    scheme: rand() < 0.5 ? 'dark' : 'light',
    accent: randHex(),
    bg: {
      mode: ['solid', 'linear', 'radial'][Math.floor(rand() * 3)],
      angle: Math.floor(rand() * 360),
      stops: Array.from({ length: 1 + Math.floor(rand() * 3) }, randHex),
    },
  });
}

const failures = [];
let worst = Infinity;

for (const spec of specs) {
  const report = describeTheme(spec);
  const min = Math.min(report.textContrast, report.accentContrast, report.signalContrast);
  worst = Math.min(worst, min);

  // Режим — выбор человека, и движок не имеет права его пересматривать.
  // Отдельная проверка, потому что раньше он именно это и делал: определял
  // режим по цвету фона и уводил приложение в белое от выбора синего.
  if (report.scheme !== spec.scheme) failures.push({ spec, min, kind: 'scheme' });

  // Тёмный режим обязан остаться тёмным на любом цвете, светлый — светлым.
  // Это то, что человек видит, и никакой контраст не отменяет обещания.
  const bg = deriveTokens(spec)['--bg-color'];
  const bgLum = luminance(parseHex(bg));
  if (spec.scheme === 'dark' && bgLum > 0.12) failures.push({ spec, min: bgLum, kind: 'фон светлый в тёмном режиме' });
  if (spec.scheme === 'light' && bgLum < 0.5) failures.push({ spec, min: bgLum, kind: 'фон тёмный в светлом режиме' });
  // Полсотой допуска — округление каналов до целых при выводе в CSS.
  if (min < AA - 0.005) failures.push({ spec, min, kind: 'contrast' });

  // Отчёт не имеет права расходиться с фактом: интерфейс показывает его
  // человеку как утверждение о том, что порог взят.
  if (report.passesAA !== min >= AA - 0.005) failures.push({ spec, min, kind: 'report' });

  // Текст на сплошном акценте — кнопки действия, выделение.
  const tokens = deriveTokens(spec);
  const [r, g, b] = tokens['--accent-rgb'].split(' ').map(Number);
  if (contrast(parseHex(tokens['--on-accent']), { r, g, b }) < AA) {
    failures.push({ spec, min, kind: 'on-accent' });
  }
}

// Стандартная тема обязана выйти из движка собой: она и есть обязательство
// бренда, и движок не имеет права её переписывать.
// Темы, сохранённые до появления поля scheme, должны читаться и достраиваться,
// а не пропадать: settings.json переживает обновление приложения.
const legacy = parseTheme({ accent: '#38bdf8', bg: { mode: 'linear', angle: 90, stops: ['#080909', '#2b0d33', '#1a1748'] } });
if (!legacy || legacy.scheme !== 'dark') {
  failures.push({ spec: 'старая тёмная тема без scheme', min: 0, kind: 'совместимость' });
}
const legacyLight = parseTheme({ accent: '#0369a1', bg: { mode: 'solid', angle: 90, stops: ['#f3f4f6'] } });
if (!legacyLight || legacyLight.scheme !== 'light') {
  failures.push({ spec: 'старая светлая тема без scheme', min: 0, kind: 'совместимость' });
}

const shipped = {
  '--grad-start': '#080909',
  '--grad-mid': '#2b0d33',
  '--grad-end': '#1a1748',
  '--accent-rgb': '56 189 248',
  '--text-main': '#f1f5f9',
  '--text-dim': '#94a3b8',
  '--text-faint': '#939fb3',
};
const defaults = deriveTokens(DEFAULT_THEME);
const drift = [];
for (const [name, want] of Object.entries(shipped)) {
  const channels = (v) => (v.startsWith('#') ? Object.values(parseHex(v)) : v.split(' ').map(Number));
  const [a, b2] = [channels(want), channels(defaults[name])];
  const delta = Math.max(...a.map((v, i) => Math.abs(v - b2[i])));
  if (delta > 0) drift.push(`${name}: ${want} → ${defaults[name]} (Δ${delta})`);
}

rmSync(out, { recursive: true, force: true });

console.log(`Проверено тем: ${specs.length}`);
console.log(`Худший контраст: ${worst.toFixed(2)}:1 (порог ${AA})`);
if (drift.length > 0) {
  console.error('\nСтандартная тема разошлась со style.css:');
  for (const line of drift) console.error(`  ${line}`);
}
if (failures.length > 0) {
  console.error(`\nПровалов: ${failures.length}`);
  for (const f of failures.slice(0, 10)) {
    console.error(`  [${f.kind}] ${f.min.toFixed(2)}:1  ${JSON.stringify(f.spec)}`);
  }
}

if (failures.length > 0 || drift.length > 0) process.exit(1);
console.log('Все роли держат порог AA на каждой теме.');
