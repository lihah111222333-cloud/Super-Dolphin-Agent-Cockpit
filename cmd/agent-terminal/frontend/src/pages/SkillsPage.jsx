import React, { useState } from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { SkillsPage as VueComp } from './SkillsPage.js';
import { TagInput } from '../components/TagInput.jsx';
import { SkillsEditorModal } from '../components/skills/SkillsEditorModal.jsx';

function SkillsHeader() {
  return (
    <div className="panel-header">
      <div className="ph-bar"></div>
      <div className="ph-text"><h2>技能管理</h2></div>
    </div>
  );
}

function SkillsResolutionEntry({
  entry, sourceIdx, conflict, conflictIdx, resolutionActioning, resolutionNamePrompt, resolutionPreview, vm
}) {
  const providerEntries = vm.resolutionProviderEntries(conflict) || [];
  const actionEntries = vm.resolutionActionEntries(conflict) || [];

  return (
    <div className="skills-resolution-actions">
      {(providerEntries.length > 1 || entry.merged_provider_entry) && (
        <span className="skills-resolution-source">{vm.resolutionProviderEntryLabel(entry)}</span>
      )}
      {actionEntries.map((actionEntry, actionIdx) => {
        const applyKey = vm.resolutionApplyKey(conflict, actionEntry.action, vm.resolutionActionEntryTarget(actionEntry, entry));
        return (
          <button
            key={applyKey + '-' + actionIdx}
            className="btn btn-ghost btn-xs"
            disabled={resolutionActioning === applyKey}
            data-testid={`skills-resolution-action-${conflictIdx}-${sourceIdx}-${actionIdx}`}
            onClick={() => vm.onApplyResolution(conflict, actionEntry.action, vm.resolutionActionEntryTarget(actionEntry, entry))}
          >
            {resolutionActioning === applyKey ? (
              <span>处理中...</span>
            ) : (
              <span>{vm.resolutionActionEntryLabel(actionEntry)}</span>
            )}
          </button>
        );
      })}

      {vm.resolutionNamePromptApplies(conflict, entry) && (
        <div className="skills-resolution-name-field skills-resolution-name-inline" data-testid="skills-resolution-name-prompt">
          <label className="skills-resolution-name-input-row">
            <span>新技能名称</span>
            <input
              value={vm.resolutionNameInput.value}
              className="modal-input"
              data-testid="skills-resolution-name-input"
              placeholder="例如：skill-private"
              onChange={(e) => { vm.resolutionNameInput.value = e.target.value; }}
              onKeyUp={(e) => { if (e.key === 'Enter') vm.confirmResolutionNewName(); }}
            />
          </label>
          <div className="skills-resolution-name-actions">
            <span>{vm.resolutionNamePromptHelpText(resolutionNamePrompt)}</span>
            <button
              className="btn btn-primary btn-xs"
              data-testid="skills-resolution-name-confirm"
              disabled={resolutionActioning === resolutionNamePrompt.applyKey}
              onClick={vm.confirmResolutionNewName}
            >
              {vm.resolutionNamePromptButtonText(resolutionNamePrompt, resolutionActioning)}
            </button>
            <button className="btn btn-ghost btn-xs" data-testid="skills-resolution-name-cancel" onClick={vm.clearResolutionNamePrompt}>取消</button>
          </div>
        </div>
      )}

      {vm.resolutionPreviewApplies(conflict, entry) && (
        <article className="skills-resolution-preview is-inline" data-testid="skills-resolution-preview">
          <div className="skills-resolution-preview-head">
            <div>
              <strong>{vm.resolutionActionLabel(resolutionPreview.action)}</strong>
              <p>{vm.resolutionPreviewIntro(resolutionPreview)}</p>
            </div>
            {resolutionPreview.requiresApply && (
              <button
                className="btn btn-primary btn-xs"
                data-testid="skills-resolution-confirm"
                disabled={resolutionActioning === 'confirm'}
                onClick={vm.confirmResolutionPreview}
              >
                {resolutionActioning === 'confirm' ? '应用中...' : '确认应用'}
              </button>
            )}
            <button className="btn btn-ghost btn-xs" data-testid="skills-resolution-cancel" onClick={vm.clearResolutionPreview}>取消</button>
          </div>
          {(resolutionPreview.items || []).map((item, previewIdx) => (
            <div
              key={item.preview_id || item.source_path_id || previewIdx}
              className="skills-resolution-preview-item"
            >
              <div className="skills-resolution-preview-summary">{vm.resolutionPreviewItemSummary(item, resolutionPreview.action)}</div>
              <div className="skills-resolution-preview-paths">
                {vm.resolutionPreviewItemPaths(item, resolutionPreview.action).map((pathItem) => (
                  <div
                    key={pathItem.label + pathItem.value}
                    className="skills-resolution-preview-path-row"
                  >
                    <span>{pathItem.label}</span>
                    <code>{pathItem.value}</code>
                  </div>
                ))}
              </div>
              {(item.diff || item.source_hash || item.target_hash) && (
                <details className="skills-resolution-technical">
                  <summary>技术信息</summary>
                  {item.source_hash && <div className="skills-resolution-preview-path">外部版本号：{vm.resolutionShortHash(item.source_hash)}</div>}
                  {item.target_hash && <div className="skills-resolution-preview-path">管理版本号：{vm.resolutionShortHash(item.target_hash)}</div>}
                  {item.diff && <pre className="skills-resolution-preview-diff">{item.diff}</pre>}
                </details>
              )}
            </div>
          ))}
        </article>
      )}
    </div>
  );
}

