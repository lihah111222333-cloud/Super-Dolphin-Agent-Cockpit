// @ts-nocheck
import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';
import { withCwd } from './SystemPromptPage.helpers.js';

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

export function pendingDraftPayload(item) {
  const draftKey = (item?.draftKey || item?.draft_key || item?.id || '').toString().trim();
  const kind = (item?.assetType || item?.kind || 'expert').toString();
  const scope = (item?.scope || item?.Scope || '').toString().trim().toLowerCase() === 'global'
    ? 'global'
    : 'project';
  return {
    draft_key: draftKey,
    kind,
    scope,
    status: (item?.draftStatus || item?.draft_status || 'ready_to_save').toString(),
    card: item?.card || {
      kind,
      title: (item?.name || '').toString(),
      summary: (item?.description || '').toString(),
      output: (item?.content || '').toString(),
      hit_examples: [],
      miss_examples: [],
    },
    issues: Array.isArray(item?.issues) ? item.issues : [],
  };
}

export function createPendingDraftActions(deps) {
  const {
    getCwd, fallbackMode, deletingId, intentWizardOpen, pendingDraftForWizard,
    loadPrompts, setNotice, setReadonlyActionNotice,
  } = deps;

  function continuePendingDraft(item) {
    if (fallbackMode.value) { setReadonlyActionNotice('继续确认'); return; }
    if (!getCwd()) { setNotice('error', '当前作用域未确定，无法继续确认草稿'); return; }
    pendingDraftForWizard.value = pendingDraftPayload(item);
    intentWizardOpen.value = true;
    setNotice('info', '');
  }

  async function discardPendingDraft(item) {
    if (fallbackMode.value) { setReadonlyActionNotice('丢弃'); return; }
    if (!getCwd()) { setNotice('error', '当前作用域未确定，无法丢弃草稿'); return; }
    const draftKey = (item?.draftKey || item?.draft_key || item?.id || '').toString().trim();
    if (!draftKey || deletingId.value) return;
    deletingId.value = draftKey;
    try {
      await callAPI('prompt-intents/discard', withCwd(getCwd(), { draft_key: draftKey }));
      await loadPrompts();
      setNotice('info', `已丢弃：${item?.name || ''}`);
    } catch (error) {
      logWarn('system-prompt', 'discard_draft.failed', { error });
      setNotice('error', `丢弃失败：${toErrorMessage(error)}`);
    } finally {
      deletingId.value = '';
    }
  }

  return { continuePendingDraft, discardPendingDraft };
}
