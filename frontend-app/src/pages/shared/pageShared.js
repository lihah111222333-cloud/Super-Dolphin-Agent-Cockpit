import { useEffect, useMemo, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { MEMORY_TYPE_INFO, memoryHealth, normalizeMemoryEntry, normalizeMemorySection, normalizeMemorySnapshot, normalizeSimilarityGroups } from '../../adapters/memoryAdapter.js';
import { memoryPageService } from '../memory/services/memoryPageService.js';

const SKILLS_REQUEST_TIMEOUT_MS = 8000;
const DASHBOARD_FOCUS_INVALIDATION_COALESCE_MS = 50;

async function withTimeout(promise, timeoutMs, message) {
  let timeoutID;
  const timeout = new Promise((_, reject) => {
    timeoutID = globalThis.setTimeout(() => reject(new Error(message)), timeoutMs);
  });
  return Promise.race([promise, timeout]).finally(() => {
    if (timeoutID) globalThis.clearTimeout(timeoutID);
  });
}

const MODEL_OPTIONS_BY_PROVIDER = Object.freeze({
  codex: Object.freeze([
    { value: 'gpt-5.5', label: 'GPT-5.5', short: '5.5' },
    { value: 'gpt-5.4', label: 'GPT-5.4', short: '5.4' },
    { value: 'gpt-5.4-mini', label: 'GPT-5.4 Mini', short: '5.4 Mini' },
    { value: 'gpt-5', label: 'GPT-5', short: '5' },
    { value: 'codex-auto-review', label: 'Codex Auto Review', short: 'Auto Review' },
  ]),
  claude: Object.freeze([
    { value: 'opus', label: 'Opus 4.7' },
    { value: 'opus[1m]', label: 'Opus 4.7 [1M]' },
    { value: 'claude-opus-4-6', label: 'Opus 4.6' },
    { value: 'claude-opus-4-6[1m]', label: 'Opus 4.6 [1M]' },
    { value: 'sonnet', label: 'Sonnet 4.7' },
    { value: 'sonnet[1m]', label: 'Sonnet 4.7 [1M]' },
    { value: 'claude-sonnet-4-6', label: 'Sonnet 4.6' },
    { value: 'claude-sonnet-4-6[1m]', label: 'Sonnet 4.6 [1M]' },
    { value: 'haiku', label: 'Haiku 4.5' },
  ]),
});

const CLAUDE_LONG_TO_SHORT = Object.freeze({
  'claude-opus-4-7': 'opus',
  'claude-opus-4-7[1m]': 'opus[1m]',
  'claude-haiku-4-5': 'haiku',
});

function normalizeProviderKey(value) {
  return textValue(value).toLowerCase() === 'claude' ? 'claude' : 'codex';
}

function normalizeConfigText(value) {
  return textValue(value);
}

function canonicalizeModelValue(provider, value) {
  const normalized = normalizeConfigText(value);
  if (normalizeProviderKey(provider) === 'claude') return CLAUDE_LONG_TO_SHORT[normalized] || normalized;
  return normalized;
}

function modelOptionFor(provider, value) {
  const normalized = canonicalizeModelValue(provider, value);
  const options = MODEL_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || MODEL_OPTIONS_BY_PROVIDER.codex;
  return (
    options.find((item) => canonicalizeModelValue(provider, item.value) === normalized)
    || (normalized ? { value: normalized, label: normalized, short: normalized } : null)
  );
}

function appendCurrentModelOption(provider, value) {
  const options = MODEL_OPTIONS_BY_PROVIDER[normalizeProviderKey(provider)] || MODEL_OPTIONS_BY_PROVIDER.codex;
  const current = modelOptionFor(provider, value);
  if (!current || options.some((item) => canonicalizeModelValue(provider, item.value) === current.value)) return options;
  return [...options, current];
}

function optionalSettingsCwd(value) {
  const cwd = textValue(value);
  return cwd && cwd !== '.' && cwd !== '未选择项目' ? cwd : '';
}

function dashboardQueryKey(cwd, page, ...parts) {
  return ['dashboard', 'project', cwd, page, ...parts.map((part) => textValue(part)).filter(Boolean)];
}

async function loadMemoryDashboard(cwd, options) {
  return memoryPageService.loadDashboard(cwd, options);
}

function queryErrorMessage(query) {
  return query?.error ? errorMessage(query.error) : '';
}

function queryHasSnapshot(query) {
  return query?.data !== undefined;
}

function dashboardQueryErrorState(query, hasSnapshot = queryHasSnapshot(query)) {
  const message = queryErrorMessage(query);
  return {
    cachedSyncError: message && hasSnapshot ? `同步失败，显示的是上次成功的数据：${message}` : '',
    blockingError: message && !hasSnapshot ? message : '',
  };
}

function useDashboardQueryFocusInvalidation(queryKey) {
  const queryClient = useQueryClient();
  const pendingInvalidateRef = useRef(null);

  useEffect(() => {
    if (!Array.isArray(queryKey) || queryKey.length === 0) return undefined;

    const flushInvalidate = () => {
      pendingInvalidateRef.current = null;
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      void queryClient.invalidateQueries({ queryKey });
    };

    const invalidate = () => {
      if (typeof document !== 'undefined' && document.visibilityState === 'hidden') return;
      if (pendingInvalidateRef.current !== null) return;
      pendingInvalidateRef.current = globalThis.setTimeout(
        flushInvalidate,
        DASHBOARD_FOCUS_INVALIDATION_COALESCE_MS,
      );
    };

    window.addEventListener('focus', invalidate);
    document.addEventListener('visibilitychange', invalidate);
    return () => {
      window.removeEventListener('focus', invalidate);
      document.removeEventListener('visibilitychange', invalidate);
      if (pendingInvalidateRef.current !== null) {
        globalThis.clearTimeout(pendingInvalidateRef.current);
        pendingInvalidateRef.current = null;
      }
    };
  }, [queryClient, queryKey]);
}

function useDashboardFocusInvalidation(cwd, surface) {
  const queryKey = useMemo(
    () => (cwd && surface ? dashboardQueryKey(cwd, surface) : null),
    [cwd, surface],
  );
  useDashboardQueryFocusInvalidation(queryKey);
}

function cleanScalar(value) {
  return textValue(value).replace(/^['"]|['"]$/g, '').trim();
}

function wordListFromText(value) {
  const text = Array.isArray(value) ? value.join(',') : textValue(value);
  return (
    text
    .replace(/[，、；;\n]/g, ',')
    .split(',')
    .map(cleanScalar)
    .filter(Boolean)
    .filter((word, index, list) => list.findIndex((item) => item.toLowerCase() === word.toLowerCase()) === index)
  );
}

function textValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function rawTextValue(value) {
  return value === null || value === undefined ? '' : value.toString();
}

function firstPresentText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return '';
}

function firstPresentRawText(...values) {
  for (const value of values) {
    const text = rawTextValue(value);
    if (text) return text;
  }
  return '';
}

function parseStrictJsonValue(text, label = 'JSON') {
  try {
    return globalThis.JSON.parse(text);
  } catch (error) {
    throw new Error(`${label} 不是合法 JSON：${errorMessage(error)}`, { cause: error });
  }
}

function parseJsonObjectValue(text, label, { allowNull = false } = {}) {
  const value = parseStrictJsonValue(text, label);
  if (value === null && allowNull) return value;
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error(`${label} 必须是 JSON 对象`);
  }
  return value;
}

function systemClockNowMillis() {
  const performanceClock = globalThis.performance;
  if (performanceClock && Number.isFinite(performanceClock.timeOrigin) && typeof performanceClock.now === 'function') {
    return Math.floor(performanceClock.timeOrigin + performanceClock.now());
  }
  return new globalThis.Date().getTime();
}

function currentTimestampMillis(label = '当前时间', clock = systemClockNowMillis) {
  const value = typeof clock === 'function' ? clock() : clock?.now?.();
  const time = Number(value);
  if (!Number.isFinite(time) || time <= 0) throw new Error(`${label} clock returned invalid timestamp`);
  return time;
}

function requireTimestampMillis(value, label) {
  const text = textValue(value);
  if (!text) throw new Error(`${label} 缺少时间戳`);
  const date = new globalThis.Date(text);
  const time = date.getTime();
  if (!Number.isFinite(time)) throw new Error(`${label} 时间戳无效：${text}`);
  return time;
}

function optionalTimestampMillis(value, label = '可选时间戳') {
  const text = textValue(value);
  if (!text) return 0;
  return requireTimestampMillis(text, label);
}

function optionalDateFromValue(value, label = '可选时间戳') {
  const text = textValue(value);
  if (!text) return null;
  return new globalThis.Date(requireTimestampMillis(text, label));
}

function requireArrayValue(value, label) {
  if (!Array.isArray(value)) throw new Error(`${label} 必须是数组`);
  return value;
}

function optionalArrayValue(value, label) {
  if (value === null || value === undefined) return [];
  return requireArrayValue(value, label);
}

function requirePlainObjectValue(value, label) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} 必须是对象`);
  return value;
}

function optionalPlainObjectValue(value, label) {
  if (value === null || value === undefined) return {};
  return requirePlainObjectValue(value, label);
}

function firstText(...values) {
  for (const value of values) {
    const text = textValue(value);
    if (text) return text;
  }
  return '';
}

function numberOrNull(value) {
  if (value === null || value === undefined || value === '') return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function objectValue(value) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value : {};
}

function sharedFileTimestamp(value) {
  const text = textValue(value);
  if (!text) return '-';
  const date = optionalDateFromValue(text, 'shared file timestamp');
  if (!date) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function memoryNoticeText(value) {
  const text = textValue(value);
  return text.length > 120 ? `${text.slice(0, 119)}…` : text;
}

function errorMessage(error) {
  return memoryNoticeText(firstPresentRawText(error?.message, error));
}

function listToText(words) {
  return Array.isArray(words) ? words.join(', ') : '';
}

export { appendCurrentModelOption, canonicalizeModelValue, CLAUDE_LONG_TO_SHORT, cleanScalar, currentTimestampMillis, dashboardQueryErrorState, dashboardQueryKey, errorMessage, firstPresentRawText, firstPresentText, firstText, listToText, loadMemoryDashboard, MEMORY_TYPE_INFO, memoryHealth, memoryNoticeText, MODEL_OPTIONS_BY_PROVIDER, modelOptionFor, normalizeConfigText, normalizeMemoryEntry, normalizeMemorySection, normalizeMemorySnapshot, normalizeProviderKey, normalizeSimilarityGroups, numberOrNull, objectValue, optionalArrayValue, optionalDateFromValue, optionalPlainObjectValue, optionalSettingsCwd, optionalTimestampMillis, parseJsonObjectValue, parseStrictJsonValue, queryErrorMessage, queryHasSnapshot, rawTextValue, requireArrayValue, requirePlainObjectValue, requireTimestampMillis, sharedFileTimestamp, SKILLS_REQUEST_TIMEOUT_MS, textValue, useDashboardFocusInvalidation, useDashboardQueryFocusInvalidation, withTimeout, wordListFromText };
