// Import Wails bindings
import { 
  BringToFront,
  CheckAdmin, 
  CheckUpdates, 
  CheckTunStatus,
  DownloadAndInstallUpdate,
  FetchSubscription,
  GetAppVersion,
  GetSettings,
  GetSubscriptions, 
  ImportClipboard, 
  NotifyWindowHidden,
  NotifyWindowShown,
  OpenLogsFolder,
  PingServer, 
  RequestAdmin, 
  RestartXray,
  SaveLogs,
  SaveSettings, 
  SaveSubscriptions, 
  StartXray, 
  StopXray 
} from './wailsjs/go/service/AppService.js';

import { EventsOn } from './wailsjs/runtime/runtime.js';

// Global session bytes counters shared between modules
window.sessionBytesDown = 0;
window.sessionBytesUp = 0;

// Expose them as window.api to maintain total compatibility with original renderer.js!
window.api = {
  // Commands
  bringToFront: () => BringToFront(),
  checkTunStatus: () => CheckTunStatus(),
  startXray: async (link, useSystemProxy) => {
    const settings = await window.api.getSettings();
    const res = await StartXray(link, JSON.stringify(settings), useSystemProxy);
    if (res && !res.success && res.error === 'admin_required') {
      window.api.requestAdmin();
    }
    return res;
  },
  restartXray: async (link, useSystemProxy) => {
    const settings = await window.api.getSettings();
    const res = await RestartXray(link, JSON.stringify(settings), useSystemProxy);
    if (res && !res.success && res.error === 'admin_required') {
      window.api.requestAdmin();
    }
    return res;
  },
  stopXray: async () => {
    const res = await StopXray();
    const container = document.getElementById('speedometerContainer');
    if (container) {
      container.style.display = 'none';
    }
    const totalContainer = document.getElementById('speedometerTotalContainer');
    if (totalContainer) {
      totalContainer.style.display = 'none';
    }
    return res;
  },
  pingServer: async (link) => {
    const latency = await PingServer(link);
    // Ping result was sent as an IPC event in Electron. We invoke the callback directly!
    if (window.api._pingCallback) {
      window.api._pingCallback({ link, latency });
    }
  },
  
  // Version reported by the Go backend — the single source of truth for the
  // number shown in the title bar.
  getAppVersion: () => GetAppVersion(),

  // Settings & Subscriptions
  getSettings: async () => {
    try {
      const s = await GetSettings();
      return JSON.parse(s);
    } catch (e) {
      console.error('getSettings parse error:', e);
      return {};
    }
  },
  saveSettings: (settings) => SaveSettings(JSON.stringify(settings)),
  getSubscriptions: async () => {
    try {
      const s = await GetSubscriptions();
      return JSON.parse(s);
    } catch (e) {
      console.error('getSubscriptions parse error:', e);
      return [];
    }
  },
  saveSubscriptions: async (subs) => {
    return SaveSubscriptions(JSON.stringify(subs));
  },
  fetchSubscription: (url) => FetchSubscription(url),
  importFromClipboard: async () => {
    try {
      const text = await navigator.clipboard.readText();
      const links = await ImportClipboard(text);
      if (window.api._subResultCallback) {
        window.api._subResultCallback(links);
      }
    } catch (e) {
      console.error("Clipboard access failed:", e);
    }
  },

  // Base64 helper
  decodeBase64: (str) => {
    try {
      const b64 = str.replace(/\s/g, '').replace(/-/g, '+').replace(/_/g, '/');
      return atob(b64);
    } catch (e) {
      return '';
    }
  },

  // Event bindings (mocked or bound to Wails runtime EventsOn)
  // The backend batches log lines (see backend/service/logstream.go): the
  // callback receives an array, not a single line.
  onLog: (callback) => EventsOn('xray-log-batch', callback),
  onStarted: (callback) => EventsOn('xray-started', callback),
  onStopped: (callback) => EventsOn('xray-stopped', callback),
  onSubscriptionResult: (callback) => {
    window.api._subResultCallback = callback;
  },
  onPingResult: (callback) => {
    window.api._pingCallback = callback;
  },
  onTrayToggleConnection: (callback) => EventsOn('tray-toggle-connection', callback),
  onSubscriptionsUpdated: (callback) => EventsOn('subscriptions-updated', callback),
  onTrayServerSelected: (callback) => EventsOn('tray-server-selected', callback),
  onTrayStartReconnect: (callback) => EventsOn('tray-start-reconnect', callback),
  onTrayRestart: (callback) => EventsOn('tray-restart', callback),

  // Auto update
  checkUpdates: () => CheckUpdates(),
  downloadAndInstallUpdate: (downloadURL, signatureHex) => DownloadAndInstallUpdate(downloadURL, signatureHex),
  openUpdateLink: (url) => {
    window.open(url, '_blank');
  },
  onUpdateProgress: (callback) => EventsOn('update-progress', callback),
  onUpdateComplete: (callback) => EventsOn('update-complete', callback),
  onUpdateError: (callback) => EventsOn('update-error', callback),

  // Logs
  saveLogs: (content) => SaveLogs(content),
  openLogsFolder: () => OpenLogsFolder(),

  // Watchdog events
  onWatchdogReconnecting: (cb) => EventsOn('watchdog-reconnecting', cb),
  onWatchdogReconnected: (cb) => EventsOn('watchdog-reconnected', cb),
  onWatchdogFailed: (cb) => EventsOn('watchdog-failed', cb),
  // Emitted while the VPN server is unreachable: the watchdog deliberately holds
  // off restarting the core until connectivity comes back.
  onWatchdogWaiting: (cb) => EventsOn('watchdog-waiting', cb),

  // Admin rights
  checkAdmin: () => CheckAdmin(),
  requestAdmin: () => RequestAdmin(),

  // Window controls
  minimize: () => {
    if (window.runtime && window.runtime.WindowMinimise) {
      window.runtime.WindowMinimise();
    }
  },
  close: () => {
    if (window.runtime && window.runtime.WindowHide) {
      window.runtime.WindowHide();
      NotifyWindowHidden();
    }
  },
  // Emitted by the backend whenever the window goes to the tray, including the
  // paths the frontend never sees (tray menu toggle, close-to-tray).
  onWindowHidden: (callback) => {
    EventsOn('window-hidden', callback);
    EventsOn('wails:window-minimise', callback);
  },
  onWindowRestored: (callback) => {
    EventsOn('window-restored', callback);
    EventsOn('wails:window-unminimise', callback);
    EventsOn('wails:window-restore', callback);
    EventsOn('wails:window-focus', callback);
    
    // Add robust fallbacks using standard DOM focus and visibility APIs
    window.addEventListener('focus', callback);
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') {
        callback();
      }
    });
  }
};

