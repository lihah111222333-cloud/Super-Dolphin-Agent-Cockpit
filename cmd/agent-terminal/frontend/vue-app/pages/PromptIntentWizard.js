// @ts-nocheck
import {
  ref,
  computed,
  watch,
} from '../../lib/vue.esm-browser.prod.js';

import { callAPI } from '../services/api.js';
import { logWarn } from '../services/log.js';
import { usePromptIntentFileDrop } from '../composables/usePromptIntentFileDrop.js';
import { usePromptIntentScope } from './PromptIntentWizard.scope.js';
import {
  PROMPT_INTENT_TYPES,
  intentTypeLabel,
  intentTypePlaceholder,
  toErrorMessage,
  normalizeIntentDraftResponse,
  hasBlockIssues,
  hasReviewIssues,
  draftExamplesReady,
  suggestedAlternativeView,
} from './PromptIntentWizard.helpers.js';

function withCwd(cwd, payload) {
  return cwd ? { ...payload, cwd } : payload;
}

function dryRunTargetLabel(action) {
  switch ((action || '').toString().trim()) {
    case 'prompt_recall':
      return '这份资料';
    case 'launch_agent':
      return '这项专家能力';
    case 'default_rule':
      return '这条默认规则';
    default:
      return '这份草稿';
  }
}

function userDryRunReason(reason) {
  const text = (reason || '').toString().trim();
  if (!text || /^question provided:/i.test(text)) return '';
  if (/\b(prompt_recall|launch_agent|default_rule|candidate|draft_key)\b/.test(text)) return '';
  return text;
}

function dryRunSummary(result) {
  if (!result || typeof result !== 'object') return '';
  const action = (result.action || '').toString().trim();
  const target = (result.target || '').toString().trim();
  const reasons = Array.isArray(result.reasons)
    ? result.reasons.map(userDryRunReason).filter(Boolean)
    : [];
  if (action || target || reasons.length > 0) {
    const label = dryRunTargetLabel(action);
    const parts = [];
    const prefix = result.would_use === false ? '这个问题通常不会触发' : '这个问题会触发';
    parts.push(`${prefix}${label}${target ? `「${target}」` : ''}。`);
    if (reasons.length > 0) parts.push(`判断依据：${reasons.join('；')}。`);
    parts.push('保存后，AI 会在类似问题中看到这份内容；真实回答仍会受当前上下文影响。');
    return parts.join('');
  }
  return (
    result.answer
    || result.summary
    || result.explanation
    || result.message
    || ''
  ).toString();
}

function cardWhenToUse(card) {
  return (card?.when_to_use || card?.whenToUse || '').toString();
}

function cardWhenNotToUse(card) {
  return (card?.when_not_to_use || card?.whenNotToUse || '').toString();
}

function cardSaveBoundary(card) {
  return (card?.save_boundary || card?.saveBoundary || '').toString();
}

function cardPreviewText(card) {
  return (card?.recall_body || card?.recallBody || card?.default_rule_body || card?.defaultRuleBody || card?.output || '').toString();
}

function originalDraftInput(ctx) {
  return ((ctx.rawInput.value || '').trim() || (ctx.lastDraftRawInput.value || '').trim());
}

function reviewStateForDraft(item) {
  return hasBlockIssues(item?.issues) || item?.status === 'draft_blocked'
    ? 'draft_blocked'
    : 'review';
}

function activateDraftOption(ctx, option) {
  if (!option || typeof option !== 'object') return;
  ctx.hydratingInitialDraft.value = true;
  ctx.draft.value = { ...option, draft_options: ctx.options };
  ctx.selectedKind.value = option.inferred_kind || option.kind || ctx.selectedKind.value;
  ctx.dryRunQuestion.value = '';
  ctx.dryRunResult.value = null;
  ctx.resetConfirmation();
  ctx.state.value = reviewStateForDraft(option);
  queueMicrotask(() => { ctx.hydratingInitialDraft.value = false; });
}

