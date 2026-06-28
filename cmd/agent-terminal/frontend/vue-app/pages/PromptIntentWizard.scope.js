// @ts-nocheck
import {
  computed,
  ref,
  watch,
} from '../../lib/vue.esm-browser.prod.js';

export function usePromptIntentScope({ currentCard, selectedKind, currentDraftKey }) {
  const selectedScope = ref('project');
  const globalDefaultRuleConfirmation = ref(false);
  const globalDefaultRuleConfirmationDraftKey = ref('');

  const selectedScopeLabel = computed(() => (selectedScope.value === 'global' ? '所有项目' : '这个项目'));
  const selectedScopeNote = computed(() => (selectedScope.value === 'global'
    ? '说明：其他项目也可以使用；当前项目同名资产优先。'
    : '说明：只在当前项目的对话中使用。'));
  const requiresGlobalDefaultRuleConfirmation = computed(() => (
    selectedScope.value === 'global'
    && (currentCard.value?.kind || selectedKind.value) === 'default_rule'
  ));

  function resetGlobalDefaultRuleConfirmation() {
    globalDefaultRuleConfirmation.value = false;
    globalDefaultRuleConfirmationDraftKey.value = '';
  }

  function resetScope() {
    selectedScope.value = 'project';
    resetGlobalDefaultRuleConfirmation();
  }

  function setScope(scope) {
    selectedScope.value = scope === 'global' ? 'global' : 'project';
  }

  function markGlobalDefaultRuleConfirmed(next) {
    globalDefaultRuleConfirmation.value = !!next;
    globalDefaultRuleConfirmationDraftKey.value = next ? currentDraftKey.value : '';
  }

  function globalDefaultRuleConfirmedForCurrentDraft() {
    return globalDefaultRuleConfirmation.value
      && globalDefaultRuleConfirmationDraftKey.value === currentDraftKey.value;
  }

  function applyScopeToCommitPayload(payload) {
    if (selectedScope.value !== 'global') return payload;
    payload.enable_global = true;
    payload.confirm_global = true;
    return payload;
  }

  function applyScopeToDraftPayload(payload) {
    if (selectedScope.value !== 'global') return payload;
    payload.enable_global = true;
    return payload;
  }

  watch(selectedScope, resetGlobalDefaultRuleConfirmation);

  return {
    selectedScope,
    selectedScopeLabel,
    selectedScopeNote,
    globalDefaultRuleConfirmation,
    globalDefaultRuleConfirmationDraftKey,
    requiresGlobalDefaultRuleConfirmation,
    setScope,
    resetScope,
    resetGlobalDefaultRuleConfirmation,
    markGlobalDefaultRuleConfirmed,
    globalDefaultRuleConfirmedForCurrentDraft,
    applyScopeToCommitPayload,
    applyScopeToDraftPayload,
  };
}
