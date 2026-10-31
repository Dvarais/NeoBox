import { el, optionalEl, setText, setAria } from '../modules/dom';
import { iconSvg } from '../modules/icons';
import { formatBytes, formatConnectionAge } from '../modules/ui-utils';
import { groupConnections } from '../modules/grouping';
import type { ConnectionGroup, GroupSort } from '../modules/grouping';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import type { AppSettings, ConnectionRow, ConnectionsSnapshot, CustomRule } from '../modules/api';

const CONNECTIONS_POLL_MS = 1000;
const MAX_CONNECTION_ROWS = 200;

export type ConnectionsViewType = AppSettings['connectionsView'];

interface ConnectionRowHandles {
  tr: HTMLTableRowElement;
  traffic: HTMLTableCellElement;
  age: HTMLTableCellElement;
  lastTraffic: string;
  lastAge: string;
}

interface GroupHandles {
  root: HTMLElement;
  keyEl: HTMLElement;
  countEl: HTMLElement;
  trafficEl: HTMLElement;
  members: HTMLElement;
  lastCount: string;
  lastTraffic: string;
  lastMembers: string;
}

type GroupKind = 'site' | 'process';

let connectionsView: ConnectionsViewType = 'grouped';
let connectionsSort: GroupSort = 'name';
let connectionsTimer: ReturnType<typeof setInterval> | null = null;
let connectionsInFlight = false;
let lastConnectionsSnapshot: ConnectionsSnapshot | null = null;
let connectionsFlashTimer: ReturnType<typeof setTimeout> | null = null;

const connectionRowHandles = new Map<string, ConnectionRowHandles>();
const groupHandles = new Map<string, GroupHandles>();
const expandedGroups = new Set<string>();
const groupOrderSignature = new Map<GroupKind, string>();

let getLang: () => Language = () => 'RU';
let getAppStateFn: () => 'on' | 'off' | 'connecting' = () => 'off';
let isViewActiveFn: () => boolean = () => false;
let isUiActiveFn: () => boolean = () => true;
let upsertRuleFn: (rule: CustomRule) => 'unchanged' | 'replaced' | 'added' = () => 'added';
let onSaveViewFn: (view: ConnectionsViewType) => void = () => {};

function connectionsShouldPoll(): boolean {
  return isViewActiveFn() && getAppStateFn() === 'on' && isUiActiveFn();
}

function processRulesAvailable(): boolean {
  const box = optionalEl<HTMLInputElement>('tunModeCheckbox');
  return box ? box.checked : false;
}

function groupRuleTarget(kind: GroupKind, key: string): CustomRule {
  if (kind === 'site') return { action: 'direct', type: 'domain_suffix', value: key };
  return { action: 'direct', type: 'process', value: key };
}

export function flashConnectionsStatus(message: string): void {
  const node = optionalEl('connectionsFlash');
  if (!node) return;
  node.textContent = message;
  node.style.display = 'inline';
  if (connectionsFlashTimer !== null) clearTimeout(connectionsFlashTimer);
  connectionsFlashTimer = setTimeout(() => {
    node.style.display = 'none';
    connectionsFlashTimer = null;
  }, 2000);
}

function applyRule(rule: CustomRule): void {
  const t = translations[getLang()];
  const outcome = upsertRuleFn(rule);
  flashConnectionsStatus(
    outcome === 'unchanged'
      ? t.ruleAlreadyExists
      : outcome === 'replaced'
      ? t.ruleReplaced
      : t.ruleAdded
  );
}

function buildRuleActions(
  base: CustomRule,
  disabledReason: string | null,
  compact: boolean
): HTMLElement {
  const t = translations[getLang()];
  const wrap = document.createElement('div');
  wrap.className = compact ? 'conn-group-actions conn-group-actions--compact' : 'conn-group-actions';

  const add = (action: CustomRule['action'], className: string, label: string) => {
    const btn = document.createElement('button');
    btn.type = 'button';
    btn.className = `conn-action ${className}`;
    btn.textContent = label;
    if (disabledReason) {
      btn.disabled = true;
      btn.title = disabledReason;
    } else {
      btn.title = `${label}: ${base.value}`;
      btn.onclick = (e) => {
        e.stopPropagation();
        applyRule({ ...base, action });
      };
    }
    wrap.appendChild(btn);
  };

  add('direct', 'conn-action-direct', t.connActionDirect);
  add('proxy', 'conn-action-proxy', t.connActionProxy);
  add('block', 'conn-action-block', t.connActionBlock);
  return wrap;
}

