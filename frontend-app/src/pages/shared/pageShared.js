import { useEffect, useMemo, useRef } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { getMemorySnapshot } from '../../shared/api/backendApi.js';

const SKILLS_REQUEST_TIMEOUT_MS = 8000;
const DASHBOARD_FOCUS_INVALIDATION_COALESCE_MS = 50;

const MEMORY_TYPE_INFO = Object.freeze({
  user: { category: 'preference', label: '偏好' },
  feedback: { category: 'preference', label: '偏好' },
  project: { category: 'project', label: '项目' },
  reference: { category: 'project', label: '项目' },
});

function withTimeout(promise, timeoutMs, message) {
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
  return (value || '').toString().trim().toLowerCase() === 'claude' ? 'claude' : 'codex';
}

function normalizeConfigText(value) {
  return (value || '').toString().trim();
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
  const cwd = (value || '').toString().trim();
  return cwd && cwd !== '.' && cwd !== '未选择项目' ? cwd : '';
}

function dashboardQueryKey(cwd, page, ...parts) {
  return ['dashboard', 'project', cwd, page, ...parts.map((part) => textValue(part)).filter(Boolean)];
}

async function fetchMemoryDashboard(cwd) {
  const response = await withTimeout(
    getMemorySnapshot({ cwd }),
    SKILLS_REQUEST_TIMEOUT_MS,
    '记忆中心加载超时，请检查记忆数据或后端状态。',
  );
  return normalizeMemorySnapshot(response);
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
  return (value || '').toString().trim().replace(/^['"]|['"]$/g, '').trim();
}

function wordListFromText(value) {
  const text = Array.isArray(value) ? value.join(',') : (value || '').toString();
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
  const date = new Date(text);
  if (!Number.isFinite(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function normalizeMemorySnapshot(response) {
  if (!response || typeof response !== 'object' || Array.isArray(response)) {
    throw new Error('memory snapshot response must be an object');
  }
  return {
    overview: objectValue(response.overview),
    entries: [
      ...normalizeMemorySection(response.private, 'private'),
      ...normalizeMemorySection(response.team, 'team'),
    ],
  };
}

function normalizeMemorySection(section, target) {
  const value = objectValue(section);
  if (!Array.isArray(value.entries)) {
    throw new Error(`memory ${target} entries must be an array`);
  }
  return value.entries.map((item, index) => normalizeMemoryEntry(item, index, target));
}

function normalizeMemoryEntry(raw, index, target) {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    throw new Error(`memory ${target} entry ${index} must be an object`);
  }
  const path = textValue(raw.path);
  if (!path) throw new Error(`memory ${target} entry ${index} path is required`);
  const type = textValue(raw.type).toLowerCase();
  const typeInfo = MEMORY_TYPE_INFO[type];
  if (!typeInfo) throw new Error(`memory ${target} entry ${index} type is unsupported: ${type || '(empty)'}`);
  const name = firstText(raw.name, raw.title, path);
  if (!name) throw new Error(`memory ${target} entry ${index} name is required`);
  return {
    id: `${target}:${path}:${index}`,
    target,
    path,
    type,
    category: typeInfo.category,
    tag: typeInfo.label,
    name,
    title: firstText(raw.title, raw.name),
    description: firstText(raw.description, raw.summary),
    preview: firstText(raw.preview, raw.content, raw.text),
    updatedAt: firstText(raw.updatedAt, raw.updated_at, raw.createdAt, raw.created_at),
    source: textValue(raw.source),
    raw,
  };
}

function normalizeSimilarityGroups(value) {
  if (value === undefined || value === null) return [];
  if (!Array.isArray(value)) throw new Error('memory health similarGroups must be an array');
  return value.map((item, index) => {
    if (!item || typeof item !== 'object' || Array.isArray(item)) {
      throw new Error(`memory similar group ${index} must be an object`);
    }
    const group = {
      targetA: textValue(item.targetA || item.target_a),
      pathA: textValue(item.pathA || item.path_a),
      nameA: firstText(item.nameA, item.name_a),
      targetB: textValue(item.targetB || item.target_b),
      pathB: textValue(item.pathB || item.path_b),
      nameB: firstText(item.nameB, item.name_b),
      score: numberOrNull(item.score) ?? 0,
    };
    for (const key of ['targetA', 'pathA', 'targetB', 'pathB']) {
      if (!group[key]) throw new Error(`memory similar group ${index} ${key} is required`);
    }
    return group;
  });
}

function memoryHealth(overview, counts) {
  const health = overview?.health;
  if (!health || typeof health !== 'object' || Array.isArray(health)) return null;
  return {
    preferenceCount: numberOrNull(health.preferenceCount) ?? counts.preference,
    projectCount: numberOrNull(health.projectCount) ?? counts.project,
    maxPerCategory: numberOrNull(health.maxPerCategory) ?? 15,
    similarGroups: normalizeSimilarityGroups(health.similarGroups),
  };
}

function memoryNoticeText(value) {
  const text = textValue(value);
  return text.length > 120 ? `${text.slice(0, 119)}…` : text;
}

function errorMessage(error) {
  return memoryNoticeText(error?.message || String(error || ''));
}

function listToText(words) {
  return Array.isArray(words) ? words.join(', ') : '';
}

export { appendCurrentModelOption, canonicalizeModelValue, CLAUDE_LONG_TO_SHORT, cleanScalar, dashboardQueryErrorState, dashboardQueryKey, errorMessage, fetchMemoryDashboard, firstText, listToText, MEMORY_TYPE_INFO, memoryHealth, memoryNoticeText, MODEL_OPTIONS_BY_PROVIDER, modelOptionFor, normalizeConfigText, normalizeMemoryEntry, normalizeMemorySection, normalizeMemorySnapshot, normalizeProviderKey, normalizeSimilarityGroups, numberOrNull, objectValue, optionalSettingsCwd, queryErrorMessage, queryHasSnapshot, sharedFileTimestamp, SKILLS_REQUEST_TIMEOUT_MS, textValue, useDashboardFocusInvalidation, useDashboardQueryFocusInvalidation, withTimeout, wordListFromText };
