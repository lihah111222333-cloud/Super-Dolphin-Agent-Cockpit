// @ts-nocheck

export function buildComposerTextFromCitation(payload) {
  const kind = (payload?.kind || '').toString().trim();
  const raw = (payload?.raw || '').toString().trim();
  if (kind === 'task') {
    return (payload?.prompt || payload?.title || raw || '').toString().trim();
  }
  if (kind === 'code-comment') {
    const title = (payload?.title || '').toString().trim();
    const message = (payload?.message || raw || '').toString().trim();
    const path = (payload?.path || '').toString().trim();
    const header = title || path ? `Code comment${path ? ` (${path})` : ''}${title ? `: ${title}` : ''}` : 'Code comment';
    if (message) return `${header}\n${message}`;
    return header === 'Code comment' ? '' : header;
  }
  if (kind === 'automation-update') {
    const title = (payload?.title || '').toString().trim();
    const prompt = (payload?.prompt || '').toString().trim();
    const message = (payload?.message || raw || '').toString().trim();
    if (title && prompt) return `Automation update (${title}):\n${prompt}`;
    if (prompt) return `Automation update:\n${prompt}`;
    if (message) return `Automation update:\n${message}`;
    return title ? `Automation update (${title})` : '';
  }
  return '';
}

export function applyComposerCitationAction(composer, payload) {
  const nextText = buildComposerTextFromCitation(payload);
  if (!nextText || !composer?.state) return false;
  const current = (composer.state.text || '').toString().trim();
  composer.state.text = current ? `${current}\n\n${nextText}` : nextText;
  return true;
}

export function handleTimelineCitationClick({
  payload,
  fileRefPreview,
  threads = [],
  selectThread,
  composer,
  scheduleScrollToBottom,
  logInfo,
}) {
  const kind = (payload?.kind || '').toString().trim();
  if (!kind) return;
  if (kind === 'image') {
    fileRefPreview?.onTimelineCitationClick?.(payload);
    return;
  }
  if (kind === 'conversation') {
    const nextThreadId = (payload?.conversationId || '').toString().trim();
    if (nextThreadId && Array.isArray(threads) && threads.some((item) => item?.id === nextThreadId)) selectThread?.(nextThreadId);
    return;
  }
  if (kind === 'skill') {
    const skillPath = (payload?.path || '').toString().trim();
    if (skillPath) fileRefPreview?.onTimelineFileRefClick?.({ path: skillPath, line: 1, column: 0, raw: payload?.raw || '' });
    return;
  }
  const path = kind === 'code-comment' ? (payload?.path || '').toString().trim() : '';
  if (path) fileRefPreview?.onTimelineFileRefClick?.({ path, line: Number(payload?.lineStart) || 1, column: 0, raw: payload?.raw || '' });
  if (applyComposerCitationAction(composer, payload)) {
    logInfo?.('ui', 'chat.citation.action.applied', { kind });
    scheduleScrollToBottom?.(false);
  }
}