function SkillsResolutionItem({
  conflict, conflictIdx, resolutionActioning, resolutionNamePrompt, resolutionPreview, vm
}) {
  const providerEntries = vm.resolutionProviderEntries(conflict) || [];
  const actionEntries = vm.resolutionActionEntries(conflict) || [];
  const manualSteps = vm.resolutionManualSteps(conflict) || [];

  return (
    <article
      className="skills-resolution-item"
      data-testid={`skills-resolution-item-${conflictIdx}`}
    >
      <div className="skills-resolution-main">
        <strong>{vm.resolutionTitle(conflict)}</strong>
        <span>
          {vm.resolutionProviderEntry(conflict).provider
            ? vm.resolutionProviderEntryLabel(vm.resolutionProviderEntry(conflict))
            : vm.scopeLabel(conflict.scope)}
        </span>
      </div>
      <p className="skills-resolution-guide">{vm.resolutionConflictGuide(conflict)}</p>
      {actionEntries.length > 0 && (
        <div className="skills-resolution-actions-title">{vm.resolutionActionSectionTitle(conflict)}</div>
      )}
      {providerEntries.map((entry, sourceIdx) => (
        <SkillsResolutionEntry
          key={entry.source_path_id || entry.provider || sourceIdx}
          entry={entry}
          sourceIdx={sourceIdx}
          conflict={conflict}
          conflictIdx={conflictIdx}
          resolutionActioning={resolutionActioning}
          resolutionNamePrompt={resolutionNamePrompt}
          resolutionPreview={resolutionPreview}
          vm={vm}
        />
      ))}
      {vm.resolutionActionFootnote(conflict) ? (
        <div className="skills-resolution-action-help">
          <span>{vm.resolutionActionFootnote(conflict)}</span>
        </div>
      ) : actionEntries.length > 0 ? (
        <div className="skills-resolution-action-help">
          {actionEntries.map((actionEntry, actionHelpIdx) => (
            <div key={'help-' + actionHelpIdx}>
              <strong>{vm.resolutionActionEntryLabel(actionEntry)}</strong>
              <span>{vm.resolutionActionEntryHelp(actionEntry)}</span>
            </div>
          ))}
        </div>
      ) : null}
      {manualSteps.length > 0 && (
        <div className="skills-resolution-manual-steps">
          <strong>处理方式</strong>
          <ol>
            {manualSteps.map((step) => (
              <li key={step}>{step}</li>
            ))}
          </ol>
        </div>
      )}
    </article>
  );
}