function setGroupExpanded(handles: GroupHandles, id: string, expanded: boolean): void {
  const t = translations[getLang()];
  handles.root.dataset.expanded = expanded ? 'true' : 'false';
  handles.members.style.display = expanded ? 'flex' : 'none';
  const toggle = handles.root.querySelector<HTMLButtonElement>('.conn-group-toggle');
  if (toggle) {
    toggle.setAttribute('aria-expanded', expanded ? 'true' : 'false');
    toggle.title = expanded ? t.connGroupCollapse : t.connGroupExpand;
  }
  if (expanded) expandedGroups.add(id);
  else expandedGroups.delete(id);
}

function buildGroup(kind: GroupKind, id: string, group: ConnectionGroup): GroupHandles {
  const t = translations[getLang()];

  const root = document.createElement('div');
  root.className = 'conn-group';
  root.dataset.expanded = 'false';

  const head = document.createElement('div');
  head.className = 'conn-group-head';

  const toggle = document.createElement('button');
  toggle.type = 'button';
  toggle.className = 'conn-group-toggle';
  toggle.setAttribute('aria-expanded', 'false');
  toggle.title = t.connGroupExpand;

  const chevron = document.createElement('span');
  chevron.className = 'conn-group-chevron';
  chevron.setAttribute('aria-hidden', 'true');
  chevron.innerHTML = `<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round"><polyline points="9 18 15 12 9 6"/></svg>`;

  const keyEl = document.createElement('span');
  keyEl.className = 'conn-group-key';
  keyEl.textContent = group.key;

  const countEl = document.createElement('span');
  countEl.className = 'conn-group-count';

  const trafficEl = document.createElement('span');
  trafficEl.className = 'conn-group-traffic';

  toggle.append(chevron, keyEl, countEl, trafficEl);

  const members = document.createElement('div');
  members.className = 'conn-group-members';
  members.style.display = 'none';
  const membersId = `conn-members-${kind}-${group.key.replace(/[^\w.-]/g, '_')}`;
  members.id = membersId;
  toggle.setAttribute('aria-controls', membersId);

  const handles: GroupHandles = {
    root,
    keyEl,
    countEl,
    trafficEl,
    members,
    lastCount: '',
    lastTraffic: '',
    lastMembers: '',
  };

  toggle.onclick = () => setGroupExpanded(handles, id, root.dataset.expanded !== 'true');

  head.appendChild(toggle);
  const base = groupRuleTarget(kind, group.key);
  if (base) {
    const blocked = kind === 'process' && !processRulesAvailable();
    head.appendChild(buildRuleActions(base, blocked ? t.connProcessRuleNeedsTun : null, false));
  }

  root.append(head, members);
  return handles;
}

function renderGroupMembers(handles: GroupHandles, group: ConnectionGroup, now: number): void {
  const t = translations[getLang()];
  const signature = group.rows.map((r) => `${r.id}|${r.upload}|${r.download}`).join(';');
  if (signature === handles.lastMembers) return;
  handles.lastMembers = signature;

  handles.members.textContent = '';
  for (const row of group.rows) {
    const item = document.createElement('div');
    item.className = 'conn-member';

    const host = document.createElement('span');
    host.className = 'conn-member-host';
    const label = row.host || row.destIP || t.connNoHost;
    host.textContent = row.process && row.host ? `${label} — ${row.process}` : label;
    host.title = `${label}${row.destIP ? ` · ${row.destIP}` : ''}`;

    const meta = document.createElement('span');
    meta.className = 'conn-member-meta';
    meta.textContent = `↑ ${formatBytes(row.upload)}  ↓ ${formatBytes(row.download)}  ·  ${formatConnectionAge(
      now - row.startMs
    )}`;

    item.append(host, meta);

    if (row.ruleType) {
      item.appendChild(
        buildRuleActions(
          { action: 'direct', type: row.ruleType as CustomRule['type'], value: row.ruleValue },
          null,
          true
        )
      );
    }

    handles.members.appendChild(item);
  }
}

