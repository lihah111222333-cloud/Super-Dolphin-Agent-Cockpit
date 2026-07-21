// @ts-check

export const THREAD_MESSAGES_PAGE_SIZE = 300;

/** @typedef {{hasMore: boolean, nextBefore: string, loading: boolean}} ThreadMessagesPagination */

/** @param {string} id @param {string} [before] */
export function messagePageParams(id, before) {
  if (before) return { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE, before };
  return { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE };
}

/** @param {unknown} res */
export function normalizeThreadMessagesPageMeta(res) {
  if (!res || typeof res !== 'object' || Array.isArray(res)) throw new TypeError('thread/messages response must be an object');
  const value = /** @type {Record<string, unknown>} */ (res);
  if (typeof value.hasMore !== 'boolean') throw new TypeError('thread/messages response hasMore must be a boolean');
  if (typeof value.nextBefore !== 'string') throw new TypeError('thread/messages response nextBefore must be a string');
  return {
    hasMore: value.hasMore,
    nextBefore: value.nextBefore,
  };
}

/** @param {ThreadMessagesPagination | undefined} current @param {Partial<ThreadMessagesPagination>} patch */
function threadMessagesPaginationEntry(current, patch) { return { ...(current || { hasMore: false, nextBefore: '', loading: false }), ...patch }; }

/** @param {{threadMessagePaginationByThread: Record<string, ThreadMessagesPagination>}} state @param {string} id @param {Partial<ThreadMessagesPagination>} [patch] */
export function threadMessagesPaginationPatch(state, id, patch = {}) {
  const currentByThread = state.threadMessagePaginationByThread;
  return {
    threadMessagePaginationByThread: {
      ...currentByThread,
      [id]: threadMessagesPaginationEntry(currentByThread[id], patch),
    },
  };
}