function SkillsResolutionSection({
  showResolutionCheckButton, resolutionConflicts, resolutionLoading,
  showResolutionPanel, resolutionConflictAlertText,
  resolutionActioning, resolutionNamePrompt, resolutionPreview,
  resolutionCheckButtonText, resolutionPanelToggleText, vm
}) {
  if (resolutionConflicts.length === 0 && !showResolutionCheckButton) return null;

  return (
    <>
      <div className="skills-subtoolbar" data-testid="skills-subtoolbar">
        {showResolutionCheckButton && (
          <button
            className={`btn btn-ghost btn-xs skills-resolution-check ${resolutionConflicts.length > 0 ? 'is-warning' : ''}`}
            data-testid="skills-resolution-refresh"
            disabled={resolutionLoading}
            onClick={vm.refreshSkillResolutions}
          >
            {resolutionCheckButtonText}
          </button>
        )}
        {resolutionConflicts.length > 0 && (
          <button
            className="btn btn-ghost btn-xs"
            data-testid="skills-resolution-panel-toggle"
            onClick={vm.toggleResolutionPanel}
          >
            {resolutionPanelToggleText}
          </button>
        )}
      </div>

      {resolutionConflictAlertText && (
        <div className="skills-resolution-alert" data-testid="skills-resolution-alert">
          {resolutionConflictAlertText}
        </div>
      )}

      {showResolutionPanel && (
        <div className="skills-resolution-list" data-testid="skills-resolution-list">
          {resolutionConflicts.map((conflict, conflictIdx) => (
            <SkillsResolutionItem
              key={conflict.conflict_id || conflictIdx}
              conflict={conflict}
              conflictIdx={conflictIdx}
              resolutionActioning={resolutionActioning}
              resolutionNamePrompt={resolutionNamePrompt}
              resolutionPreview={resolutionPreview}
              vm={vm}
            />
          ))}
        </div>
      )}
    </>
  );
}

