// @ts-nocheck

export function getThreadSendBlockedNoticeFromState(state, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  const notices = state?.sendBlockedNoticesByThread;
  return notices && typeof notices === 'object' ? (notices[id] || '').toString() : '';
}

export function getThreadSendHoldNoticeFromState(state, threadId) {
  const id = (threadId || '').toString().trim();
  if (!id) return '';
  const notices = state?.sendHoldNoticesByThread;
  return notices && typeof notices === 'object' ? (notices[id] || '').toString() : '';
}

export function isThreadSendBlockedInState(state, threadId) {
  return Boolean(getThreadSendBlockedNoticeFromState(state, threadId));
}

export function isThreadSendHeldInState(state, threadId) {
  return Boolean(getThreadSendHoldNoticeFromState(state, threadId));
}

export function assertThreadCanSendInState(state, threadId) {
  const blockedNotice = getThreadSendBlockedNoticeFromState(state, threadId);
  if (blockedNotice) throw new Error(blockedNotice);
  const holdNotice = getThreadSendHoldNoticeFromState(state, threadId);
  if (holdNotice) throw new Error(holdNotice);
}

export function clearThreadSendBlockedNoticeInState(state, threadId) {
  const id = (threadId || '').toString().trim();
  const notices = state?.sendBlockedNoticesByThread;
  if (!id || !notices || typeof notices !== 'object' || !Object.prototype.hasOwnProperty.call(notices, id)) return;
  const next = { ...notices };
  delete next[id];
  state.sendBlockedNoticesByThread = next;
}

export function clearThreadSendHoldNoticeInState(state, threadId) {
  const id = (threadId || '').toString().trim();
  const notices = state?.sendHoldNoticesByThread;
  if (!id || !notices || typeof notices !== 'object' || !Object.prototype.hasOwnProperty.call(notices, id)) return;
  const next = { ...notices };
  delete next[id];
  state.sendHoldNoticesByThread = next;
}

export function clearThreadSendNoticesInState(state, threadId) {
  clearThreadSendBlockedNoticeInState(state, threadId);
  clearThreadSendHoldNoticeInState(state, threadId);
}

export function clearThreadSendHoldNoticesInState(state) {
  if (!state || !state.sendHoldNoticesByThread || Object.keys(state.sendHoldNoticesByThread).length === 0) return;
  state.sendHoldNoticesByThread = {};
}

export function setThreadSendBlockedNoticeFromError(state, threadId, error) {
  const id = (threadId || '').toString().trim();
  if (!id || !state) return;
  const detail = ((error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '') || String(error || '')).toString().trim();
  const notice = detail ? `发送失败：${detail}` : '发送失败：后端未返回错误详情';
  state.sendBlockedNoticesByThread = { ...(state.sendBlockedNoticesByThread || {}), [id]: notice };
}

export function setThreadSendHoldNoticeFromError(state, threadId, error) {
  const id = (threadId || '').toString().trim();
  if (!id || !state) return;
  const detail = ((error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '') || String(error || '')).toString().trim();
  const notice = detail
    ? `发送状态同步失败：${detail}。请刷新会话状态后继续。`
    : '发送状态同步失败。请刷新会话状态后继续。';
  state.sendHoldNoticesByThread = { ...(state.sendHoldNoticesByThread || {}), [id]: notice };
}

export async function callTurnStartWithSendBlock(ctx, threadId, requestPayload, isRecoverableSessionError, recoverThread) {
  try {
    return await ctx.callAPI('turn/start', requestPayload);
  } catch (turnError) {
    if (!isRecoverableSessionError(turnError)) {
      setThreadSendBlockedNoticeFromError(ctx.state, threadId, turnError);
      throw turnError;
    }
    if (typeof ctx.logWarn === 'function') {
      ctx.logWarn('thread', 'send.auto_recover', { thread_id: threadId, error: turnError });
    }
    try {
      await recoverThread(ctx, threadId);
      return await ctx.callAPI('turn/start', requestPayload);
    } catch (retryError) {
      setThreadSendBlockedNoticeFromError(ctx.state, threadId, retryError);
      throw retryError;
    }
  }
}
