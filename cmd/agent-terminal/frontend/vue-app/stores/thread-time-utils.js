// @ts-nocheck

const threadOrderIndexById = new Map();
let threadOrderSeq = 0;

export function ensureThreadOrderIndex(threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return Number.MAX_SAFE_INTEGER;
  const existing = threadOrderIndexById.get(id);
  if (Number.isFinite(existing)) return existing;
  const created = threadOrderSeq;
  threadOrderSeq += 1;
  threadOrderIndexById.set(id, created);
  return created;
}

export function sortThreadsByStableFirstSeen(threads) {
  if (!Array.isArray(threads) || threads.length <= 1) {
    return Array.isArray(threads) ? threads : [];
  }
  return threads
    .map((item, index) => ({
      item,
      index,
      stableOrder: ensureThreadOrderIndex(item?.id),
    }))
    .sort((left, right) => {
      if (left.stableOrder !== right.stableOrder) {
        return left.stableOrder - right.stableOrder;
      }
      return left.index - right.index;
    })
    .map((entry) => entry.item);
}

export function normalizeEpochMillis(value) {
  if (!Number.isFinite(value) || value <= 0) {
    return 0;
  }
  const rounded = Math.round(value);
  if (rounded >= 1_000_000_000 && rounded < 1_000_000_000_000) {
    // 10-digit unix seconds → milliseconds.
    return rounded * 1000;
  }
  return rounded;
}

export function parseEpochMillis(value) {
  if (value === null || value === undefined) {
    return 0;
  }
  if (typeof value === 'number') {
    return normalizeEpochMillis(value);
  }
  const raw = value.toString().trim();
  if (!raw) {
    return 0;
  }
  const numeric = Number(raw);
  if (Number.isFinite(numeric)) {
    return normalizeEpochMillis(numeric);
  }
  const parsed = Date.parse(raw);
  if (!Number.isFinite(parsed) || parsed <= 0) {
    return 0;
  }
  return Math.round(parsed);
}

export function parseThreadCreatedAtFromID(threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return 0;
  const chunks = id.split(/[^0-9]+/).filter(Boolean);
  if (chunks.length === 0) return 0;
  const minEpochMs = Date.UTC(2000, 0, 1);
  const maxEpochMs = Date.UTC(2100, 0, 1);
  for (const chunk of chunks) {
    if (chunk.length < 10 || chunk.length > 19) continue;
    // chunk.slice(0, 13) collapses any precision-richer-than-ms encoding
    // (16-digit μs, 19-digit ns from Go's time.Now().UnixNano()) back to
    // milliseconds. 10/13-digit chunks are unchanged by the slice.
    const ts = parseEpochMillis(chunk.slice(0, 13));
    if (ts < minEpochMs || ts > maxEpochMs) continue;
    return ts;
  }
  return 0;
}
