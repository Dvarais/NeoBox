import { optionalEl, setText } from '../modules/dom';
import type { Translations } from '../modules/translations';

/**
 * Разделяет скорость на число и единицу.
 */
export function formatSpeedParts(bytesPerSec: number): { value: string; unit: string } {
  const units = ['B/s', 'KB/s', 'MB/s', 'GB/s', 'TB/s'];
  if (!bytesPerSec || bytesPerSec <= 0) return { value: '0', unit: units[0] };

  const k = 1024;
  const i = Math.min(Math.floor(Math.log(bytesPerSec) / Math.log(k)), units.length - 1);
  const scaled = bytesPerSec / Math.pow(k, i);
  return {
    value: scaled >= 100 ? String(Math.round(scaled)) : scaled.toFixed(1),
    unit: units[i],
  };
}

/**
 * Разделяет объём на число и единицу теми же правилами, что и скорость.
 */
export function formatBytesParts(bytes: number): { value: string; unit: string } {
  const units = ['B', 'KB', 'MB', 'GB', 'TB'];
  if (!bytes || bytes <= 0) return { value: '0', unit: units[0] };

  const k = 1024;
  const i = Math.min(Math.floor(Math.log(bytes) / Math.log(k)), units.length - 1);
  const scaled = bytes / Math.pow(k, i);
  return {
    value: scaled >= 100 || i === 0 ? String(Math.round(scaled)) : scaled.toFixed(1),
    unit: units[i],
  };
}

const trafficLast: Record<string, string> = {};

function writeTrafficCell(idBase: string, parts: { value: string; unit: string }): void {
  if (trafficLast[idBase + 'v'] !== parts.value) {
    trafficLast[idBase + 'v'] = parts.value;
    setText(idBase + 'Value', parts.value);
  }
  if (trafficLast[idBase + 'u'] !== parts.unit) {
    trafficLast[idBase + 'u'] = parts.unit;
    setText(idBase + 'Unit', parts.unit);
  }
}

/**
 * Показывает индикатор только при активном подключении.
 */
export function setSpeedMeterVisible(visible: boolean): void {
  const card = optionalEl('trafficCard');
  if (!card) return;
  card.style.display = visible ? 'flex' : 'none';
  if (!visible) {
    for (const key of Object.keys(trafficLast)) delete trafficLast[key];
    writeTrafficCell('speedDown', formatSpeedParts(0));
    writeTrafficCell('speedUp', formatSpeedParts(0));
    writeTrafficCell('totalDown', formatBytesParts(0));
    writeTrafficCell('totalUp', formatBytesParts(0));
  }
}

export function applyTrafficLanguage(t: Translations): void {
  setText('trafficColDown', t.speedDownLabel);
  setText('trafficColUp', t.speedUpLabel);
  setText('trafficRowNow', t.trafficRowNow);
  setText('trafficRowSession', t.trafficRowSession);
  setText('trafficCardTitle', t.speedMeterLabel);
}

let isInitialized = false;

export function initTrafficView(): void {
  if (isInitialized) return;
  isInitialized = true;

  if (window.api?.onTraffic) {
    window.api.onTraffic((stats) => {
      writeTrafficCell('speedDown', formatSpeedParts(stats.down));
      writeTrafficCell('speedUp', formatSpeedParts(stats.up));
      writeTrafficCell('totalDown', formatBytesParts(stats.totalDown));
      writeTrafficCell('totalUp', formatBytesParts(stats.totalUp));
    });
  }
}
