// @ts-check

export const THREAD_MESSAGES_PAGE_SIZE = 300;

function normalizeString(value) {
  return (value || '').toString().trim();
}

function hasOwn(value, key) {
  return Boolean(value && Object.prototype.hasOwnProperty.call(value, key));
}

function normalizeThreadMessagesTotal(value) {
  if (value === null || value === undefined || value === '') return null;
  const total = Number(value);
  return Number.isFinite(total) && total >= 0 ? total : null;
}

function normalizeThreadMessagesBoolean(value) {
  if (typeof value === 'boolean') return value;
  if (typeof value === 'number') return value > 0;
  const normalized = normalizeString(value).toLowerCase();
  if (normalized === 'true') return true;
  if (normalized === 'false') return false;
  if (normalized === '1') return true;
  if (normalized === '0') return false;
  return Boolean(value);
}

function threadMessageNumericId(message) {
  const value = Number(message?.id);
  return Number.isSafeInteger(value) && value > 0 ? value : 0;
}

function oldestThreadMessageCursor(messages) {
  const ids = messages.map(threadMessageNumericId).filter((id) => id > 0);
  if (ids.length > 0) return String(Math.min(...ids));

  const timestamps = messages
    .map((message) => normalizeString(message?.createdAt || message?.created_at))
    .map((raw) => ({ raw, timestamp: Date.parse(raw) }))
    .filter(({ raw, timestamp }) => raw && Number.isFinite(timestamp) && timestamp > 0)
    .sort((left, right) => left.timestamp - right.timestamp);
  return timestamps[0]?.raw || '';
}

export function messagePageParams(id, before) {
  if (before) return { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE, before };
  return { threadId: id, limit: THREAD_MESSAGES_PAGE_SIZE };
}

export function normalizeThreadMessagesPageMeta(res, page) {
  const backendHasMore = hasOwn(res, 'hasMore') || hasOwn(res, 'has_more');
  const hasMore = backendHasMore
    ? normalizeThreadMessagesBoolean(res.hasMore ?? res.has_more)
    : (normalizeThreadMessagesTotal(res?.total) ?? page.length) > page.length || page.length >= THREAD_MESSAGES_PAGE_SIZE;
  const nextBefore = normalizeString(res?.nextBefore || res?.next_before);
  return {
    hasMore,
    nextBefore: hasMore ? nextBefore || (backendHasMore ? '' : oldestThreadMessageCursor(page)) : '',
  };
}

export function threadMessagesPaginationPatch(state, id, patch = {}) {
  return {
    threadMessagePaginationByThread: {
      ...state.threadMessagePaginationByThread,
      [id]: {
        ...(state.threadMessagePaginationByThread[id] || { hasMore: false, nextBefore: '', loading: false }),
        ...patch,
      },
    },
  };
}
