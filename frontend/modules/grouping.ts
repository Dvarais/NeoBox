import type { ConnectionRow } from './api';

// Сведение живых соединений в две группировки: по сайту и по программе.
//
// Одно соединение попадает в обе. Это не дублирование данных, а два среза
// одного и того же: «какие сайты сейчас идут» и «какие программы сейчас
// ходят». Соединение chrome.exe → youtube.com видно и там, и там, и суммы
// трафика в двух срезах поэтому совпадают — так и задумано.
//
// Соединение без имени хоста (голый IP, часть QUIC) в срез сайтов не попадает:
// ключа для него нет. Соединение без процесса не попадает в срез программ —
// вне TUN-режима ядро владельца сокета обычно не видит, и там этот срез будет
// почти пуст.

/**
 * Составные окончания, у которых регистрируемое имя занимает три уровня, а не
 * два. Полный Public Suffix List сюда не тянется: это несколько сотен
 * килобайт ради вкладки с живыми соединениями. Список покрывает то, что
 * реально встречается, а промах даёт не поломку, а более дробную группировку —
 * `bbc.co.uk` без записи `co.uk` сгруппировался бы как `co.uk`, что было бы
 * заметно и неверно, поэтому распространённые записи здесь есть.
 */
const MULTIPART_SUFFIXES = new Set([
  'co.uk', 'org.uk', 'ac.uk', 'gov.uk', 'me.uk', 'net.uk',
  'com.au', 'net.au', 'org.au', 'edu.au', 'gov.au',
  'co.jp', 'or.jp', 'ne.jp', 'ac.jp', 'go.jp',
  'com.br', 'net.br', 'org.br', 'gov.br',
  'com.cn', 'net.cn', 'org.cn', 'gov.cn', 'edu.cn',
  'com.tr', 'net.tr', 'org.tr', 'gov.tr',
  'com.ua', 'net.ua', 'org.ua', 'in.ua', 'kiev.ua',
  'com.pl', 'net.pl', 'org.pl',
  'co.il', 'org.il', 'net.il',
  'co.kr', 'or.kr', 'ne.kr',
  'co.in', 'net.in', 'org.in',
  'com.mx', 'com.ar', 'com.co', 'com.sg', 'com.hk', 'com.tw', 'com.my',
  'co.za', 'co.nz', 'co.th', 'com.vn', 'com.ph',
  'net.ru', 'org.ru', 'com.ru', 'pp.ru', 'edu.ru', 'gov.ru', 'ac.ru',
  'com.es', 'com.pt', 'com.de', 'co.at', 'co.id',
  'github.io', 'gitlab.io', 'pages.dev', 'workers.dev', 'vercel.app', 'netlify.app',
  'amazonaws.com', 'cloudfront.net', 'azurewebsites.net', 'herokuapp.com',
]);

/**
 * Регистрируемое имя домена: то, что пользователь считает «одним сайтом».
 *
 * `rr1---sn-ab.googlevideo.com` и `rr7---sn-cd.googlevideo.com` дают
 * `googlevideo.com`; `api.example.com` и `cdn.example.com` — `example.com`.
 * Именно оно становится значением правила `domain_suffix`, когда пользователь
 * жмёт действие на группе, поэтому дробить мельче нельзя: правило по
 * `rr1---sn-ab.googlevideo.com` не покрыло бы соседние узлы и выглядело бы
 * неработающим.
 *
 * Возвращает пустую строку, если из хоста ключ не выводится: пусто, IP-литерал
 * или одна метка.
 */
function siteKey(host: string): string {
  const h = host.trim().toLowerCase().replace(/\.+$/, '');
  if (!h || h.includes(':') || h.includes('/')) return '';
  // IPv4-литерал доменом не является.
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(h)) return '';

  const parts = h.split('.').filter(Boolean);
  if (parts.length < 2) return '';

  const lastTwo = parts.slice(-2).join('.');
  if (parts.length >= 3 && MULTIPART_SUFFIXES.has(lastTwo)) {
    return parts.slice(-3).join('.');
  }
  return lastTwo;
}

/** Одна папка: ключ, входящие соединения и суммарный трафик. */
export interface ConnectionGroup {
  key: string;
  rows: ConnectionRow[];
  upload: number;
  download: number;
}

export interface GroupedConnections {
  sites: ConnectionGroup[];
  programs: ConnectionGroup[];
}

function push(map: Map<string, ConnectionGroup>, key: string, row: ConnectionRow): void {
  let group = map.get(key);
  if (!group) {
    group = { key, rows: [], upload: 0, download: 0 };
    map.set(key, group);
  }
  group.rows.push(row);
  group.upload += row.upload;
  group.download += row.download;
}

/** Чем упорядочены папки. */
export type GroupSort = 'name' | 'traffic';

/**
 * Раскладывает снимок по двум срезам.
 *
 * Порядок групп по умолчанию алфавитный, и это осознанно. Сортировка по
 * трафику переставляла бы папки на каждом тике, а по этому экрану не только
 * смотрят, им ещё и пользуются: чтобы отправить программу мимо VPN, её надо
 * сначала поймать курсором.
 *
 * Но вопрос «кто сейчас забирает канал» на алфавитном списке не читается
 * вовсе, а он и есть причина, по которой на этот экран заходят чаще всего.
 * Поэтому порядок стал выбором: пока человек наблюдает, папки едут по трафику;
 * как только он собрался нажимать — возвращает алфавит. Умолчание осталось
 * прежним, потому что промахнуться мимо кнопки хуже, чем не увидеть лидера
 * сразу.
 *
 * При равном трафике порядок доопределяется именем: иначе две молчащие папки
 * менялись бы местами от тика к тику на ровном месте.
 */
export function groupConnections(rows: ConnectionRow[], sort: GroupSort = 'name'): GroupedConnections {
  const sites = new Map<string, ConnectionGroup>();
  const programs = new Map<string, ConnectionGroup>();

  for (const row of rows) {
    const site = siteKey(row.host || '');
    if (site) push(sites, site, row);

    const process = (row.process || '').trim();
    if (process) push(programs, process, row);
  }

  const byKey = (a: ConnectionGroup, b: ConnectionGroup) => a.key.localeCompare(b.key);
  const byTraffic = (a: ConnectionGroup, b: ConnectionGroup) => {
    const diff = (b.upload + b.download) - (a.upload + a.download);
    return diff !== 0 ? diff : byKey(a, b);
  };
  const order = sort === 'traffic' ? byTraffic : byKey;

  return {
    sites: [...sites.values()].sort(order),
    programs: [...programs.values()].sort(order),
  };
}
