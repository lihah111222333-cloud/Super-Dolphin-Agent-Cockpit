const PROMPT_HISTORY_LIMIT = 50;
const STALE_PROMPT_HISTORY_CODE = -31003;
const STALE_PROMPT_HISTORY_MESSAGE = 'prompt history snapshot is stale';

export function createPromptHistoryController({ fetchPage, cwd, activeThreadId = '' }) {
  if (typeof fetchPage !== 'function') throw new Error('fetchPage is required');
  if (typeof cwd !== 'string') throw new Error('cwd is required');
  const normalizedCwd = cwd.trim();
  if (!normalizedCwd) throw new Error('cwd is required');
  if (typeof activeThreadId !== 'string') throw new TypeError('activeThreadId must be a string');
  const normalizedActiveThreadId = activeThreadId.trim();
  let entries = [];
  let index = -1;
  let cursor = '';
  let nonce = '';
  let hasMore = false;
  let loaded = false;
  let generation = 0;
  let navigationIntent = 0;
  let pending;
  let pendingSelection;
  let draftSentinel = '';

  function captureDraft(draft) {
    if (typeof draft !== 'string') throw new TypeError('draft must be a string');
    if (index === -1) draftSentinel = draft;
  }

  function previous() {
    if (pending && pendingSelection?.intent === navigationIntent) return pendingSelection.promise;
    const requestIntent = navigationIntent + 1;
    navigationIntent = requestIntent;
    if (index + 1 < entries.length) {
      index += 1;
      return Promise.resolve(entries[index].text);
    }
    if (loaded && !hasMore) {
      return Promise.resolve(index >= 0 ? entries[index].text : draftSentinel);
    }
    if (!pending) {
      const requestGeneration = generation;
      const task = loadPreviousPage(requestGeneration, false);
      const shared = task.finally(() => {
        if (pending === shared) pending = undefined;
      });
      pending = shared;
    }
    const selection = pending.then((pageLoaded) => {
      if (!pageLoaded || requestIntent !== navigationIntent) return undefined;
      if (index + 1 < entries.length) {
        index += 1;
        return entries[index].text;
      }
      return index >= 0 ? entries[index].text : draftSentinel;
    });
    pendingSelection = { intent: requestIntent, promise: selection };
    return selection;
  }

  function next() {
    navigationIntent += 1;
    if (index > 0) {
      index -= 1;
      return entries[index].text;
    }
    if (index === 0) index = -1;
    return draftSentinel;
  }

  function invalidate() {
    generation += 1;
    navigationIntent += 1;
    resetPageState();
    pending = undefined;
    pendingSelection = undefined;
  }

  function snapshot() {
    return {
      entries: entries.map((entry) => ({ ...entry })),
      index,
      cursor,
      nonce,
      hasMore,
      generation,
      pending: Boolean(pending),
      draftSentinel,
    };
  }

  async function loadPreviousPage(requestGeneration, staleRetried) {
    try {
      const response = await fetchPage({
        cwd: normalizedCwd,
        activeThreadId: normalizedActiveThreadId,
        cursor,
        nonce,
        limit: PROMPT_HISTORY_LIMIT,
      });
      if (requestGeneration !== generation) return false;
      assertPromptHistoryPage(response);
      entries = entries.concat(response.entries);
      cursor = response.nextCursor;
      nonce = response.nonce;
      hasMore = response.hasMore;
      loaded = true;
      return true;
    } catch (error) {
      if (requestGeneration !== generation) return false;
      if (!staleRetried && isStalePromptHistoryError(error)) {
        resetPageState();
        return loadPreviousPage(requestGeneration, true);
      }
      throw error;
    }
  }

  function resetPageState() {
    entries = [];
    index = -1;
    cursor = '';
    nonce = '';
    hasMore = false;
    loaded = false;
  }

  return { captureDraft, previous, next, invalidate, snapshot };
}

function isStalePromptHistoryError(error) {
  const message = typeof error?.message === 'string' ? error.message : '';
  return message === STALE_PROMPT_HISTORY_MESSAGE
    && typeof error?.code === 'number'
    && error.code === STALE_PROMPT_HISTORY_CODE;
}

function assertPromptHistoryPage(response) {
  if (!response || typeof response !== 'object'
    || !Array.isArray(response.entries)
    || response.entries.length > PROMPT_HISTORY_LIMIT
    || typeof response.nextCursor !== 'string'
    || typeof response.hasMore !== 'boolean'
    || typeof response.nonce !== 'string'
    || !response.nonce.trim()
    || (response.hasMore && !response.nextCursor)
    || (!response.hasMore && response.nextCursor !== '')) {
    throw new TypeError('prompt history response is invalid');
  }
  for (const entry of response.entries) {
    if (!entry || typeof entry !== 'object' || typeof entry.text !== 'string') {
      throw new TypeError('prompt history response is invalid');
    }
  }
}

export { PROMPT_HISTORY_LIMIT, STALE_PROMPT_HISTORY_CODE, STALE_PROMPT_HISTORY_MESSAGE };
