import { optionalEl, setText } from '../modules/dom';
import { errorText, showConfirm, showPrompt } from '../modules/ui-utils';
import { translations } from '../modules/translations';
import type { Language, Translations } from '../modules/translations';
import type { Profile, ProfileSettings } from '../modules/api';
import { flashConnectionsStatus } from './connections.view';
import { currentDnsSetting, setDnsSetting } from './settings.view';
import {
  getCustomRules,
  getProcessListBlacklist,
  getProcessListWhitelist,
  getProcessMode,
  setCustomRules,
  setProcessListBlacklist,
  setProcessListWhitelist,
  setProcessMode,
} from './routing.view';

export interface ProfilesViewOptions {
  getCurrentLanguage: () => Language;
  isCoreActive: () => boolean;
  restartCore: () => void;
  onSaveSettings: () => Promise<unknown> | unknown;
  getActiveServerLink: () => string;
  onSelectServer?: (serverLink: string) => void;
  onRefreshKillSwitch?: () => void;
  onUpdateInterceptionGuidance?: () => void;
  showAlert?: (title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void;
}

let getLang: () => Language = () => 'RU';
let isCoreActiveFn: () => boolean = () => false;
let restartCoreFn: () => void = () => {};
let onSaveSettingsFn: () => Promise<unknown> | unknown = () => {};
let getActiveServerLinkFn: () => string = () => '';
let onSelectServerFn: ((serverLink: string) => void) | null = null;
let onRefreshKillSwitchFn: () => void = () => {};
let onUpdateInterceptionGuidanceFn: () => void = () => {};
let showAlertFn: ((title: string, message: string, isError: boolean, t: Translations) => Promise<unknown> | void) | null = null;

let profiles: Profile[] = [];
let isInitialized = false;

export function getProfiles(): Profile[] {
  return profiles;
}

export function setProfiles(profs: Profile[]): void {
  profiles = Array.isArray(profs) ? profs : [];
  renderProfiles();
}

/** Снимок тех полей, которые профиль запоминает. */
export function currentProfileSettings(): ProfileSettings {
  return {
    dns: currentDnsSetting(),
    bypassRu: optionalEl<HTMLInputElement>('bypassRuCheckbox')?.checked ?? false,
    blockQuic: optionalEl<HTMLInputElement>('blockQuicCheckbox')?.checked ?? false,
    blockTorrent: optionalEl<HTMLInputElement>('blockTorrentCheckbox')?.checked ?? false,
    tunMode: optionalEl<HTMLInputElement>('tunModeCheckbox')?.checked ?? false,
    systemProxy: optionalEl<HTMLInputElement>('systemProxyCheckbox')?.checked ?? true,
    processMode: getProcessMode(),
    processListBlacklist: getProcessListBlacklist(),
    processListWhitelist: getProcessListWhitelist(),
    customRules: getCustomRules().map((r) => ({ ...r })),
    killSwitch: optionalEl<HTMLInputElement>('killSwitchCheckbox')?.checked ?? false,
    dnsLeak: optionalEl<HTMLInputElement>('dnsLeakCheckbox')?.checked ?? false,
    ipv6Leak: optionalEl<HTMLInputElement>('ipv6LeakCheckbox')?.checked ?? false,
    fakeDns: optionalEl<HTMLInputElement>('fakeDnsCheckbox')?.checked ?? false,
    tlsFragment: optionalEl<HTMLInputElement>('tlsFragmentCheckbox')?.checked ?? false,
    allowLan: optionalEl<HTMLInputElement>('allowLanCheckbox')?.checked ?? false,
  };
}

/**
 * Раскладывает профиль по элементам интерфейса.
 */
export function applyProfileSettings(p: ProfileSettings): void {
  setDnsSetting(p.dns);

  const bypassRu = optionalEl<HTMLInputElement>('bypassRuCheckbox');
  if (bypassRu) bypassRu.checked = p.bypassRu;

  const blockQuic = optionalEl<HTMLInputElement>('blockQuicCheckbox');
  if (blockQuic) blockQuic.checked = !!p.blockQuic;

  const blockTorrent = optionalEl<HTMLInputElement>('blockTorrentCheckbox');
  if (blockTorrent) blockTorrent.checked = !!p.blockTorrent;

  const tunMode = optionalEl<HTMLInputElement>('tunModeCheckbox');
  if (tunMode) tunMode.checked = p.tunMode;

  const sysProxy = optionalEl<HTMLInputElement>('systemProxyCheckbox');
  if (sysProxy) sysProxy.checked = p.systemProxy;

  setProcessListBlacklist(p.processListBlacklist);
  setProcessListWhitelist(p.processListWhitelist);
  setProcessMode(p.processMode);
  setCustomRules(p.customRules.map((r) => ({ ...r })));

  const killSwitch = optionalEl<HTMLInputElement>('killSwitchCheckbox');
  if (killSwitch) killSwitch.checked = p.killSwitch;

  const dnsLeak = optionalEl<HTMLInputElement>('dnsLeakCheckbox');
  if (dnsLeak) dnsLeak.checked = p.dnsLeak;

  const ipv6Leak = optionalEl<HTMLInputElement>('ipv6LeakCheckbox');
  if (ipv6Leak) ipv6Leak.checked = p.ipv6Leak;

  const fakeDns = optionalEl<HTMLInputElement>('fakeDnsCheckbox');
  if (fakeDns) fakeDns.checked = p.fakeDns;

  const tlsFragment = optionalEl<HTMLInputElement>('tlsFragmentCheckbox');
  if (tlsFragment) tlsFragment.checked = !!p.tlsFragment;

  const allowLan = optionalEl<HTMLInputElement>('allowLanCheckbox');
  if (allowLan) {
    allowLan.checked = !!p.allowLan;
    const btn = optionalEl('openLanSharingBtn');
    if (btn) btn.style.display = allowLan.checked ? 'inline-flex' : 'none';
  }

  onUpdateInterceptionGuidanceFn();
  onRefreshKillSwitchFn();
}

export function renderProfiles(): void {
  const t = translations[getLang()];
  const list = optionalEl('profilesList');
  const empty = optionalEl('profilesEmpty');
  if (!list) return;

  list.textContent = '';
  if (empty) empty.style.display = profiles.length === 0 ? 'block' : 'none';

  profiles.forEach((profile) => {
    const row = document.createElement('div');
    row.className = 'profile-row';

    const apply = document.createElement('button');
    apply.type = 'button';
    apply.className = 'btn-glass profile-apply';
    apply.textContent = profile.name;
    apply.title = profile.server ? t.profileAppliesServer : t.profileRoutingOnly;
    apply.onclick = () => void applyProfile(profile);

    const del = document.createElement('button');
    del.type = 'button';
    del.className = 'btn-glass profile-delete';
    del.textContent = t.deleteBtn;
    del.setAttribute('aria-label', `${t.deleteBtn}: ${profile.name}`);
    del.onclick = async () => {
      if (!(await showConfirm(t.profileDeleteConfirm.replace('{name}', profile.name)))) return;
      const prevProfiles = [...profiles];
      profiles = profiles.filter((p) => p.id !== profile.id);
      renderProfiles();
      try {
        await onSaveSettingsFn();
        await window.api.rebuildTrayProfiles();
      } catch (err) {
        console.error('Failed to delete profile:', err);
        profiles = prevProfiles;
        renderProfiles();
        if (showAlertFn) {
          const tNow = translations[getLang()];
          void showAlertFn(tNow.errorDialogTitle, errorText(err) || tNow.unknownError, true, tNow);
        }
      }
    };

    if (profile.server) {
      const mark = document.createElement('span');
      mark.className = 'profile-server-mark';
      mark.textContent = t.profileServerMark;
      row.append(apply, mark, del);
    } else {
      row.append(apply, del);
    }
    list.appendChild(row);
  });
}

export async function applyProfile(profile: Profile): Promise<void> {
  const t = translations[getLang()];
  try {
    applyProfileSettings(profile.settings);

    if (profile.server && onSelectServerFn) {
      onSelectServerFn(profile.server);
    }

    await onSaveSettingsFn();

    if (isCoreActiveFn()) {
      restartCoreFn();
    }
    flashConnectionsStatus(t.profileApplied.replace('{name}', profile.name));
  } catch (err) {
    console.error('Failed to apply profile:', err);
    if (showAlertFn) {
      void showAlertFn(t.errorDialogTitle, errorText(err) || t.unknownError, true, t);
    }
  }
}

export function applyProfilesLanguage(t: Translations): void {
  setText('profilesTitle', t.profilesTitle);
  setText('profilesDesc', t.profilesDesc);
  setText('profilesEmpty', t.profilesEmpty);
  setText('saveProfileBtnText', t.saveProfileBtn);
  setText('profileIncludeServerLabel', t.profileIncludeServerLabel);

  renderProfiles();
}

export function initProfilesView(options: ProfilesViewOptions): void {
  getLang = options.getCurrentLanguage;
  isCoreActiveFn = options.isCoreActive;
  restartCoreFn = options.restartCore;
  onSaveSettingsFn = options.onSaveSettings;
  getActiveServerLinkFn = options.getActiveServerLink;
  if (options.onSelectServer) onSelectServerFn = options.onSelectServer;
  if (options.onRefreshKillSwitch) onRefreshKillSwitchFn = options.onRefreshKillSwitch;
  if (options.onUpdateInterceptionGuidance) onUpdateInterceptionGuidanceFn = options.onUpdateInterceptionGuidance;
  if (options.showAlert) showAlertFn = options.showAlert;

  if (isInitialized) return;
  isInitialized = true;

  const saveProfileBtn = optionalEl('saveProfileBtn');
  if (saveProfileBtn) {
    saveProfileBtn.onclick = async () => {
      const t = translations[getLang()];
      const name = (await showPrompt(t.profileNamePrompt))?.trim();
      if (!name) return;

      const includeServer = optionalEl<HTMLInputElement>('profileIncludeServerCheckbox')?.checked ?? false;
      const profile: Profile = {
        id: Date.now().toString(),
        name,
        server: includeServer ? getActiveServerLinkFn() : null,
        settings: currentProfileSettings(),
      };

      const existing = profiles.findIndex((p) => p.name.toLowerCase() === name.toLowerCase());
      const prevProfiles = [...profiles];
      if (existing !== -1) {
        if (!(await showConfirm(t.profileOverwriteConfirm.replace('{name}', name)))) return;
        profile.id = profiles[existing].id;
        profiles[existing] = profile;
      } else {
        profiles.push(profile);
      }

      renderProfiles();
      try {
        await onSaveSettingsFn();
        await window.api.rebuildTrayProfiles();
        flashConnectionsStatus(t.profileSaved.replace('{name}', name));
      } catch (err) {
        console.error('Failed to save profile:', err);
        profiles = prevProfiles;
        renderProfiles();
        if (showAlertFn) {
          void showAlertFn(t.errorDialogTitle, errorText(err) || t.unknownError, true, t);
        }
      }
    };
  }

  window.api.onTrayProfileSelected((id: string) => {
    const profile = profiles.find((p) => p.id === id);
    if (profile) {
      applyProfile(profile).catch((err) => {
        console.error('onTrayProfileSelected applyProfile failed:', err);
      });
    }
  });
}