// Formatting helper for human-readable speed strings
function formatSpeed(bytesPerSec) {
  if (!bytesPerSec || bytesPerSec <= 0) return '0.0 KB/s';
  const k = 1024;
  const sizes = ['B/s', 'KB/s', 'MB/s', 'GB/s'];
  const i = Math.floor(Math.log(bytesPerSec) / Math.log(k));
  return parseFloat((bytesPerSec / Math.pow(k, i)).toFixed(1)) + ' ' + sizes[i];
}

// Global listener for the 'traffic-stats' event emitted by the Go backend
EventsOn('traffic-stats', (data) => {
  const container = document.getElementById('speedometerContainer');
  const speedDownload = document.getElementById('speedDownload');
  const speedUpload = document.getElementById('speedUpload');

  if (container && speedDownload && speedUpload) {
    // Show container when there's an active connection and statistics are coming in
    if (container.style.display !== 'flex') {
      container.style.display = 'flex';
    }
    speedDownload.textContent = formatSpeed(data.down);
    speedUpload.textContent = formatSpeed(data.up);

    // Итоги за сессию считает Go: пока окно в трее события сюда не приходят
    // вовсе, и суммирование на этой стороне потеряло бы весь этот трафик.
    window.sessionBytesDown = data.totalDown || 0;
    window.sessionBytesUp   = data.totalUp   || 0;

    const totalContainer = document.getElementById('sessionTotalContainer');
    const totalDown = document.getElementById('sessionTotalDown');
    const totalUp   = document.getElementById('sessionTotalUp');
    if (totalContainer && totalDown && totalUp) {
      if (totalContainer.style.display === 'none') totalContainer.style.display = 'flex';
      totalDown.textContent = formatBytesLocal(window.sessionBytesDown);
      totalUp.textContent   = formatBytesLocal(window.sessionBytesUp);
    }

    const speedTotalContainer = document.getElementById('speedometerTotalContainer');
    const speedTotal = document.getElementById('speedTotal');
    if (speedTotalContainer && speedTotal) {
      if (speedTotalContainer.style.display !== 'flex') speedTotalContainer.style.display = 'flex';
      speedTotal.textContent = formatBytesLocal(window.sessionBytesDown + window.sessionBytesUp);
    }

    // Игровой стиль прогресс-бара трафика
    const totalBytes = window.sessionBytesDown + window.sessionBytesUp;
    
    // Вычисляем динамический лимит
    let limitBytes = 100 * 1024 * 1024; // 100 MB default
    let limitLabel = '100 MB';
    
    if (totalBytes > 100 * 1024 * 1024) {
      if (totalBytes <= 1024 * 1024 * 1024) {
        limitBytes = 1024 * 1024 * 1024; // 1 GB
        limitLabel = '1 GB';
      } else if (totalBytes <= 10 * 1024 * 1024 * 1024) {
        limitBytes = 10 * 1024 * 1024 * 1024; // 10 GB
        limitLabel = '10 GB';
      } else if (totalBytes <= 100 * 1024 * 1024 * 1024) {
        limitBytes = 100 * 1024 * 1024 * 1024; // 100 GB
        limitLabel = '100 GB';
      } else {
        limitBytes = 1024 * 1024 * 1024 * 1024; // 1 TB
        limitLabel = '1 TB';
      }
    }
    
    const percentage = Math.min(100, Math.round((totalBytes / limitBytes) * 100));
    
    const ratioEl = document.getElementById('trafficGameRatio');
    const fillEl = document.getElementById('trafficGameProgressFill');
    const totalLimitEl = document.getElementById('trafficGameTotalAndLimit');
    
    if (ratioEl) ratioEl.textContent = `${percentage}%`;
    if (fillEl) fillEl.style.width = `${percentage}%`;
    if (totalLimitEl) {
      totalLimitEl.textContent = `${formatBytesLocal(totalBytes)} / ${limitLabel}`;
    }
  }
});

// Локальный formatBytes (без зависимости от renderer.js)
function formatBytesLocal(bytes) {
  if (!bytes || bytes <= 0) return '0 B';
  const k = 1024;
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(i === 0 ? 0 : 2)) + ' ' + sizes[i];
}

// Reset speedometer when VPN engine stops
EventsOn('xray-stopped', () => {
  const container = document.getElementById('speedometerContainer');
  if (container) container.style.display = 'none';
  const totalContainer = document.getElementById('sessionTotalContainer');
  if (totalContainer) totalContainer.style.display = 'none';
  const speedTotalContainer = document.getElementById('speedometerTotalContainer');
  if (speedTotalContainer) speedTotalContainer.style.display = 'none';
  // Сбрасываем счётчик
  window.sessionBytesDown = 0;
  window.sessionBytesUp = 0;
});