function renderGroupSection(
  kind: GroupKind,
  listId: string,
  emptyId: string,
  groups: ConnectionGroup[],
  now: number
): void {
  const list = optionalEl(listId);
  const empty = optionalEl(emptyId);
  if (!list) return;

  if (empty) empty.style.display = groups.length === 0 ? 'block' : 'none';

  const seen = new Set<string>();
  for (const group of groups) {
    const id = `${kind}:${group.key}`;
    seen.add(id);

    let handles = groupHandles.get(id);
    if (!handles) {
      handles = buildGroup(kind, id, group);
      groupHandles.set(id, handles);
      list.appendChild(handles.root);
      if (expandedGroups.has(id)) setGroupExpanded(handles, id, true);
    }

    const count = translations[getLang()].connGroupCount.replace('{n}', String(group.rows.length));
    if (handles.lastCount !== count) {
      handles.countEl.textContent = count;
      handles.lastCount = count;
    }
    const traffic = `↑ ${formatBytes(group.upload)}  ↓ ${formatBytes(group.download)}`;
    if (handles.lastTraffic !== traffic) {
      handles.trafficEl.textContent = traffic;
      handles.lastTraffic = traffic;
    }

    if (handles.root.dataset.expanded === 'true') {
      renderGroupMembers(handles, group, now);
    } else if (handles.lastMembers !== '') {
      handles.members.textContent = '';
      handles.lastMembers = '';
    }
  }

  for (const [id, handles] of groupHandles) {
    if (!id.startsWith(`${kind}:`) || seen.has(id)) continue;
    handles.root.remove();
    groupHandles.delete(id);
  }

  const signature = groups.map((g) => g.key).join(' ');
  if (groupOrderSignature.get(kind) !== signature) {
    groupOrderSignature.set(kind, signature);
    for (const group of groups) {
      const handles = groupHandles.get(`${kind}:${group.key}`);
      if (handles) list.appendChild(handles.root);
    }
  }
}

function renderConnectionsGrouped(rows: ConnectionRow[]): void {
  const now = Date.now();
  const { sites, programs } = groupConnections(rows, connectionsSort);
  renderGroupSection('site', 'connSitesList', 'connSitesEmpty', sites, now);
  renderGroupSection('process', 'connProgramsList', 'connProgramsEmpty', programs, now);
}

function renderConnectionsPlaceholder(shown: number, total: number): void {
  const t = translations[getLang()];

  const empty = optionalEl('connectionsEmpty');
  if (empty) {
    empty.textContent = getAppStateFn() === 'on' ? t.connectionsEmpty : t.connectionsOffline;
    empty.style.display = shown === 0 ? 'flex' : 'none';
  }

  const count = optionalEl('connectionsCount');
  if (count) {
    count.textContent =
      total > shown
        ? t.connectionsShowing.replace('{shown}', String(shown)).replace('{total}', String(total))
        : '';
  }
}