async function runPromptIntentDryRun(ctx) {
  if (ctx.props.fallbackMode || !ctx.hasResolvedCwd.value || !ctx.currentDraftKey.value || ctx.state.value === 'dry_running') return;
  const question = (ctx.dryRunQuestion.value || '').trim();
  if (!question) {
    ctx.showNotice('请先输入一个想验证的问题', 'error');
    return;
  }
  const previousState = ctx.state.value;
  const draftKey = ctx.currentDraftKey.value;
  ctx.state.value = 'dry_running';
  ctx.notice.value = '';
  try {
    const result = await callAPI('prompt-intents/dry-run', withCwd(ctx.props.cwd, {
      draft_key: draftKey,
      kind: ctx.selectedKind.value,
      card: ctx.currentCard.value,
      question,
    }));
    if (ctx.currentDraftKey.value !== draftKey) return;
    ctx.dryRunResult.value = result;
    ctx.state.value = previousState === 'draft_blocked' ? 'draft_blocked' : 'review';
  } catch (error) {
    logWarn('prompt-intent-wizard', 'dry_run.failed', { error });
    if (ctx.currentDraftKey.value !== draftKey) return;
    ctx.state.value = previousState;
    ctx.showNotice(`验证失败：${toErrorMessage(error)}`, 'error');
  }
}

