// Import Wails bindings
import { 
  CheckAdmin, 
  CheckUpdates, 
  FetchSubscription, 
  GetSettings, 
  GetSubscriptions, 
  ImportClipboard, 
  NotifyWindowHidden,
  NotifyWindowShown,
  PingServer, 
  RequestAdmin, 
  SaveSettings, 
  SaveSubscriptions, 
  StartXray, 
  StopXray 
} from './wailsjs/go/service/AppService.js';

import { EventsOn } from './wailsjs/runtime/runtime.js';

// Expose them as window.api to maintain total compatibility with original renderer.js!
window.api = {
  // Commands
  startXray: async (link, useSystemProxy) => {
    const settings = await window.api.getSettings();
    const res = await StartXray(link, JSON.stringify(settings), useSystemProxy);
    if (res && !res.success && res.error === 'admin_required') {
      window.api.requestAdmin();
    }
    return res;
  },
  restartXray: async (link, useSystemProxy) => {
    await StopXray();
    const settings = await window.api.getSettings();
    const res = await StartXray(link, JSON.stringify(settings), useSystemProxy);
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
    return res;
  },
  pingServer: async (link) => {
    const latency = await PingServer(link);
    // Ping result was sent as an IPC event in Electron. We invoke the callback directly!
    if (window.api._pingCallback) {
      window.api._pingCallback({ link, latency });
    }
  },
  
  // Settings & Subscriptions
  getSettings: async () => {
    const s = await GetSettings();
    return JSON.parse(s);
  },
  saveSettings: (settings) => SaveSettings(JSON.stringify(settings)),
  getSubscriptions: async () => {
    const s = await GetSubscriptions();
    return JSON.parse(s);
  },
  saveSubscriptions: async (subs) => {
    return SaveSubscriptions(JSON.stringify(subs));
  },
  setLanguage: (lang) => {
    // Handled dynamically on frontend renderer
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
  onLog: (callback) => EventsOn('xray-log', callback),
  onStopped: (callback) => EventsOn('xray-stopped', callback),
  onSubscriptionResult: (callback) => {
    window.api._subResultCallback = callback;
  },
  onPingResult: (callback) => {
    window.api._pingCallback = callback;
  },
  onSpeedtestResult: (callback) => {},
  onTrayToggleConnection: (callback) => EventsOn('tray-toggle-connection', callback),
  onSubscriptionsUpdated: (callback) => EventsOn('subscriptions-updated', callback),
  onTrayServerSelected: (callback) => EventsOn('tray-server-selected', callback),
  onTrayStartReconnect: (callback) => EventsOn('tray-start-reconnect', callback),
  onTrayRestart: (callback) => EventsOn('tray-restart', callback),

  // Auto update
  checkUpdates: () => CheckUpdates(),
  openUpdateLink: (url) => {
    window.open(url, '_blank');
  },

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
  }
});

// Reset speedometer when VPN engine stops
EventsOn('xray-stopped', () => {
  const container = document.getElementById('speedometerContainer');
  if (container) {
    container.style.display = 'none';
  }
});