function SkillsList({
  filteredSkillCards, skillCardKey, isSkillCardActive, isSkillCardRecentlySaved,
  scopeLabel, deletingSkillName, isDeletingSkill, onEditSkill, onDeleteSkill,
  showSkillCount, skillCountText, notice, isEditorOpen,
  visibleImportSummaryDrafts, importSummaryPanelTitle, importSummaryPanelHint,
  clearImportSummaryDrafts, applyImportSummaryDraft, openImportSummaryDraft,
  dismissImportSummaryDraft, importFailures
}) {
  return (
    <>
      {filteredSkillCards.length === 0 ? (
        <div className="empty-state" data-testid="skills-search-empty-state">
          <div className="es-icon skills-empty-icon">
            <svg viewBox="0 0 24 24" width="32" height="32" aria-hidden="true">
              <path fill="currentColor" d="M10 2a8 8 0 1 0 5 14.3l5 5 1.4-1.4-5-5A8 8 0 0 0 10 2zm0 2a6 6 0 1 1 0 12 6 6 0 0 1 0-12z" />
            </svg>
          </div>
          <h3>没有匹配技能</h3>
          <p>尝试更换关键词或切换使用范围，支持按名称、简介、关键词搜索</p>
        </div>
      ) : (
        <div className="skills-card-grid" data-testid="skills-list">
          {filteredSkillCards.map((item, idx) => (
            <article
              key={skillCardKey(item)}
              className={`data-card-vue skill-card skill-card-compact ${isSkillCardActive(item) ? 'active' : ''}`}
              data-testid={`skills-card-${idx}`}
            >
              <div className="skill-card-header">
                <div className="skill-card-heading">
                  <div className="skill-card-title">{item.displayLabel}</div>
                  {item.displayName && <div className="skill-card-path" title={item.name}>{item.name}</div>}
                  <div className="skill-card-path" title={item.dir}>{item.dir || '-'}</div>
                </div>
                <div className="skill-card-tags">
                  <span
                    className={`skill-card-scope-tag skill-card-scope-${item.scope || 'project'}`}
                    title={scopeLabel(item.scope)}
                    data-testid={`skills-card-scope-${idx}`}
                  >
                    {scopeLabel(item.scope)}
                  </span>
                  {isSkillCardActive(item) && <span className="skill-card-badge">编辑中</span>}
                  {isSkillCardRecentlySaved(item) && (
                    <span className="skill-card-saved-badge" data-testid={`skills-card-saved-${idx}`}>
                      已保存
                    </span>
                  )}
                </div>
              </div>
              <div className="skill-card-description">{item.description || '暂无简介'}</div>
              <div className="skill-card-summary-preview">{item.summary || '暂无简介，点击编辑补充。'}</div>
              <div className="skill-word-groups">
                {item.displayScenarioWords?.length > 0 && (
                  <div className="skill-word-line">
                    <strong>关键词</strong>
                    <div className="skill-chip-row">
                      {item.displayScenarioWords.slice(0, 4).map((word, wordIdx) => (
                        <span key={`trigger-${idx}-${wordIdx}`} className="skill-word-chip">
                          {word}
                        </span>
                      ))}
                      {item.displayScenarioWords.length > 4 && (
                        <span className="skill-word-chip muted">+{item.displayScenarioWords.length - 4}</span>
                      )}
                    </div>
                  </div>
                )}
              </div>
              <div className="data-actions-vue skill-actions">
                <button className="btn btn-secondary btn-xs" data-testid={`skills-edit-button-${idx}`} onClick={() => onEditSkill(item)}>编辑详情</button>
                <button
                  className="btn btn-ghost btn-xs btn-warning"
                  data-testid={`skills-delete-button-${idx}`}
                  disabled={Boolean(deletingSkillName)}
                  onClick={() => onDeleteSkill(item)}
                >
                  {isDeletingSkill(item.name) ? '删除中...' : '删除'}
                </button>
              </div>
            </article>
          ))}
        </div>
      )}

      {showSkillCount && <div className="skills-inline-tip">{skillCountText}</div>}

      {notice.message && !isEditorOpen && (
        <div className={`skills-notice is-${notice.level}`} data-testid="skills-notice">
          {notice.message}
        </div>
      )}

      {visibleImportSummaryDrafts.length > 0 && (
        <div className="skills-import-summary-panel" data-testid="skills-import-summary-panel">
          <div className="skills-import-summary-head">
            <div>
              <strong>{importSummaryPanelTitle}</strong>
              <span>{importSummaryPanelHint}</span>
            </div>
            <button className="btn btn-ghost btn-xs" data-testid="skills-import-summary-clear" onClick={clearImportSummaryDrafts}>收起</button>
          </div>
          {visibleImportSummaryDrafts.map((draft, draftIdx) => (
            <article
              key={draft.id || draft.skillFile || draftIdx}
              className={`skills-import-summary-item is-${draft.status}`}
              data-testid={`skills-import-summary-item-${draftIdx}`}
            >
              <div className="skills-import-summary-main">
                <strong>{draft.name || '未命名技能'}</strong>
                <span>{scopeLabel(draft.scope)}</span>
              </div>
              {draft.status === 'ready' || draft.status === 'applied' ? (
                <p className="skills-import-summary-text">{draft.suggestion}</p>
              ) : draft.status === 'conflict' ? (
                <p className="skills-import-summary-text">{draft.error}</p>
              ) : (
                <p className="skills-import-summary-text">{draft.error || '技能已正常导入。可以稍后手动补充简介。'}</p>
              )}
              <div className="skills-import-summary-actions">
                {draft.status === 'ready' ? (
                  <button
                    className="btn btn-secondary btn-xs"
                    data-testid={`skills-import-summary-apply-${draftIdx}`}
                    onClick={() => applyImportSummaryDraft(draft)}
                  >
                    采用并编辑
                  </button>
                ) : draft.status === 'applied' ? (
                  <span className="skills-inline-tip">已采用，保存后生效</span>
                ) : draft.status === 'error' ? (
                  <button
                    className="btn btn-secondary btn-xs"
                    data-testid={`skills-import-summary-edit-${draftIdx}`}
                    onClick={() => openImportSummaryDraft(draft)}
                  >
                    编辑简介
                  </button>
                ) : null}
                <button className="btn btn-ghost btn-xs" data-testid={`skills-import-summary-dismiss-${draftIdx}`} onClick={() => dismissImportSummaryDraft(draft)}>跳过</button>
              </div>
            </article>
          ))}
        </div>
      )}

      {importFailures.length > 0 && (
        <>
          <ul className="skills-failure-list" data-testid="skills-failure-list">
            {importFailures.slice(0, 5).map((item) => (
              <li key={item}>{item}</li>
            ))}
          </ul>
          {importFailures.length > 5 && (
            <div className="skills-inline-tip">还有 {importFailures.length - 5} 条失败项</div>
          )}
        </>
      )}
    </>
  );
}


