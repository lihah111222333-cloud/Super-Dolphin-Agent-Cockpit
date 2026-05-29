import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { PromptIntentWizard as VueComp } from './PromptIntentWizard.js';

function PromptIntentWizardDraftSection({
  currentCard, selectedKind, intentTypeLabel, cardWhenToUse, cardWhenNotToUse, cardSaveBoundary, cardPreviewText,
  suggestedAlternative, canApplySuggestedAlternative, applySuggestedAlternative, reviewIssues, hasReviews, hasBlocks,
  reviewConfirmation, confirmationDraftKey, currentDraftKey, markReviewConfirmed, requiresGlobalDefaultRuleConfirmation,
  globalDefaultRuleConfirmation, globalDefaultRuleConfirmationDraftKey, markGlobalDefaultRuleConfirmed, dryRunQuestion,
  dryRunResult, dryRunSummary, fallbackMode, hasResolvedCwd, state, runDryRun, commitIntent, canSave, vm
}) {
  return (
    <section className="sp-intent-confirmation" data-testid="sp-intent-confirmation">
      <div className="sp-intent-confirmation-head">
        <span className="sp-intent-kind">{intentTypeLabel(currentCard.kind || selectedKind)}</span>
        <strong data-testid="sp-intent-card-title">{currentCard.title || '待确认草稿'}</strong>
      </div>
      {currentCard.summary && <p className="sp-intent-summary" data-testid="sp-intent-card-summary">{currentCard.summary}</p>}
      {cardWhenToUse(currentCard) && <p className="sp-intent-copy">{cardWhenToUse(currentCard)}</p>}
      {cardWhenNotToUse(currentCard) && <p className="sp-intent-copy">{cardWhenNotToUse(currentCard)}</p>}
      {cardSaveBoundary(currentCard) && (
        <p className="sp-intent-copy" data-testid="sp-intent-save-boundary">
          保存边界：{cardSaveBoundary(currentCard)}
        </p>
      )}
      {currentCard.workflow && currentCard.workflow.length > 0 && (
        <ol className="sp-intent-list">
          {currentCard.workflow.map((step) => (
            <li key={step}>{step}</li>
          ))}
        </ol>
      )}
      {cardPreviewText(currentCard) && (
        <div className="sp-intent-preview" data-testid="sp-intent-card-preview">
          {cardPreviewText(currentCard)}
        </div>
      )}

      <div className="sp-intent-examples" data-testid="sp-intent-examples">
        <div>
          <div className="sp-intent-examples-title">适合的问题</div>
          <ul>
            {currentCard.hit_examples?.map((example) => (
              <li key={`hit-${example}`}>{example}</li>
            ))}
          </ul>
        </div>
        <div>
          <div className="sp-intent-examples-title">不适合的问题</div>
          <ul>
            {currentCard.miss_examples?.map((example) => (
              <li key={`miss-${example}`}>{example}</li>
            ))}
          </ul>
        </div>
      </div>

      {(currentCard.kind || selectedKind) === 'default_rule' && (
        <div className="sp-intent-default-rule" data-testid="sp-intent-default-rule-review">
          <div className="sp-intent-examples-title">可能冲突的已有规则</div>
          {currentCard.conflicting_rules && currentCard.conflicting_rules.length > 0 ? (
            <ul>
              {currentCard.conflicting_rules.map((rule, idx) => (
                <li key={rule.title || rule.summary || idx}>
                  <strong>{rule.title || '未命名规则'}</strong>
                  {rule.summary && `：${rule.summary}`}
                </li>
              ))}
            </ul>
          ) : (
            <p>未发现明显冲突</p>
          )}
        </div>
      )}

      {suggestedAlternative && (
        <div className="sp-intent-optimization" data-testid="sp-intent-suggested-alternative">
          <div>
            <div className="sp-intent-examples-title">{suggestedAlternative.title}</div>
            <p>{suggestedAlternative.body}</p>
            {suggestedAlternative.reason && <p>原因：{suggestedAlternative.reason}</p>}
          </div>
          {canApplySuggestedAlternative && (
            <button
              type="button"
              className="btn btn-secondary"
              data-testid="sp-intent-apply-suggested-alternative"
              disabled={state === 'drafting'}
              onClick={applySuggestedAlternative}
            >
              按这个方向重新整理
            </button>
          )}
        </div>
      )}

      {reviewIssues.length > 0 && (
        <div className="sp-intent-issues" data-testid="sp-intent-issues">
          {reviewIssues.map((issue, idx) => (
            <div
              key={issue.code + issue.message + idx}
              className={`sp-intent-issue sp-intent-issue--${issue.severity}`}
            >
              {issue.message}
            </div>
          ))}
        </div>
      )}

      {hasReviews && !hasBlocks && (
        <label className="sp-intent-review-confirm" data-testid="sp-intent-review-confirm">
          <input
            type="checkbox"
            checked={reviewConfirmation && confirmationDraftKey === currentDraftKey}
            onChange={(e) => markReviewConfirmed(e.target.checked)}
          />
          <span>我已确认这些风险，仍要保存</span>
        </label>
      )}

      {requiresGlobalDefaultRuleConfirmation && !hasBlocks && (
        <label className="sp-intent-review-confirm" data-testid="sp-intent-global-rule-confirm">
          <input
            type="checkbox"
            checked={globalDefaultRuleConfirmation && globalDefaultRuleConfirmationDraftKey === currentDraftKey}
            onChange={(e) => vm.markGlobalDefaultRuleConfirmed(e.target.checked)}
          />
          <span>我确认这条默认规则会在所有项目生效</span>
        </label>
      )}

      <details className="sp-intent-dry-run" data-testid="sp-intent-dry-run-panel">
        <summary>试问验证</summary>
        <div className="sp-intent-dry-run-body">
          <textarea
            className="sp-textarea"
            rows={3}
            data-testid="sp-intent-dry-run-question"
            value={dryRunQuestion}
            placeholder="输入一个问题，看看这份草稿会如何参与回答"
            onChange={(e) => { vm.dryRunQuestion.value = e.target.value; }}
          ></textarea>
          <button
            type="button"
            className="btn btn-secondary"
            data-testid="sp-intent-dry-run-submit"
            disabled={fallbackMode || !hasResolvedCwd || state === 'dry_running'}
            onClick={runDryRun}
          >
            {state === 'dry_running' ? '验证中...' : '验证'}
          </button>
          {dryRunResult && (
            <div className="sp-intent-dry-run-result" data-testid="sp-intent-dry-run-result">
              <strong>仅用于保存前验证。</strong>
              <span>{dryRunSummary(dryRunResult)}</span>
            </div>
          )}
        </div>
      </details>

      <div className="sp-intent-actions">
        <button
          type="button"
          className="btn btn-primary sp-intent-save"
          data-testid="sp-intent-save-btn"
          disabled={!canSave}
          onClick={commitIntent}
        >
          {state === 'committing' ? '保存中...' : '确认保存'}
        </button>
      </div>
    </section>
  );
}