export const PromptIntentWizard = {
  name: 'PromptIntentWizard',
  props: {
    cwd: { type: String, default: '' },
    visible: { type: Boolean, default: false },
    fallbackMode: { type: Boolean, default: false },
    initialDraft: { type: Object, default: null },
  },
  emits: ['close', 'saved', 'drafted'],
  setup(props, { emit }) {
    const selectedKind = ref('expert');
    const rawInput = ref('');
    const state = ref('editing');
    const draft = ref(null);
    const lastDraftRawInput = ref('');
    const reviewConfirmation = ref(false);
    const confirmationDraftKey = ref('');
    const dryRunQuestion = ref('');
    const dryRunResult = ref(null);
    const notice = ref('');
    const noticeLevel = ref('error');
    const hydratingInitialDraft = ref(false);

    const selectedPlaceholder = computed(() => intentTypePlaceholder(selectedKind.value));
    const reviewIssues = computed(() => draft.value?.issues || []);
    const currentCard = computed(() => draft.value?.card || {});
    const currentDraftKey = computed(() => (draft.value?.draft_key || '').toString());
    const draftOptions = computed(() => (
      Array.isArray(draft.value?.draft_options) ? draft.value.draft_options : []
    ));
    const suggestedAlternative = computed(() => suggestedAlternativeView(currentCard.value, draft.value?.requested_kind || selectedKind.value));
    const hasBlocks = computed(() => hasBlockIssues(reviewIssues.value));
    const hasReviews = computed(() => hasReviewIssues(reviewIssues.value, currentCard.value));
    const examplesReady = computed(() => draftExamplesReady(currentCard.value));
    const canShowDraft = computed(() => ['review', 'draft_blocked', 'dry_running'].includes(state.value));
    const scopeState = usePromptIntentScope({ currentCard, selectedKind, currentDraftKey });
    const hasResolvedCwd = computed(() => {
      const cwd = (props.cwd || '').toString().trim();
      return Boolean(cwd && cwd !== '.');
    });
    const canDraft = computed(() => !props.fallbackMode && hasResolvedCwd.value && !['drafting', 'dry_running', 'committing'].includes(state.value));
    const canApplySuggestedAlternative = computed(() => (
      Boolean(suggestedAlternative.value?.kind)
      && !props.fallbackMode
      && hasResolvedCwd.value
      && Boolean(originalDraftInput({ rawInput, lastDraftRawInput }))
      && !['drafting', 'dry_running', 'committing'].includes(state.value)
    ));
    const canSave = computed(() => (
      !props.fallbackMode
      && hasResolvedCwd.value
      && state.value === 'review'
      && currentDraftKey.value
      && examplesReady.value
      && !hasBlocks.value
      && (!hasReviews.value || (reviewConfirmation.value && confirmationDraftKey.value === currentDraftKey.value))
      && (!scopeState.requiresGlobalDefaultRuleConfirmation.value || scopeState.globalDefaultRuleConfirmedForCurrentDraft())
    ));
    const fileDrop = usePromptIntentFileDrop({
      props,
      state,
      rawInput,
      notice,
      noticeLevel,
    });

    function showNotice(message, level = 'error') {
      noticeLevel.value = level;
      notice.value = message;
    }

    function isScopeSelectionDisabled() {
      return state.value === 'drafting'
        || state.value === 'committing'
        || props.fallbackMode
        || !hasResolvedCwd.value
        || Boolean(currentDraftKey.value);
    }

    function setError(error, prefix) {
      const msg = toErrorMessage(error);
      showNotice(`${prefix}：${msg}`, 'error');
      state.value = 'error';
    }

    function resetConfirmation() {
      reviewConfirmation.value = false;
      confirmationDraftKey.value = '';
      scopeState.resetGlobalDefaultRuleConfirmation();
    }

    function clearDraftArtifacts(nextState = 'editing') {
      draft.value = null;
      lastDraftRawInput.value = '';
      dryRunQuestion.value = '';
      dryRunResult.value = null;
      resetConfirmation();
      state.value = nextState;
    }

    function resetAll() {
      selectedKind.value = 'expert';
      scopeState.resetScope();
      rawInput.value = '';
      notice.value = '';
      noticeLevel.value = 'error';
      clearDraftArtifacts('editing');
    }

    function loadInitialDraft(raw) {
      if (!raw || typeof raw !== 'object') return;
      const normalized = normalizeIntentDraftResponse(raw, selectedKind.value);
      if (!normalized.draft_key) return;
      hydratingInitialDraft.value = true;
      selectedKind.value = normalized.kind || 'expert';
      scopeState.setScope(normalized.scope || 'project');
      draft.value = normalized;
      rawInput.value = '';
      lastDraftRawInput.value = '';
      dryRunQuestion.value = '';
      dryRunResult.value = null;
      notice.value = '';
      noticeLevel.value = 'error';
      resetConfirmation();
      state.value = reviewStateForDraft(normalized);
      queueMicrotask(() => { hydratingInitialDraft.value = false; });
    }

    const selectDraftOption = option => activateDraftOption({ draft, selectedKind, dryRunQuestion, dryRunResult, resetConfirmation, state, hydratingInitialDraft, options: draftOptions.value }, option);

    function closeWizard() {
      resetAll();
      emit('close');
    }

    async function draftIntentWithKind(kind, requireCanDraft) {
      if (props.fallbackMode || !hasResolvedCwd.value) return;
      if (requireCanDraft && !canDraft.value) return;
      if (!requireCanDraft && ['drafting', 'dry_running', 'committing'].includes(state.value)) return;
      const text = originalDraftInput({ rawInput, lastDraftRawInput });
      if (!text) {
        showNotice('请先写下希望 AI 记住或使用的内容', 'error');
        return;
      }
      state.value = 'drafting';
      notice.value = '';
      clearDraftArtifacts('drafting');
      try {
        const payload = withCwd(props.cwd, {
          kind,
          raw_input: text,
          source_type: 'user_input',
        });
        scopeState.applyScopeToDraftPayload(payload);
        const res = await callAPI('prompt-intents/draft', payload);
        const normalized = normalizeIntentDraftResponse(res, kind);
        hydratingInitialDraft.value = true;
        selectedKind.value = normalized.inferred_kind || normalized.kind || kind;
        draft.value = normalized;
        lastDraftRawInput.value = text;
        resetConfirmation();
        state.value = reviewStateForDraft(normalized);
        emit('drafted', normalized);
        queueMicrotask(() => { hydratingInitialDraft.value = false; });
      } catch (error) {
        hydratingInitialDraft.value = false;
        logWarn('prompt-intent-wizard', 'draft.failed', { error });
        setError(error, '整理失败');
      }
    }

    const draftIntent = () => draftIntentWithKind(selectedKind.value, true);
    const applySuggestedAlternative = () => (suggestedAlternative.value?.kind && canApplySuggestedAlternative.value
      ? draftIntentWithKind(suggestedAlternative.value.kind, false)
      : undefined);

    const runDryRun = () => runPromptIntentDryRun({ props, hasResolvedCwd, currentDraftKey, state, dryRunQuestion, showNotice, notice, selectedKind, currentCard, dryRunResult });

    async function commitIntent() {
      if (!canSave.value) return;
      const payload = withCwd(props.cwd, { draft_key: currentDraftKey.value });
      scopeState.applyScopeToCommitPayload(payload);
      if (hasReviews.value && reviewConfirmation.value && confirmationDraftKey.value === currentDraftKey.value) {
        payload.confirm_risk = true;
      }
      state.value = 'committing';
      notice.value = '';
      try {
        const res = await callAPI('prompt-intents/commit', payload);
        state.value = 'done';
        resetConfirmation();
        emit('saved', res);
        resetAll();
      } catch (error) {
        logWarn('prompt-intent-wizard', 'commit.failed', { error });
        resetConfirmation();
        setError(error, '保存失败');
      }
    }

    function markReviewConfirmed(next) {
      reviewConfirmation.value = !!next;
      confirmationDraftKey.value = next ? currentDraftKey.value : '';
    }

    watch(selectedKind, () => {
      if (hydratingInitialDraft.value) return;
      clearDraftArtifacts('editing');
      notice.value = '';
    });

    watch(rawInput, (next) => {
      if (!draft.value) return;
      if ((next || '').trim() === lastDraftRawInput.value) return;
      clearDraftArtifacts('editing');
      notice.value = '';
    });

    watch(() => props.visible, (visible) => {
      resetConfirmation();
      if (!visible) {
        resetAll();
        return;
      }
      loadInitialDraft(props.initialDraft);
    });

    watch(() => props.initialDraft, (next) => {
      if (props.visible) loadInitialDraft(next);
    }, { immediate: true });

    watch(currentDraftKey, () => resetConfirmation());

    return {
      PROMPT_INTENT_TYPES,
      selectedKind,
      rawInput,
      state,
      draft,
      reviewConfirmation,
      confirmationDraftKey,
      dryRunQuestion,
      dryRunResult,
      notice,
      noticeLevel,
      selectedPlaceholder,
      reviewIssues,
      currentCard,
      currentDraftKey,
      draftOptions,
      suggestedAlternative,
      hasBlocks,
      hasReviews,
      examplesReady,
      canShowDraft,
      hasResolvedCwd,
      canDraft,
      canSave,
      canApplySuggestedAlternative,
      intentTypeLabel,
      dryRunSummary,
      cardWhenToUse,
      cardWhenNotToUse,
      cardSaveBoundary,
      cardPreviewText,
      isScopeSelectionDisabled,
      resetAll,
      loadInitialDraft,
      selectDraftOption,
      applySuggestedAlternative,
      closeWizard,
      draftIntent,
      runDryRun,
      commitIntent,
      markReviewConfirmed,
      ...scopeState,
      ...fileDrop,
    };
  },
  template: `
    <div
      v-if="visible"
      class="modal-overlay sp-intent-overlay"
      data-testid="sp-intent-wizard"
      @click.self="closeWizard"
      @keydown.esc.prevent="closeWizard"
    >
      <div class="modal-box sp-intent-wizard" role="dialog" aria-modal="true">
        <div class="sp-intent-head">
          <div>
            <div class="modal-title">新建提示词</div>
            <div class="sp-intent-scope" data-testid="sp-intent-scope">可用范围：{{ selectedScopeLabel }}</div>
          </div>
          <button class="btn btn-ghost" data-testid="sp-intent-close" @click="closeWizard">关闭</button>
        </div>

        <div class="sp-intent-body">
          <div class="sp-intent-type-tabs" data-testid="sp-intent-type-tabs">
            <button
              v-for="type in PROMPT_INTENT_TYPES"
              :key="type.key"
              type="button"
              class="sp-intent-type-tab"
              :class="{ 'is-active': selectedKind === type.key }"
              :data-testid="'sp-intent-type-' + type.key"
              :disabled="state === 'drafting' || state === 'committing' || fallbackMode || !hasResolvedCwd"
              @click="selectedKind = type.key"
            >{{ type.label }}</button>
          </div>

          <div class="sp-scope-segmented sp-intent-scope-picker" data-testid="sp-intent-scope-group">
            <label class="sp-scope-option" :class="{ active: selectedScope === 'project' }">
              <input
                type="radio"
                value="project"
                v-model="selectedScope"
                data-testid="sp-intent-scope-project"
                :disabled="isScopeSelectionDisabled()"
              />
              <span>这个项目</span>
            </label>
            <label class="sp-scope-option" :class="{ active: selectedScope === 'global' }">
              <input
                type="radio"
                value="global"
                v-model="selectedScope"
                data-testid="sp-intent-scope-global"
                :disabled="isScopeSelectionDisabled()"
              />
              <span>所有项目</span>
            </label>
          </div>

          <div class="sp-field">
            <label>你想让 AI 记住或使用什么？</label>
            <div
              id="prompt-intent-drop-zone"
              class="sp-intent-drop-zone"
              :class="{ 'is-drop-active': dropActive }"
              data-file-drop-target=""
              @dragenter="onDragEnter"
              @dragover="onDragOver"
              @dragleave="onDragLeave"
              @drop="onDrop"
            >
              <textarea
                class="sp-textarea sp-intent-raw-input"
                data-testid="sp-intent-raw-input"
                rows="7"
                v-model="rawInput"
                :placeholder="selectedPlaceholder"
                :disabled="state === 'drafting' || state === 'committing' || fallbackMode"
              ></textarea>
              <div v-if="dropActive" class="sp-intent-drop-hint" aria-live="polite">松开读取文档、表格或文本资料</div>
            </div>
          </div>

          <div class="sp-intent-scope-note" data-testid="sp-intent-scope-note">{{ selectedScopeNote }}</div>

          <div class="sp-intent-actions">
            <button
              type="button"
              class="btn btn-primary sp-intent-primary-action"
              data-testid="sp-intent-draft-btn"
              :disabled="!canDraft || fallbackMode"
              @click="draftIntent"
            >{{ state === 'drafting' ? '整理中...' : '整理' }}</button>
          </div>

          <section v-if="canShowDraft" class="sp-intent-confirmation" data-testid="sp-intent-confirmation">
            <div v-if="draftOptions.length > 1" class="sp-intent-draft-options" data-testid="sp-intent-draft-options">
              <button
                v-for="option in draftOptions"
                :key="option.draft_key"
                type="button"
                class="sp-intent-draft-option"
                :class="{ 'is-active': option.draft_key === currentDraftKey }"
                :data-testid="'sp-intent-draft-option-' + option.draft_key"
                :disabled="state === 'dry_running'"
                @click="selectDraftOption(option)"
              >
                <span>{{ intentTypeLabel(option.inferred_kind || option.kind) }}</span>
                <strong>{{ (option.card && option.card.title) || '待确认草稿' }}</strong>
              </button>
            </div>

            <div class="sp-intent-confirmation-head">
              <span class="sp-intent-kind">{{ intentTypeLabel(currentCard.kind || selectedKind) }}</span>
              <strong data-testid="sp-intent-card-title">{{ currentCard.title || '待确认草稿' }}</strong>
            </div>
            <p v-if="currentCard.summary" class="sp-intent-summary" data-testid="sp-intent-card-summary">{{ currentCard.summary }}</p>
            <p v-if="cardWhenToUse(currentCard)" class="sp-intent-copy">{{ cardWhenToUse(currentCard) }}</p>
            <p v-if="cardWhenNotToUse(currentCard)" class="sp-intent-copy">{{ cardWhenNotToUse(currentCard) }}</p>
            <p v-if="cardSaveBoundary(currentCard)" class="sp-intent-copy" data-testid="sp-intent-save-boundary">保存边界：{{ cardSaveBoundary(currentCard) }}</p>
            <ol v-if="currentCard.workflow && currentCard.workflow.length" class="sp-intent-list">
              <li v-for="step in currentCard.workflow" :key="step">{{ step }}</li>
            </ol>
            <div v-if="cardPreviewText(currentCard)" class="sp-intent-preview" data-testid="sp-intent-card-preview">
              {{ cardPreviewText(currentCard) }}
            </div>

            <div class="sp-intent-examples" data-testid="sp-intent-examples">
              <div>
                <div class="sp-intent-examples-title">适合的问题</div>
                <ul><li v-for="example in currentCard.hit_examples" :key="'hit-' + example">{{ example }}</li></ul>
              </div>
              <div>
                <div class="sp-intent-examples-title">不适合的问题</div>
                <ul><li v-for="example in currentCard.miss_examples" :key="'miss-' + example">{{ example }}</li></ul>
              </div>
            </div>

            <div v-if="(currentCard.kind || selectedKind) === 'default_rule'" class="sp-intent-default-rule" data-testid="sp-intent-default-rule-review">
              <div class="sp-intent-examples-title">可能冲突的已有规则</div>
              <ul v-if="currentCard.conflicting_rules && currentCard.conflicting_rules.length">
                <li v-for="rule in currentCard.conflicting_rules" :key="rule.title || rule.summary">
                  <strong>{{ rule.title || '未命名规则' }}</strong>
                  <span v-if="rule.summary">：{{ rule.summary }}</span>
                </li>
              </ul>
              <p v-else>未发现明显冲突</p>
            </div>

            <div v-if="suggestedAlternative" class="sp-intent-optimization" data-testid="sp-intent-optimization">
              <div>
                <div class="sp-intent-examples-title">{{ suggestedAlternative.title }}</div>
                <p>{{ suggestedAlternative.body }}</p>
                <p v-if="suggestedAlternative.reason">原因：{{ suggestedAlternative.reason }}</p>
              </div>
              <button
                v-if="canApplySuggestedAlternative"
                type="button"
                class="btn btn-secondary"
                data-testid="sp-intent-apply-suggested-alternative"
                :disabled="state === 'drafting'"
                @click="applySuggestedAlternative"
              >按这个方向重新整理</button>
            </div>

            <div v-if="reviewIssues.length" class="sp-intent-issues" data-testid="sp-intent-issues">
              <div
                v-for="issue in reviewIssues"
                :key="issue.code + issue.message"
                class="sp-intent-issue"
                :class="'sp-intent-issue--' + issue.severity"
              >{{ issue.message }}</div>
            </div>

            <label v-if="hasReviews && !hasBlocks" class="sp-intent-review-confirm" data-testid="sp-intent-review-confirm">
              <input
                type="checkbox"
                :checked="reviewConfirmation && confirmationDraftKey === currentDraftKey"
                @change="markReviewConfirmed($event.target.checked)"
              />
              <span>我已确认这些风险，仍要保存</span>
            </label>

            <label v-if="requiresGlobalDefaultRuleConfirmation && !hasBlocks" class="sp-intent-review-confirm" data-testid="sp-intent-global-rule-confirm">
              <input
                type="checkbox"
                :checked="globalDefaultRuleConfirmation && globalDefaultRuleConfirmationDraftKey === currentDraftKey"
                @change="markGlobalDefaultRuleConfirmed($event.target.checked)"
              />
              <span>我确认这条默认规则会在所有项目生效</span>
            </label>

            <details class="sp-intent-dry-run" data-testid="sp-intent-dry-run-panel">
              <summary>试问验证</summary>
              <div class="sp-intent-dry-run-body">
                <textarea
                  class="sp-textarea"
                  rows="3"
                  data-testid="sp-intent-dry-run-question"
                  v-model="dryRunQuestion"
                  placeholder="输入一个问题，看看这份草稿会如何参与回答"
                ></textarea>
                <button type="button" class="btn btn-secondary" data-testid="sp-intent-dry-run-submit" :disabled="fallbackMode || !hasResolvedCwd || state === 'dry_running'" @click="runDryRun">
                  {{ state === 'dry_running' ? '验证中...' : '验证' }}
                </button>
                <div v-if="dryRunResult" class="sp-intent-dry-run-result" data-testid="sp-intent-dry-run-result">
                  <strong>仅用于保存前验证。</strong>
                  <span>{{ dryRunSummary(dryRunResult) }}</span>
                </div>
              </div>
            </details>

            <div class="sp-intent-actions">
              <button
                type="button"
                class="btn btn-primary sp-intent-save"
                data-testid="sp-intent-save-btn"
                :disabled="!canSave"
                @click="commitIntent"
              >{{ state === 'committing' ? '保存中...' : '确认保存' }}</button>
            </div>
          </section>

          <div v-if="notice" class="sp-notice" :class="'is-' + noticeLevel" data-testid="sp-intent-notice">{{ notice }}</div>
        </div>
      </div>
    </div>
  `,
};