export function SkillsPage(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const searchQuery = val(vm.searchQuery);
  const scopeFilter = val(vm.scopeFilter);
  const scopeCounts = val(vm.scopeCounts) || { all: 0, project: 0, personal: 0 };
  const filteredSkillCards = val(vm.filteredSkillCards) || [];
  const showSkillCount = val(vm.showSkillCount);
  const skillCountText = val(vm.skillCountText);
  const uploading = val(vm.uploading);
  const importScopePromptOpen = val(vm.importScopePromptOpen);

  // Resolutions fields
  const showResolutionCheckButton = val(vm.showResolutionCheckButton);
  const resolutionConflicts = val(vm.resolutionConflicts) || [];
  const resolutionLoading = val(vm.resolutionLoading);
  const showResolutionPanel = val(vm.showResolutionPanel);
  const resolutionConflictAlertText = val(vm.resolutionConflictAlertText);
  const resolutionActioning = val(vm.resolutionActioning);
  const resolutionNamePrompt = val(vm.resolutionNamePrompt);
  const resolutionPreview = val(vm.resolutionPreview);
  const resolutionCheckButtonText = val(vm.resolutionCheckButtonText);
  const resolutionPanelToggleText = val(vm.resolutionPanelToggleText);

  // Editor fields
  const isEditorOpen = val(vm.isEditorOpen);
  const form = vm.form || {};
  const isEditingMainSkillFile = val(vm.isEditingMainSkillFile);
  const summarySuggesting = val(vm.summarySuggesting);
  const summarySuggestion = val(vm.summarySuggestion);
  const generatedSummaryPreview = val(vm.generatedSummaryPreview);
  const scenarioKeywordsText = val(vm.scenarioKeywordsText);
  const showRelatedSkillFiles = val(vm.showRelatedSkillFiles);
  const skillFiles = val(vm.skillFiles) || [];
  const activeSkillFilePath = val(vm.activeSkillFilePath);
  const isBodyEditing = val(vm.isBodyEditing);
  const bodyEditorFocused = val(vm.bodyEditorFocused);
  const skillBodyMarkdownHtml = val(vm.skillBodyMarkdownHtml) || '';
  const notice = vm.notice || {};
  const saving = val(vm.saving);
  const saveButtonLabel = val(vm.saveButtonLabel);

  // Delete modal fields
  const confirmDeleteTarget = val(vm.confirmDeleteTarget);
  const deletingSkillName = val(vm.deletingSkillName);

  // Failures lists
  const importFailures = val(vm.importFailures) || [];

  return (
    <section id="page-skills" className="page active skills-page" data-testid="skills-page">
      <SkillsHeader />
      <div className="split-duo" data-testid="skills-split">
        <div className="split-left" data-testid="skills-left">
          <div className="section-header">技能列表</div>
          <div className="panel-body skills-list-panel" data-testid="skills-list-panel">
            <div className="skills-toolbar" data-testid="skills-toolbar">
              <button
                className="btn btn-secondary"
                data-testid="skills-import-button"
                disabled={uploading || importScopePromptOpen}
                onClick={vm.onUploadSkill}
              >
                {uploading ? '导入中...' : '批量导入技能目录'}
              </button>
              <button className="btn btn-ghost" data-testid="skills-create-button" onClick={vm.onCreateSkill}>
                新建技能
              </button>
              <div className="skills-search-wrap">
                <input
                  value={searchQuery}
                  className="modal-input skills-search-input"
                  data-testid="skills-search-input"
                  placeholder="搜索技能名称、简介、关键词..."
                  onChange={(e) => { vm.searchQuery.value = e.target.value; }}
                />
              </div>
            </div>

            <div className="skills-segmented skills-scope-filter" data-testid="skills-scope-filter" role="tablist">
              <button
                type="button"
                className={`skills-segmented-item ${scopeFilter === 'personal' ? 'active' : ''}`}
                data-testid="skills-scope-filter-personal"
                role="tab"
                onClick={() => { vm.scopeFilter.value = 'personal'; }}
              >
                <span className="skills-scope-dot skills-scope-dot-personal" aria-hidden="true"></span>
                <span>私人使用</span>
                <span className="skills-segmented-count">{scopeCounts.personal}</span>
              </button>
              <button
                type="button"
                className={`skills-segmented-item ${scopeFilter === 'project' ? 'active' : ''}`}
                data-testid="skills-scope-filter-project"
                role="tab"
                onClick={() => { vm.scopeFilter.value = 'project'; }}
              >
                <span className="skills-scope-dot skills-scope-dot-project" aria-hidden="true"></span>
                <span>项目共享</span>
                <span className="skills-segmented-count">{scopeCounts.project}</span>
              </button>
              <button
                type="button"
                className={`skills-segmented-item ${scopeFilter === 'all' ? 'active' : ''}`}
                data-testid="skills-scope-filter-all"
                role="tab"
                onClick={() => { vm.scopeFilter.value = 'all'; }}
              >
                <span>全部</span>
                <span className="skills-segmented-count">{scopeCounts.all}</span>
              </button>
            </div>

            <SkillsResolutionSection
              showResolutionCheckButton={showResolutionCheckButton}
              resolutionConflicts={resolutionConflicts}
              resolutionLoading={resolutionLoading}
              refreshSkillResolutions={vm.refreshSkillResolutions}
              resolutionCheckButtonText={resolutionCheckButtonText}
              resolutionPanelToggleText={resolutionPanelToggleText}
              toggleResolutionPanel={vm.toggleResolutionPanel}
              showResolutionPanel={showResolutionPanel}
              resolutionConflictAlertText={resolutionConflictAlertText}
              onApplyResolution={vm.onApplyResolution}
              resolutionActioning={resolutionActioning}
              confirmResolutionNewName={vm.confirmResolutionNewName}
              clearResolutionNewNamePrompt={vm.clearResolutionNamePrompt}
              confirmResolutionPreview={vm.confirmResolutionPreview}
              clearResolutionPreview={vm.clearResolutionPreview}
              resolutionPreview={resolutionPreview}
              resolutionNamePrompt={resolutionNamePrompt}
              resolutionTitle={vm.resolutionTitle}
              resolutionProviderEntry={vm.resolutionProviderEntry}
              resolutionProviderEntryLabel={vm.resolutionProviderEntryLabel}
              resolutionConflictGuide={vm.resolutionConflictGuide}
              resolutionActionEntries={vm.resolutionActionEntries}
              resolutionActionSectionTitle={vm.resolutionActionSectionTitle}
              resolutionProviderEntries={vm.resolutionProviderEntries}
              resolutionActionEntryTarget={vm.resolutionActionEntryTarget}
              resolutionApplyKey={vm.resolutionApplyKey}
              resolutionActionEntryLabel={vm.resolutionActionEntryLabel}
              resolutionNamePromptApplies={vm.resolutionNamePromptApplies}
              resolutionNamePromptHelpText={vm.resolutionNamePromptHelpText}
              resolutionNamePromptButtonText={vm.resolutionNamePromptButtonText}
              resolutionPreviewApplies={vm.resolutionPreviewApplies}
              resolutionPreviewIntro={vm.resolutionPreviewIntro}
              resolutionPreviewItemSummary={vm.resolutionPreviewItemSummary}
              resolutionPreviewItemPaths={vm.resolutionPreviewItemPaths}
              resolutionActionLabel={vm.resolutionActionLabel}
              resolutionShortHash={vm.resolutionShortHash}
              resolutionActionFootnote={vm.resolutionActionFootnote}
              resolutionManualSteps={vm.resolutionManualSteps}
              scopeLabel={vm.scopeLabel}
              vm={vm}
            />

            <SkillsList
              filteredSkillCards={filteredSkillCards}
              skillCardKey={vm.skillCardKey}
              isSkillCardActive={vm.isSkillCardActive}
              isSkillCardRecentlySaved={vm.isSkillCardRecentlySaved}
              scopeLabel={vm.scopeLabel}
              deletingSkillName={deletingSkillName}
              isDeletingSkill={vm.isDeletingSkill}
              onEditSkill={vm.onEditSkill}
              onDeleteSkill={vm.onDeleteSkill}
              showSkillCount={showSkillCount}
              skillCountText={skillCountText}
              notice={notice}
              isEditorOpen={isEditorOpen}
              visibleImportSummaryDrafts={vm.visibleImportSummaryDrafts}
              importSummaryPanelTitle={vm.importSummaryPanelTitle}
              importSummaryPanelHint={vm.importSummaryPanelHint}
              clearImportSummaryDrafts={vm.clearImportSummaryDrafts}
              applyImportSummaryDraft={vm.applyImportSummaryDraft}
              openImportSummaryDraft={vm.openImportSummaryDraft}
              dismissImportSummaryDraft={vm.dismissImportSummaryDraft}
              importFailures={importFailures}
            />
          </div>
        </div>
      </div>

      <SkillsEditorModal
        isEditorOpen={isEditorOpen}
        form={form}
        isEditingMainSkillFile={isEditingMainSkillFile}
        summarySuggesting={vm.summarySuggesting}
        onSuggestSkillSummary={vm.onSuggestSkillSummary}
        summarySuggestion={summarySuggestion}
        applySummarySuggestion={vm.applySummarySuggestion}
        generatedSummaryPreview={generatedSummaryPreview}
        scenarioKeywordsText={scenarioKeywordsText}
        showRelatedSkillFiles={showRelatedSkillFiles}
        skillFiles={skillFiles}
        activeSkillFilePath={activeSkillFilePath}
        onOpenSkillSubfile={vm.onOpenSkillSubfile}
        isBodyEditing={isBodyEditing}
        bodyEditorFocused={bodyEditorFocused}
        startBodyEdit={vm.startBodyEdit}
        finishBodyEdit={vm.finishBodyEdit}
        skillBodyMarkdownHtml={skillBodyMarkdownHtml}
        onSkillPreviewClick={vm.onSkillPreviewClick}
        onBodyFocus={vm.onBodyFocus}
        onBodyBlur={vm.onBodyBlur}
        notice={notice}
        saving={saving}
        onSaveSkill={vm.onSaveSkill}
        saveButtonLabel={saveButtonLabel}
        closeEditor={vm.closeEditor}
        vm={vm}
      />

      {confirmDeleteTarget && (
        <div className="modal-overlay" data-testid="skills-delete-overlay" onClick={(e) => { if (e.target === e.currentTarget) vm.cancelSkillDelete(); }}>
          <div className="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="skills-delete-modal">
            <div className="memory-modal-head">
              <div>
                <div className="modal-title">删除技能</div>
                <div className="memory-modal-tip">{confirmDeleteTarget.name} · {vm.scopeLabel(confirmDeleteTarget.scope)}</div>
              </div>
              <button className="btn btn-ghost" data-testid="skills-delete-close" disabled={Boolean(deletingSkillName)} onClick={vm.cancelSkillDelete}>关闭</button>
            </div>
            <div className="memory-form-helper">保存位置：{confirmDeleteTarget.dir || '-'}</div>
            <div className="memory-form-helper">确定删除技能 “{confirmDeleteTarget.name}” 吗？该操作会删除技能目录及其资源文件，无法恢复。</div>
            <div className="memory-editor-actions">
              <button className="btn btn-ghost" data-testid="skills-delete-cancel" disabled={Boolean(deletingSkillName)} onClick={vm.cancelSkillDelete}>取消</button>
              <button className="btn btn-danger" data-testid="skills-delete-confirm" disabled={Boolean(deletingSkillName)} onClick={vm.confirmSkillDelete}>{vm.isDeletingSkill(confirmDeleteTarget.name) ? '删除中...' : '确认删除'}</button>
            </div>
          </div>
        </div>
      )}

      {importScopePromptOpen && (
        <div className="modal-overlay" data-testid="skills-import-scope-modal" onClick={(e) => { if (e.target === e.currentTarget) vm.cancelImportScopePrompt(); }}>
          <div className="modal-box memory-modal" role="dialog" aria-modal="true">
            <div className="memory-modal-head">
              <div>
                <div className="modal-title">导入技能</div>
                <div className="memory-modal-tip">选择导入后的使用范围</div>
              </div>
              <button className="btn btn-ghost" data-testid="skills-import-scope-close" disabled={uploading} onClick={vm.cancelImportScopePrompt}>关闭</button>
            </div>
            <div className="memory-form-helper">这些技能导入后给谁使用？</div>
            <div className="memory-editor-actions">
              <button className="btn btn-ghost" data-testid="skills-import-scope-cancel" disabled={uploading} onClick={vm.cancelImportScopePrompt}>取消</button>
              <button className="btn btn-secondary" data-testid="skills-import-scope-personal" disabled={uploading} onClick={() => vm.confirmImportScope('personal')}>私人使用</button>
              <button className="btn btn-primary" data-testid="skills-import-scope-project" disabled={uploading} onClick={() => vm.confirmImportScope('project')}>项目共享</button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
