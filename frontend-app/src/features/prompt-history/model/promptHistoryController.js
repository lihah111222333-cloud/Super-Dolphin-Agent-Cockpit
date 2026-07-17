const PROMPT_HISTORY_LIMIT = 50;
const STALE_PROMPT_HISTORY_CODE = -31003;
const STALE_PROMPT_HISTORY_MESSAGE = 'prompt history snapshot is stale';

/**
 * @typedef {{ messageId?: string, text: string, threadId?: string, createdAt?: string }} PromptHistoryEntry
 * @typedef {{
 *   entries: PromptHistoryEntry[],
 *   nextCursor: string,
 *   hasMore: boolean,
 *   nonce: string,
 * }} PromptHistoryPage
 * @typedef {{
 *   fetchPage: (params: { cwd: string, activeThreadId: string, cursor: string, nonce: string, limit: number }) => Promise<unknown>,
 *   cwd: string,
 *   activeThreadId?: string,
 * }} PromptHistoryControllerOptions
 */

/** @param {PromptHistoryControllerOptions} options */
export function createPromptHistoryController({ fetchPage, cwd, activeThreadId = '' }) {
  if (typeof fetchPage !== 'function') throw new Error('fetchPage is required');
  if (typeof cwd !== 'string') throw new Error('cwd is required');
  const normalizedCwd = cwd.trim();
  if (!normalizedCwd) throw new Error('cwd is required');
  if (typeof activeThreadId !== 'string') throw new TypeError('activeThreadId must be a string');
  const normalizedActiveThreadId = activeThreadId.trim();
  /** @type {PromptHistoryEntry[]} */
  let entries = [];
  let index = -1;
  let cursor = '';
  let nonce = '';
  let hasMore = false;
  let loaded = false;
  let generation = 0;
  let navigationIntent = 0;
  /** @type {Promise<boolean> | undefined} */
  let pending;
  /** @type {{ intent: number, promise: Promise<string | undefined> } | undefined} */
  let pendingSelection;
  let draftSentinel = '';
  let disposed = false;

  /** @param {string} draft */
  function captureDraft(draft) {
    if (typeof draft !== 'string') throw new TypeError('draft must be a string');
    if (index === -1) draftSentinel = draft;
  }

  function previous() {
    if (disposed) return Promise.resolve(undefined);
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

  function next(applySelection) {
    if (applySelection !== undefined && typeof applySelection !== 'function') {
      throw new TypeError('applySelection must be a function');
    }
    if (disposed) return undefined;
    let nextIndex = index;
    if (index > 0) {
      nextIndex = index - 1;
    } else if (index === 0) {
      nextIndex = -1;
    }
    const selected = nextIndex >= 0 ? entries[nextIndex].text : draftSentinel;
    if (applySelection) applySelection(selected);
    navigationIntent += 1;
    index = nextIndex;
    return selected;
  }

  function invalidate() {
    generation += 1;
    navigationIntent += 1;
    resetPageState();
    pending = undefined;
    pendingSelection = undefined;
  }

  function dispose() {
    disposed = true;
    generation += 1;
    navigationIntent += 1;
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

  /**
   * @param {number} requestGeneration
   * @param {boolean} staleRetried
   * @param {string} [requestCursor]
   * @param {string} [requestNonce]
   */
  async function loadPreviousPage(requestGeneration, staleRetried, requestCursor = cursor, requestNonce = nonce) {
    try {
      const response = await fetchPage({
        cwd: normalizedCwd,
        activeThreadId: normalizedActiveThreadId,
        cursor: requestCursor,
        nonce: requestNonce,
        limit: PROMPT_HISTORY_LIMIT,
      });
      if (requestGeneration !== generation) return false;
      assertPromptHistoryPage(response);
      entries = staleRetried ? [...response.entries] : entries.concat(response.entries);
      if (staleRetried) index = -1;
      cursor = response.nextCursor;
      nonce = response.nonce;
      hasMore = response.hasMore;
      loaded = true;
      return true;
    } catch (error) {
      if (requestGeneration !== generation) return false;
      if (!staleRetried && isStalePromptHistoryError(error)) {
        return loadPreviousPage(requestGeneration, true, '', '');
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

  return { captureDraft, previous, next, invalidate, dispose, snapshot };
}

/** @param {unknown} error */
function isStalePromptHistoryError(error) {
  if (!error || typeof error !== 'object') return false;
  const fields = /** @type {Record<string, unknown>} */ (error);
  const message = typeof fields.message === 'string' ? fields.message : '';
  return message === STALE_PROMPT_HISTORY_MESSAGE
    && typeof fields.code === 'number'
    && fields.code === STALE_PROMPT_HISTORY_CODE;
}

/**
 * @param {unknown} response
 * @returns {asserts response is PromptHistoryPage}
 */
function assertPromptHistoryPage(response) {
  if (!response || typeof response !== 'object'
    || !Array.isArray(/** @type {Record<string, unknown>} */ (response).entries)) {
    throw new TypeError('prompt history response is invalid');
  }
  const page = /** @type {Record<string, unknown> & { entries: unknown[] }} */ (response);
  if (page.entries.length > PROMPT_HISTORY_LIMIT
    || typeof page.nextCursor !== 'string'
    || typeof page.hasMore !== 'boolean'
    || typeof page.nonce !== 'string'
    || !page.nonce.trim()
    || (page.hasMore && !page.nextCursor)
    || (!page.hasMore && page.nextCursor !== '')) {
    throw new TypeError('prompt history response is invalid');
  }
  for (const entry of page.entries) {
    if (!entry || typeof entry !== 'object'
      || typeof /** @type {Record<string, unknown>} */ (entry).text !== 'string') {
      throw new TypeError('prompt history response is invalid');
    }
  }
}

export { PROMPT_HISTORY_LIMIT, STALE_PROMPT_HISTORY_CODE, STALE_PROMPT_HISTORY_MESSAGE };
