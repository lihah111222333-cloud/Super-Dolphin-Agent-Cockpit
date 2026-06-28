// @ts-nocheck
export function normalizeDiffRevision(value) {
  const num = Number(value);
  return Number.isFinite(num) && num > 0 ? Math.floor(num) : 0;
}

export function getLoadedDiffRevision({ threadId, diffTextByThread, loadedRevisionByThread, normalizeThreadID }) {
  const id = normalizeThreadID(threadId);
  if (!id) return 0;
  if (!Object.prototype.hasOwnProperty.call(diffTextByThread || {}, id)) return 0;
  return normalizeDiffRevision(loadedRevisionByThread.get(id));
}

function mergeNumberMapAtomic(current, source) {
  if (!source || typeof source !== 'object') return { next: current, changed: false };
  let next = current;
  let changed = false;
  for (const [key, value] of Object.entries(source)) {
    const num = normalizeDiffRevision(value);
    if (next[key] === num) continue;
    if (!changed) { next = { ...(next || {}) }; changed = true; }
    next[key] = num;
  }
  return { next, changed };
}

export function mergeDiffRuntimePatch(data, state) {
  const patch = {};
  const mergedDiffRevisions = mergeNumberMapAtomic(state.diffRevisionByThread, data.diffRevisionByThread);
  if (mergedDiffRevisions.changed) patch.diffRevisionByThread = mergedDiffRevisions.next;
  let nextDiffTextByThread = state.diffTextByThread;
  let diffChanged = false;
  if (data.diffTextByThread && typeof data.diffTextByThread === 'object') {
    for (const [key, value] of Object.entries(data.diffTextByThread)) {
      const str = (value || '').toString();
      if (nextDiffTextByThread[key] === str) continue;
      if (!diffChanged) { nextDiffTextByThread = { ...nextDiffTextByThread }; diffChanged = true; }
      nextDiffTextByThread[key] = str;
    }
  }
  if (diffChanged) patch.diffTextByThread = nextDiffTextByThread;
  return patch;
}

export function markLoadedDiffRevisions(data, state, loadedRevisionByThread) {
  if (!data.diffTextByThread || typeof data.diffTextByThread !== 'object') return;
  Object.keys(data.diffTextByThread).forEach((key) => {
    loadedRevisionByThread.set(key, normalizeDiffRevision(state.diffRevisionByThread?.[key]));
  });
}

export function createSyncThreadDiffState(deps) {
  const {
    state,
    threadDiffSyncPromiseByThread,
    threadDiffLoadedRevisionByThread,
    normalizeThreadID,
    getPreferenceScopeCwd,
    callAPI,
    withPreferenceScope,
    logInfo,
    logWarn,
    perfNow,
    applyRuntimeSnapshot,
  } = deps;
  return async function syncThreadDiffState(threadId, options = {}) {
    const id = normalizeThreadID(threadId);
    if (!id) return null;
    const force = options?.force === true;
    const snapshotDiffRevision = normalizeDiffRevision(state.diffRevisionByThread?.[id]);
    const loadedDiffRevision = getLoadedDiffRevision({
      threadId: id,
      diffTextByThread: state.diffTextByThread,
      loadedRevisionByThread: threadDiffLoadedRevisionByThread,
      normalizeThreadID,
    });
    // ui/state/get 已按当前 thread 作用域返回 diff；前端这里继续按 thread 记 knownDiffRevision 即可。
    if (!force && snapshotDiffRevision === loadedDiffRevision) return null;
    if (!force && snapshotDiffRevision === 0 && loadedDiffRevision === 0) return null;
    const inFlight = threadDiffSyncPromiseByThread.get(id);
    if (inFlight) return inFlight;

    logInfo('thread', 'state.sync.diff.request', {
      thread_id: id,
      snapshot_diff_revision: snapshotDiffRevision,
      loaded_diff_revision: loadedDiffRevision,
      force,
      cwd: getPreferenceScopeCwd(),
    });
    const request = (async () => {
      const start = perfNow();
      try {
        const res = await callAPI('ui/state/get', withPreferenceScope({ threadId: id, includeDiff: true, knownDiffRevision: force ? 0 : loadedDiffRevision }));
        const hasDiffPayload = Boolean(res?.diffTextByThread && typeof res.diffTextByThread === 'object' && Object.prototype.hasOwnProperty.call(res.diffTextByThread, id));
        const diffText = hasDiffPayload ? (res.diffTextByThread[id] || '').toString() : '';
        const diffRevision = normalizeDiffRevision(res?.diffRevisionByThread?.[id]);
        logInfo('thread', 'state.sync.diff.response', {
          thread_id: id,
          diff_revision: diffRevision,
          known_diff_revision: force ? 0 : loadedDiffRevision,
          has_diff_payload: hasDiffPayload,
          diff_len: diffText.length,
          duration_ms: Math.round(perfNow() - start),
        });
        applyRuntimeSnapshot(state, res || {}, { requestedThreadId: id, allowActiveSelectionPatch: false, loadedRevisionByThread: threadDiffLoadedRevisionByThread });
        if (!hasDiffPayload && diffRevision === (force ? 0 : loadedDiffRevision)) {
          threadDiffLoadedRevisionByThread.set(id, diffRevision);
        }
        logInfo('thread', 'state.sync.diff.applied', {
          thread_id: id,
          diff_revision: normalizeDiffRevision(state.diffRevisionByThread?.[id]),
          loaded_diff_revision: getLoadedDiffRevision({
            threadId: id,
            diffTextByThread: state.diffTextByThread,
            loadedRevisionByThread: threadDiffLoadedRevisionByThread,
            normalizeThreadID,
          }),
          duration_ms: Math.round(perfNow() - start),
        });
        return res || {};
      } catch (error) {
        logWarn('thread', 'state.sync.diff.failed', { thread_id: id, error, duration_ms: Math.round(perfNow() - start) });
        throw error;
      } finally {
        threadDiffSyncPromiseByThread.delete(id);
      }
    })();
    threadDiffSyncPromiseByThread.set(id, request);
    return request;
  };
}
