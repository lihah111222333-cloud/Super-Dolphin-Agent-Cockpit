// @ts-nocheck
import { callAPI } from '../services/api.js';
import { logDebug, logWarn } from '../services/log.js';
import { canForceLaunchPrompt } from './SystemPromptPage.helpers.js';

export const PREF_KEY_ACTIVE_PROMPT = 'settings.activePromptKey';

async function prefGet(cwd, key, scope, fallback = null) {
  if (!cwd) return fallback;
  try {
    return await callAPI('ui/preferences/get', { key, cwd });
  } catch (error) {
    logDebug('system-prompt', scope + '.load.failed', { error });
    return null;
  }
}

async function prefSet(cwd, key, value) {
  await callAPI('ui/preferences/set', { key, value, cwd });
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

export function createLaunchPromptActions(deps) {
  const { getCwd, fallbackMode, promptCards, activePromptId, activatingId, setNotice, setReadonlyActionNotice } = deps;
  async function clearStaleActivePromptId(staleId) {
    activePromptId.value = '';
    try {
      const cwd = getCwd();
      if (cwd) await prefSet(cwd, PREF_KEY_ACTIVE_PROMPT, '');
    } catch (error) {
      logWarn('system-prompt', 'active.stale_clear.failed', { error, staleId });
    }
    return '';
  }
  async function sanitizeActivePromptId() {
    const id = (activePromptId.value || '').toString().trim();
    if (!id) return '';
    const cards = Array.isArray(promptCards?.value) ? promptCards.value : [];
    if (cards.length === 0) return id;
    const launchable = cards.some(card => (card?.id || '').toString() === id && canForceLaunchPrompt(card));
    return launchable ? id : clearStaleActivePromptId(id);
  }
  async function loadActivePromptId() {
    const raw = await prefGet(getCwd(), PREF_KEY_ACTIVE_PROMPT, 'active', '');
    if (raw === null) return sanitizeActivePromptId();
    activePromptId.value = (typeof raw === 'string' ? raw : '').trim();
    return sanitizeActivePromptId();
  }
  async function applyActivePromptId(nextId, successMessage) {
    const cwd = getCwd();
    if (!cwd) { setNotice('error', '当前作用域未确定，无法记录强制使用'); return; }
    try {
      await prefSet(cwd, PREF_KEY_ACTIVE_PROMPT, (nextId || '').toString());
      activePromptId.value = (nextId || '').toString();
      setNotice('info', successMessage);
    } catch (error) {
      logWarn('system-prompt', 'active.persist.failed', { error });
      setNotice('error', `设置强制使用失败：${toErrorMessage(error)}`);
    }
  }
  async function setLaunchPrompt(item) {
    if (fallbackMode.value) { setReadonlyActionNotice('强制使用'); return; }
    if (item?.isPendingDraft) { setNotice('info', '这条草稿还在待确认，确认保存后才能强制使用'); return; }
    if (!canForceLaunchPrompt(item)) { setNotice('info', '只有启用中的专家能力可以强制使用'); return; }
    const id = (item?.id || '').toString();
    if (!id || activatingId.value) return;
    activatingId.value = id;
    try { await applyActivePromptId(id, `已设为强制使用：${item?.name || id}`); }
    finally { activatingId.value = ''; }
  }
  async function clearLaunchPrompt() {
    if (fallbackMode.value) { setReadonlyActionNotice('取消强制'); return; }
    if (activatingId.value) return;
    activatingId.value = 'clear';
    try { await applyActivePromptId('', '已取消强制使用，新对话将使用默认路由'); }
    finally { activatingId.value = ''; }
  }
  return { loadActivePromptId, setLaunchPrompt, clearLaunchPrompt, sanitizeActivePromptId };
}
