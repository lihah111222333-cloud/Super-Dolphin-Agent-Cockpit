import { parse as parseLosslessJson, stringify as stringifyLosslessJson } from 'lossless-json';
import { systemClockMillis } from '../../../entities/client/model/contractStoreModel.js';

const IMAGE_PATH_RE = /\.(?:png|jpe?g|webp|gif|svg|bmp)(?:[?#].*)?$/i;
const SAFE_RASTER_DATA_URL_RE = /^data:image\/(?:png|jpe?g|webp|gif|bmp);base64,[a-z0-9+/=\s]+$/i;

function basenameFromPath(path) {
  const value = textValue(path).trim().split(/[?#]/, 1)[0];
  if (!value) return '';
  return firstText(value.split(/[\\/]/).filter(Boolean).pop(), value);
}

function decodeLocalPath(value) {
  try {
    return decodeURIComponent(value);
  } catch {
    return value;
  }
}

function stripPathSuffix(value) {
  return textValue(value).split(/[?#]/, 1)[0];
}

function localPathSegments(value) {
  return decodeLocalPath(stripPathSuffix(value)).replace(/\\/g, '/').split('/').filter(Boolean);
}

function fileURLToPath(value) {
  const raw = textValue(value).trim();
  const stripped = decodeLocalPath(stripPathSuffix(raw.replace(/^file:\/\//i, '')));
  if (/^\/[A-Za-z]:[\\/]/.test(stripped)) return stripped.slice(1);
  if (/^[A-Za-z]:[\\/]/.test(stripped)) return stripped;
  if (/^\/(?!\/)/.test(stripped)) return stripped;
  try {
    const url = new URL(raw);
    if (url.protocol.toLowerCase() !== 'file:') return '';
    const path = decodeLocalPath(textValue(url.pathname));
    return /^\/[A-Za-z]:[\\/]/.test(path) ? path.slice(1) : path;
  } catch {
    return '';
  }
}

function isGeneratedImagePath(value) {
  const path = textValue(value).trim();
  if (!path || !IMAGE_PATH_RE.test(path)) return false;
  const segments = localPathSegments(path);
  if (segments.includes('..')) return false;
  return segments.some((segment, index) => (
    segment.toLowerCase() === '.codex'
    && segments[index + 1]?.toLowerCase() === 'generated_images'
    && Boolean(segments[index + 2])
  ));
}

function backendLocalImagePreviewSource(rawValue) {
  const value = textValue(rawValue).trim();
  if (!value.startsWith('/local-image?')) return '';
  try {
    const url = new URL(value, 'http://localhost');
    const id = firstText(url.searchParams.get('id'), url.searchParams.get('token')).trim();
    if (url.pathname !== '/local-image' || !id || url.searchParams.has('path')) return '';
    return value;
  } catch {
    return '';
  }
}

function backendGeneratedImagePreviewSource(rawValue) {
  const value = textValue(rawValue).trim();
  if (!value.startsWith('/generated-image?')) return '';
  try {
    const url = new URL(value, 'http://localhost');
    if (url.origin !== 'http://localhost' || url.pathname !== '/generated-image') return '';
    if (url.searchParams.getAll('path').length !== 1) return '';
    if ([...url.searchParams.keys()].some((key) => key !== 'path')) return '';
    const path = textValue(url.searchParams.get('path')).trim();
    if (!isGeneratedImagePath(path)) return '';
    return `/generated-image?path=${encodeURIComponent(path)}`;
  } catch {
    return '';
  }
}

function trustedImagePreviewSource(rawValue) {
  const value = textValue(rawValue).trim();
  if (!value) return '';
  if (value.startsWith('/clipboard/')) return value;
  const generatedPreview = backendGeneratedImagePreviewSource(value);
  if (generatedPreview) return generatedPreview;
  const localPreview = backendLocalImagePreviewSource(value);
  if (localPreview) return localPreview;
  if (SAFE_RASTER_DATA_URL_RE.test(value) || /^https?:\/\//i.test(value)) return value;
  return '';
}

function imagePreviewSource(rawValue) {
  const value = textValue(rawValue).trim();
  const trustedPreview = trustedImagePreviewSource(value);
  if (trustedPreview) return trustedPreview;
  if (!value || !IMAGE_PATH_RE.test(value)) return '';
  const localPath = /^file:\/\//i.test(value) ? fileURLToPath(value) : value;
  if (isGeneratedImagePath(localPath)) {
    return `/generated-image?path=${encodeURIComponent(localPath)}`;
  }
  return '';
}

function normalizeMessageText(text) {
  return textValue(text).replace(/\r\n/g, '\n');
}

function textValue(value) {
  if (value === null || value === undefined) return '';
  return value.toString();
}

function trimmedText(value) {
  return textValue(value).trim();
}

function firstText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return '';
}

function firstTrimmedText(...values) {
  for (const value of values) {
    const text = trimmedText(value);
    if (text) return text;
  }
  return '';
}

function requiredMarkdownObject(value, label) {
  if (value && typeof value === 'object') return value;
  throw new Error(`${label} 必须是对象`);
}

function requiredMarkdownArray(value, label) {
  if (Array.isArray(value)) return value;
  throw new Error(`${label} 必须是数组`);
}

function parseMarkdownJsonSnippet(text) {
  const value = trimmedText(text);
  try {
    return { ok: true, text: stringifyLosslessJson(parseLosslessJson(value), null, 2) };
  } catch (error) {
    return { ok: false, message: error instanceof Error ? error.message : 'JSON 解析失败' };
  }
}

function currentTimestampMs() {
  return systemClockMillis('chat current timestamp');
}

function parseTimestampParts(value) {
  if (!value) return null;
  const text = trimmedText(value);
  const match = text.match(/^(\d{4})-(\d{2})-(\d{2})(?:[T\s](\d{2}):(\d{2})(?::(\d{2})(?:\.\d+)?)?(Z|[+-]\d{2}:?\d{2})?)?$/);
  if (!match) return null;
  const year = Number(match[1]);
  const month = Number(match[2]);
  const day = Number(match[3]);
  const hour = Number(match[4] ?? 0);
  const minute = Number(match[5] ?? 0);
  const second = Number(match[6] ?? 0);
  if (!validTimestampFields({
    year,
    month,
    day,
    hour,
    minute,
    second,
  })) return null;
  return { year, month, day, hour, minute, second, zone: textValue(match[7]) };
}

function parseTimestampMs(value) {
  if (typeof value === 'number') return Number.isFinite(value) && value > 0 ? normalizeEpochMs(value) : 0;
  const text = trimmedText(value);
  if (!text) return 0;
  if (/^\d+(?:\.\d+)?$/.test(text)) return normalizeEpochMs(Number(text));
  const parts = parseTimestampParts(text);
  if (!parts) return 0;
  return timestampPartsToEpochMs(parts);
}

function timeLabelFromTimestamp(value) {
  const parts = parseTimestampParts(value);
  if (!parts) return '';
  return `${pad2(parts.hour)}:${pad2(parts.minute)}`;
}

function isoTimestampFromMs(ms) {
  if (!Number.isFinite(ms) || ms <= 0) return '';
  const totalSeconds = Math.floor(ms / 1000);
  const dayNumber = Math.floor(totalSeconds / 86400);
  const secondsOfDay = totalSeconds - (dayNumber * 86400);
  const date = civilFromDays(dayNumber);
  const hour = Math.floor(secondsOfDay / 3600);
  const minute = Math.floor((secondsOfDay % 3600) / 60);
  const second = secondsOfDay % 60;
  return `${date.year}-${pad2(date.month)}-${pad2(date.day)}T${pad2(hour)}:${pad2(minute)}:${pad2(second)}Z`;
}

function normalizeEpochMs(value) {
  return value < 1_000_000_000_000 ? value * 1000 : value;
}

function timestampPartsToEpochMs(parts) {
  const days = daysFromCivil(parts.year, parts.month, parts.day);
  let seconds = (days * 86400) + (parts.hour * 3600) + (parts.minute * 60) + parts.second;
  const offset = zoneOffsetMinutes(parts.zone);
  if (offset !== null) seconds -= offset * 60;
  return seconds > 0 ? seconds * 1000 : 0;
}

function zoneOffsetMinutes(zone) {
  if (!zone || zone === 'Z') return 0;
  const match = zone.match(/^([+-])(\d{2}):?(\d{2})$/);
  if (!match) return null;
  const minutes = (Number(match[2]) * 60) + Number(match[3]);
  return match[1] === '-' ? -minutes : minutes;
}

function validTimestampFields({
  year,
  month,
  day,
  hour,
  minute,
  second,
}) {
  if (year < 1970 || month < 1 || month > 12 || day < 1 || hour > 23 || minute > 59 || second > 59) return false;
  return day <= daysInMonth(year, month);
}

function daysInMonth(year, month) {
  if (month === 2) return isLeapYear(year) ? 29 : 28;
  return [4, 6, 9, 11].includes(month) ? 30 : 31;
}

function isLeapYear(year) {
  return year % 4 === 0 && (year % 100 !== 0 || year % 400 === 0);
}

function daysFromCivil(year, month, day) {
  let adjustedYear = year;
  let adjustedMonth = month;
  adjustedYear -= adjustedMonth <= 2 ? 1 : 0;
  const era = Math.floor(adjustedYear / 400);
  const yearOfEra = adjustedYear - era * 400;
  const monthPrime = adjustedMonth + (adjustedMonth > 2 ? -3 : 9);
  const dayOfYear = Math.floor((153 * monthPrime + 2) / 5) + day - 1;
  const dayOfEra = yearOfEra * 365 + Math.floor(yearOfEra / 4) - Math.floor(yearOfEra / 100) + dayOfYear;
  return era * 146097 + dayOfEra - 719468;
}

function civilFromDays(days) {
  const shifted = days + 719468;
  const era = Math.floor(shifted / 146097);
  const dayOfEra = shifted - era * 146097;
  const yearOfEra = Math.floor((dayOfEra - Math.floor(dayOfEra / 1460) + Math.floor(dayOfEra / 36524) - Math.floor(dayOfEra / 146096)) / 365);
  const yearDay = dayOfEra - (365 * yearOfEra + Math.floor(yearOfEra / 4) - Math.floor(yearOfEra / 100));
  const monthPrime = Math.floor((5 * yearDay + 2) / 153);
  const day = yearDay - Math.floor((153 * monthPrime + 2) / 5) + 1;
  const month = monthPrime + (monthPrime < 10 ? 3 : -9);
  const year = yearOfEra + era * 400 + (month <= 2 ? 1 : 0);
  return { year, month, day };
}

function pad2(value) {
  return String(value).padStart(2, '0');
}

export {
  basenameFromPath,
  currentTimestampMs,
  firstText,
  firstTrimmedText,
  imagePreviewSource,
  normalizeMessageText,
  isoTimestampFromMs,
  parseMarkdownJsonSnippet,
  parseTimestampMs,
  timeLabelFromTimestamp,
  requiredMarkdownArray,
  requiredMarkdownObject,
  textValue,
  trimmedText,
  trustedImagePreviewSource,
};