export function PromptIntentWizard(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const selectedKind = val(vm.selectedKind);
  const rawInput = val(vm.rawInput);
  const state = val(vm.state);
  const draft = val(vm.draft);
  const reviewConfirmation = val(vm.reviewConfirmation);
  const confirmationDraftKey = val(vm.confirmationDraftKey);
  const dryRunQuestion = val(vm.dryRunQuestion);
  const dryRunResult = val(vm.dryRunResult);
  const notice = val(vm.notice);
  const noticeLevel = val(vm.noticeLevel);
  const selectedPlaceholder = val(vm.selectedPlaceholder);
  const reviewIssues = val(vm.reviewIssues) || [];
  const currentCard = val(vm.currentCard) || {};
  const currentDraftKey = val(vm.currentDraftKey);
  const draftOptions = val(vm.draftOptions) || [];
  const suggestedAlternative = val(vm.suggestedAlternative);
  const hasBlocks = val(vm.hasBlocks);
  const hasReviews = val(vm.hasReviews);
  const examplesReady = val(vm.examplesReady);
  const canShowDraft = val(vm.canShowDraft);
  const hasResolvedCwd = val(vm.hasResolvedCwd);
  const canDraft = val(vm.canDraft);
  const canSave = val(vm.canSave);
  const canApplySuggestedAlternative = val(vm.canApplySuggestedAlternative);
  const selectedScopeLabel = val(vm.selectedScopeLabel);
  const selectedScope = val(vm.selectedScope);
  const selectedScopeNote = val(vm.selectedScopeNote);
  const dropActive = val(vm.dropActive);

  // Global default rule fields from scopeState
  const requiresGlobalDefaultRuleConfirmation = val(vm.requiresGlobalDefaultRuleConfirmation);
  const globalDefaultRuleConfirmation = val(vm.globalDefaultRuleConfirmation);
  const globalDefaultRuleConfirmationDraftKey = val(vm.globalDefaultRuleConfirmationDraftKey);

  if (!props.visible) return null;

  return (
    <div
      className="modal-overlay sp-intent-overlay"
      data-testid="sp-intent-wizard"
      onClick={(e) => { if (e.target === e.currentTarget) vm.closeWizard(); }}
      onKeyDown={(e) => { if (e.key === 'Escape') vm.closeWizard(); }}
    >
      <div className="modal-box sp-intent-wizard" role="dialog" aria-modal="true">
        <div className="sp-intent-head">
          <div>
            <div className="modal-title">新建提示词</div>
            <div className="sp-intent-scope" data-testid="sp-intent-scope">可用范围：{selectedScopeLabel}</div>
          </div>
          <button className="btn btn-ghost" data-testid="sp-intent-close" onClick={vm.closeWizard}>关闭</button>
        </div>

        <div className="sp-intent-body">
          <div className="sp-intent-type-tabs" data-testid="sp-intent-type-tabs">
            {vm.PROMPT_INTENT_TYPES.map((type) => (
              <button
                key={type.key}
                type="button"
                className={`sp-intent-type-tab ${selectedKind === type.key ? 'is-active' : ''}`}
                data-testid={`sp-intent-type-${type.key}`}
                disabled={state === 'drafting' || state === 'committing' || props.fallbackMode || !hasResolvedCwd}
                onClick={() => { vm.selectedKind.value = type.key; }}
              >
                {type.label}
              </button>
            ))}
          </div>

          <div className="sp-scope-segmented sp-intent-scope-picker" data-testid="sp-intent-scope-group">
            <label className={`sp-scope-option ${selectedScope === 'project' ? 'active' : ''}`}>
              <input
                type="radio"
                name="selectedScope"
                value="project"
                checked={selectedScope === 'project'}
                data-testid="sp-intent-scope-project"
                disabled={vm.isScopeSelectionDisabled()}
                onChange={(e) => { vm.selectedScope.value = e.target.value; }}
              />
              <span>这个项目</span>
            </label>
            <label className={`sp-scope-option ${selectedScope === 'global' ? 'active' : ''}`}>
              <input
                type="radio"
                name="selectedScope"
                value="global"
                checked={selectedScope === 'global'}
                data-testid="sp-intent-scope-global"
                disabled={vm.isScopeSelectionDisabled()}
                onChange={(e) => { vm.selectedScope.value = e.target.value; }}
              />
              <span>所有项目</span>
            </label>
          </div>

          <div className="sp-field">
            <label>你想让 AI 记住或使用什么？</label>
            <div
              id="prompt-intent-drop-zone"
              className={`sp-intent-drop-zone ${dropActive ? 'is-drop-active' : ''}`}
              data-file-drop-target=""
              onDragEnter={vm.onDragEnter}
              onDragOver={vm.onDragOver}
              onDragLeave={vm.onDragLeave}
              onDrop={vm.onDrop}
            >
              <textarea
                className="sp-textarea sp-intent-raw-input"
                data-testid="sp-intent-raw-input"
                rows={7}
                value={rawInput}
                placeholder={selectedPlaceholder}
                disabled={state === 'drafting' || state === 'committing' || props.fallbackMode}
                onChange={(e) => { vm.rawInput.value = e.target.value; }}
              ></textarea>
              {dropActive && <div className="sp-intent-drop-hint" aria-live="polite">松开读取文档、表格或文本资料</div>}
            </div>
          </div>

          <div className="sp-intent-scope-note" data-testid="sp-intent-scope-note">{selectedScopeNote}</div>

          <div className="sp-intent-actions">
            <button
              type="button"
              className="btn btn-primary sp-intent-primary-action"
              data-testid="sp-intent-draft-btn"
              disabled={!canDraft || props.fallbackMode}
              onClick={vm.draftIntent}
            >
              {state === 'drafting' ? '整理中...' : '整理'}
            </button>
          </div>

          {canShowDraft && (
            <>
              {draftOptions.length > 1 && (
                <div className="sp-intent-draft-options" data-testid="sp-intent-draft-options">
                  {draftOptions.map((option) => (
                    <button
                      key={option.draft_key}
                      type="button"
                      className={`sp-intent-draft-option ${option.draft_key === currentDraftKey ? 'is-active' : ''}`}
                      data-testid={`sp-intent-draft-option-${option.draft_key}`}
                      disabled={state === 'dry_running'}
                      onClick={() => vm.selectDraftOption(option)}
                    >
                      <span>{vm.intentTypeLabel(option.inferred_kind || option.kind)}</span>
                      <strong>{option.card?.title || '待确认草稿'}</strong>
                    </button>
                  ))}
                </div>
              )}

              <PromptIntentWizardDraftSection
                currentCard={currentCard}
                selectedKind={selectedKind}
                intentTypeLabel={vm.intentTypeLabel}
                cardWhenToUse={vm.cardWhenToUse}
                cardWhenNotToUse={vm.cardWhenNotToUse}
                cardSaveBoundary={vm.cardSaveBoundary}
                cardPreviewText={vm.cardPreviewText}
                suggestedAlternative={suggestedAlternative}
                canApplySuggestedAlternative={canApplySuggestedAlternative}
                applySuggestedAlternative={vm.applySuggestedAlternative}
                reviewIssues={reviewIssues}
                hasReviews={hasReviews}
                hasBlocks={hasBlocks}
                reviewConfirmation={reviewConfirmation}
                confirmationDraftKey={confirmationDraftKey}
                currentDraftKey={currentDraftKey}
                markReviewConfirmed={vm.markReviewConfirmed}
                requiresGlobalDefaultRuleConfirmation={requiresGlobalDefaultRuleConfirmation}
                globalDefaultRuleConfirmation={globalDefaultRuleConfirmation}
                globalDefaultRuleConfirmationDraftKey={globalDefaultRuleConfirmationDraftKey}
                markGlobalDefaultRuleConfirmed={vm.markGlobalDefaultRuleConfirmed}
                dryRunQuestion={dryRunQuestion}
                dryRunResult={dryRunResult}
                dryRunSummary={vm.dryRunSummary}
                fallbackMode={props.fallbackMode}
                hasResolvedCwd={hasResolvedCwd}
                state={state}
                runDryRun={vm.runDryRun}
                commitIntent={vm.commitIntent}
                canSave={canSave}
                vm={vm}
              />
            </>
          )}

          {notice && <div className={`sp-notice is-${noticeLevel}`} data-testid="sp-intent-notice">{notice}</div>}
        </div>
      </div>
    </div>
  );
}
