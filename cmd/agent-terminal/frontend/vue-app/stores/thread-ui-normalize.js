// @ts-nocheck

import { normalizeStatus } from '../services/status.js';

export function normalizePreferenceScopeCwd(value) {
  const raw = (value || '').toString().trim();
  if (!raw || raw === '.') return '';
  return raw.replace(/[\\/]+$/, '');
}

export function normalizeSplitRatio(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 60;
  return Math.max(30, Math.min(75, Math.round(n)));
}

export function normalizeThreadRailWidth(value) {
  const n = Number(value);
  if (!Number.isFinite(n)) return 232;
  return Math.max(188, Math.min(420, Math.round(n)));
}

export function normalizeCmdCardCols(value) {
  return Number(value) === 2 ? 2 : 3;
}

export function normalizeThread(item) {
  const thread = {
    id: item?.id || '',
    name: item?.name || '',
    state: normalizeStatus(item?.state || 'idle'),
  };
  const createdAt = item?.createdAt || item?.created_at || '';
  const updatedAt = item?.updatedAt || item?.updated_at || '';
  if (createdAt) thread.createdAt = createdAt;
  if (updatedAt) thread.updatedAt = updatedAt;
  return thread;
}

export function normalizeThreadTimestampMap(value) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    return {};
  }
  const next = {};
  for (const [rawID, rawTime] of Object.entries(value)) {
    const id = (rawID || '').toString().trim();
    if (!id) continue;
    const ts = Number(rawTime);
    if (!Number.isFinite(ts) || ts <= 0) continue;
    next[id] = Math.round(ts);
  }
  return next;
}