function buildConnectionRow(row: ConnectionRow, traffic: string, age: string): ConnectionRowHandles {
  const t = translations[getLang()];
  const tr = document.createElement('tr');
  tr.className = 'conn-row';

  const cell = (className: string, text: string, title?: string) => {
    const td = document.createElement('td');
    td.className = className;
    td.textContent = text;
    if (title) td.title = title;
    tr.appendChild(td);
    return td;
  };

  cell('conn-cell-host', row.host || t.connNoHost, row.host);
  cell('conn-cell-dest', row.destPort ? `${row.destIP}:${row.destPort}` : row.destIP);
  cell('conn-cell-process', row.process, row.process);
  cell(
    'conn-cell-rule',
    row.outbound ? `${row.rule} → ${row.outbound}` : row.rule,
    row.outbound ? `${row.rule} → ${row.outbound}` : row.rule
  );
  const trafficCell = cell('conn-cell-traffic', traffic);
  const ageCell = cell('conn-cell-age', age);

  const actions = document.createElement('td');
  actions.className = 'conn-cell-actions';

  const ruleAction = (action: CustomRule['action'], className: string, label: string) => {
    const btn = document.createElement('button');
    btn.className = `conn-action ${className}`;
    btn.textContent = label;
    if (row.ruleType === '') {
      btn.disabled = true;
      btn.title = t.connActionUnavailable;
    } else {
      btn.title = `${label}: ${row.ruleValue}`;
      btn.onclick = () => {
        const outcome = upsertRuleFn({ action, type: row.ruleType as CustomRule['type'], value: row.ruleValue });
        flashConnectionsStatus(
          outcome === 'unchanged'
            ? t.ruleAlreadyExists
            : outcome === 'replaced'
            ? t.ruleReplaced
            : t.ruleAdded
        );
      };
    }
    actions.appendChild(btn);
  };

  ruleAction('direct', 'conn-action-direct', t.connActionDirect);
  ruleAction('proxy', 'conn-action-proxy', t.connActionProxy);
  ruleAction('block', 'conn-action-block', t.connActionBlock);

  const closeBtn = document.createElement('button');
  closeBtn.className = 'conn-action conn-action-close';
  closeBtn.innerHTML = iconSvg('x', 13);
  closeBtn.title = t.connActionClose;
  closeBtn.onclick = () => {
    tr.remove();
    connectionRowHandles.delete(row.id);
    window.api.closeConnection(row.id).catch((e) => {
      console.error(`closeConnection ${row.id} failed:`, e);
    });
  };
  actions.appendChild(closeBtn);

  tr.appendChild(actions);
  return { tr, traffic: trafficCell, age: ageCell, lastTraffic: traffic, lastAge: age };
}

export function renderConnections(snapshot: ConnectionsSnapshot): void {
  lastConnectionsSnapshot = snapshot;
  const rowsAll = (snapshot.connections ?? []).slice(0, MAX_CONNECTION_ROWS);
  if (connectionsView === 'grouped') {
    renderConnectionsGrouped(rowsAll);
    renderConnectionsPlaceholder(rowsAll.length, snapshot.total ?? rowsAll.length);
    return;
  }

  const body = optionalEl<HTMLTableSectionElement>('connectionsBody');
  if (!body) return;

  const rows = (snapshot.connections ?? []).slice(0, MAX_CONNECTION_ROWS);
  const seen = new Set<string>();
  const arriving = document.createDocumentFragment();
  const now = Date.now();

  for (const row of rows) {
    seen.add(row.id);
    const traffic = `↑ ${formatBytes(row.upload)}  ↓ ${formatBytes(row.download)}`;
    const age = formatConnectionAge(now - row.startMs);

    const known = connectionRowHandles.get(row.id);
    if (known) {
      if (known.lastTraffic !== traffic) {
        known.traffic.textContent = traffic;
        known.lastTraffic = traffic;
      }
      if (known.lastAge !== age) {
        known.age.textContent = age;
        known.lastAge = age;
      }
      continue;
    }

    const handles = buildConnectionRow(row, traffic, age);
    connectionRowHandles.set(row.id, handles);
    arriving.appendChild(handles.tr);
  }

  for (const [id, handles] of connectionRowHandles) {
    if (seen.has(id)) continue;
    handles.tr.remove();
    connectionRowHandles.delete(id);
  }

  if (arriving.childNodes.length > 0) body.prepend(arriving);
  renderConnectionsPlaceholder(rows.length, snapshot.total ?? rows.length);
}

export async function pollConnections(): Promise<void> {
  if (connectionsInFlight || !connectionsShouldPoll()) return;
  connectionsInFlight = true;
  try {
    const snapshot = await window.api.getConnections();
    if (!connectionsShouldPoll()) return;
    renderConnections(snapshot);
  } catch (e) {
    console.error('getConnections failed:', e);
  } finally {
    connectionsInFlight = false;
  }
}

export function syncConnectionsPolling(): void {
  if (connectionsShouldPoll()) {
    if (connectionsTimer !== null) return;
    void pollConnections();
    connectionsTimer = setInterval(() => void pollConnections(), CONNECTIONS_POLL_MS);
    return;
  }

  if (connectionsTimer !== null) {
    clearInterval(connectionsTimer);
    connectionsTimer = null;
  }
  if (!isViewActiveFn() || getAppStateFn() !== 'on') resetConnectionsView();
}

export function resetConnectionsView(): void {
  const body = optionalEl('connectionsBody');
  if (body) body.innerHTML = '';
  connectionRowHandles.clear();

  for (const [, handles] of groupHandles) handles.root.remove();
  groupHandles.clear();
  groupOrderSignature.clear();

  renderConnectionsPlaceholder(0, 0);
}

