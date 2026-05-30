const DEFAULT_DRAFT_MODE = 'chat';

function normalizeMode(mode) {
  return (mode || DEFAULT_DRAFT_MODE).toString().trim() || DEFAULT_DRAFT_MODE;
}

function normalizeThreadId(threadId) {
  return (threadId || '').toString().trim();
}

function draftKey(threadId, mode) {
  const id = normalizeThreadId(threadId);
  return id ? `thread:${id}` : `new:${normalizeMode(mode)}`;
}

function cloneAttachments(attachments) {
  return Array.isArray(attachments) ? attachments.map((item) => ({ ...item })) : [];
}

function normalizeDraft(draft) {
  return {
    text: (draft?.text || '').toString(),
    attachments: cloneAttachments(draft?.attachments),
  };
}

function isEmptyDraft(draft) {
  return !(draft?.text || '').toString() && cloneAttachments(draft?.attachments).length === 0;
}

export function createComposerDraftController(state, logDebug = () => {}) {
  const drafts = new Map();
  let activeKey = draftKey('', DEFAULT_DRAFT_MODE);

  function readCurrentDraft() {
    return normalizeDraft(state);
  }

  function writeDraftToState(draft) {
    const next = normalizeDraft(draft);
    state.text = next.text;
    state.attachments = next.attachments;
  }

  function saveActiveDraft() {
    const draft = readCurrentDraft();
    if (isEmptyDraft(draft)) drafts.delete(activeKey);
    else drafts.set(activeKey, draft);
  }

  function activateDraft(threadId = '', mode = DEFAULT_DRAFT_MODE) {
    const nextKey = draftKey(threadId, mode);
    if (nextKey === activeKey) return nextKey;
    saveActiveDraft();
    activeKey = nextKey;
    writeDraftToState(drafts.get(activeKey));
    logDebug('composer', 'draft.activated', { draft_key: activeKey, draft_count: drafts.size });
    return activeKey;
  }

  function clearCurrentDraft() {
    state.text = '';
    state.attachments = [];
    drafts.delete(activeKey);
  }

  function clearDraft(threadId = '', mode = DEFAULT_DRAFT_MODE) {
    const key = draftKey(threadId, mode);
    drafts.delete(key);
    if (key === activeKey) clearCurrentDraft();
  }

  function restoreDraft(threadId = '', mode = DEFAULT_DRAFT_MODE, draft = {}) {
    const key = draftKey(threadId, mode);
    const next = normalizeDraft(draft);
    if (isEmptyDraft(next)) drafts.delete(key);
    else drafts.set(key, next);
    if (key === activeKey) writeDraftToState(next);
  }

  function resetDrafts() {
    drafts.clear();
    activeKey = draftKey('', DEFAULT_DRAFT_MODE);
    state.text = '';
    state.attachments = [];
  }

  return {
    activateDraft,
    clearCurrentDraft,
    clearDraft,
    restoreDraft,
    resetDrafts,
  };
}