export function getConnectionsView(): ConnectionsViewType {
  return connectionsView;
}

export function setConnectionsView(view: ConnectionsViewType, persist = true): void {
  connectionsView = view;

  const grouped = optionalEl('connectionsGrouped');
  const flat = optionalEl('connectionsFlatWrap');
  if (grouped) grouped.style.display = view === 'grouped' ? 'block' : 'none';
  if (flat) flat.style.display = view === 'flat' ? 'block' : 'none';

  document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-view]').forEach((btn) => {
    const active = btn.dataset.view === view;
    btn.classList.toggle('active', active);
    btn.setAttribute('aria-pressed', active ? 'true' : 'false');
  });

  if (lastConnectionsSnapshot && getAppStateFn() === 'on') {
    renderConnections(lastConnectionsSnapshot);
  }

  if (persist) onSaveViewFn(view);
}

export function setConnectionsSort(sort: GroupSort): void {
  connectionsSort = sort;
  document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-sort]').forEach((btn) => {
    const active = btn.dataset.sort === sort;
    btn.classList.toggle('active', active);
    btn.setAttribute('aria-pressed', active ? 'true' : 'false');
  });
  if (lastConnectionsSnapshot) renderConnections(lastConnectionsSnapshot);
}

export function applyConnectionsLanguage(t: Translations): void {
  setText('connectionsTitle', t.connectionsTitle);
  setText('connectionsDesc', t.connectionsDesc);
  setText('connColHost', t.connColHost);
  setText('connColDest', t.connColDest);
  setText('connColProcess', t.connColProcess);
  setText('connColRule', t.connColRule);
  setText('connColTraffic', t.connColTraffic);
  setText('connColAge', t.connColAge);
  setText('connColActions', t.connColActions);
  setText('connGroupSitesTitle', t.connGroupSites);
  setText('connGroupProgramsTitle', t.connGroupPrograms);
  setText('connSitesEmpty', t.connGroupSitesEmpty);
  setText('connProgramsEmpty', processRulesAvailable() ? t.connGroupProgramsEmpty : t.connGroupProgramsNeedTun);
  setText('connViewGrouped', t.connViewGrouped);
  setText('connViewFlat', t.connViewFlat);
  setText('connSortName', t.connSortName);
  setText('connSortTraffic', t.connSortTraffic);

  setAria('connViewSwitch', t.connViewLabel);
  setAria('connSortSwitch', t.connSortLabel);
  setAria('connTable', t.connTableLabel);

  resetConnectionsView();
  if (lastConnectionsSnapshot && getAppStateFn() === 'on') {
    renderConnections(lastConnectionsSnapshot);
  } else {
    renderConnectionsPlaceholder(0, 0);
  }
}

let connectionsInitialized = false;

export function initConnectionsView(options: {
  getCurrentLanguage: () => Language;
  getAppState: () => 'on' | 'off' | 'connecting';
  isViewActive: () => boolean;
  isUiActive: () => boolean;
  upsertCustomRule: (rule: CustomRule) => 'unchanged' | 'replaced' | 'added';
  onSaveView: (view: ConnectionsViewType) => void;
  initialView?: ConnectionsViewType;
}): void {
  getLang = options.getCurrentLanguage;
  getAppStateFn = options.getAppState;
  isViewActiveFn = options.isViewActive;
  isUiActiveFn = options.isUiActive;
  upsertRuleFn = options.upsertCustomRule;
  onSaveViewFn = options.onSaveView;

  if (options.initialView) {
    setConnectionsView(options.initialView, false);
  }

  if (connectionsInitialized) return;
  connectionsInitialized = true;

  document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-view]').forEach((btn) => {
    btn.addEventListener('click', () => {
      setConnectionsView(btn.dataset.view === 'flat' ? 'flat' : 'grouped');
    });
  });

  document.querySelectorAll<HTMLButtonElement>('.conn-view-btn[data-sort]').forEach((btn) => {
    btn.addEventListener('click', () => {
      setConnectionsSort(btn.dataset.sort === 'traffic' ? 'traffic' : 'name');
    });
  });
}
