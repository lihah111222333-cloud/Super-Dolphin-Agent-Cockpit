import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { SystemPromptPage as VueComp } from './SystemPromptPage.js';
import { TagInput } from '../components/TagInput.jsx';
import { SectionsEditor } from './SectionsEditor.jsx';
import { PromptIntentWizard } from './PromptIntentWizard.jsx';

function SystemPromptHeader({ title, cwdDisplay }) {
  return (
    <div className="panel-header">
      <div className="ph-bar"></div>
      <div className="ph-text"><h2>{title}</h2></div>
      <div className="ph-tip">{cwdDisplay}</div>
    </div>
  );
}

function SystemPromptTabs({
  activeTab, fallbackMode, PROMPT_ASSET_TABS, switchTab,
  scopeFilters, scopeFilter, switchScopeFilter,
  statusFilters, statusFilter, switchStatusFilter
}) {
  return (
    <div className="sp-tabs-row" data-testid="sp-tabs">
      <div className="sp-category-tabs">
        {PROMPT_ASSET_TABS.map((tab) => (
          <button
            key={tab.key}
            type="button"
            className={`sp-category-tab ${activeTab === tab.key ? 'active' : ''}`}
            data-testid={`sp-tab-${tab.key}`}
            onClick={() => switchTab(tab.key)}
          >
            <span>{tab.label}</span>
          </button>
        ))}
      </div>
      <div className="sp-filters" data-testid="sp-filters">
        <div className="sp-filter-group" data-testid="sp-scope-filter">
          <span className="sp-filter-label">范围</span>
          {scopeFilters.map((item) => (
            <button
              key={item.key}
              type="button"
              className={`sp-filter-chip ${scopeFilter === item.key ? 'active' : ''}`}
              disabled={fallbackMode}
              data-testid={`sp-scope-filter-${item.key}`}
              onClick={() => switchScopeFilter(item.key)}
            >
              {item.label}
            </button>
          ))}
        </div>
        <div className="sp-filter-group" data-testid="sp-status-filter">
          <span className="sp-filter-label">状态</span>
          {statusFilters.map((item) => (
            <button
              key={item.key}
              type="button"
              className={`sp-filter-chip ${statusFilter === item.key ? 'active' : ''}`}
              disabled={fallbackMode}
              data-testid={`sp-status-filter-${item.key}`}
              onClick={() => switchStatusFilter(item.key)}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

function SystemPromptCard({
  item, idx, editorOpen, form, activePromptId, canForceLaunchPrompt,
  promptAssetBucket, truncate, continuePendingDraft, discardPendingDraft, deletingId,
  openEdit, editButtonCopy, copyPromptContent, clearLaunchPrompt,
  activatingId, activateDisabled, setLaunchPrompt, deletePrompt, deleteDisabled,
  fallbackMode
}) {
  const classes = [
    'data-card-vue',
    'sp-card',
    editorOpen && form.id === item.id ? 'active' : '',
    item.enabled === false && !item.isPendingDraft ? 'is-disabled' : '',
    item.isPendingDraft ? 'is-pending-draft' : '',
    item.assetType === 'recall' ? 'is-recall' : '',
    item.assetType === 'default_rule' ? 'is-default-rule' : ''
  ].filter(Boolean).join(' ');

  return (
    <article className={classes} data-testid={`sp-card-${idx}`}>
      <div className="sp-card-header">
        <div className="sp-card-heading">
          <div className="sp-card-title">{item.name || '未命名'}</div>
          {item.isDefault && <span className="sp-card-badge is-default">默认</span>}
          {promptAssetBucket(item) === 'expert' && <span className="sp-card-badge is-asset">专家能力</span>}
          {item.assetType === 'recall' && <span className="sp-card-badge is-asset">参考资料</span>}
          {item.assetType === 'default_rule' && <span className="sp-card-badge is-asset">默认规则</span>}
          {item.scope === 'global' && (
            <span className="sp-card-badge is-global" data-testid={`sp-scope-badge-${idx}`}>
              全局可用
            </span>
          )}
          {item.isPendingDraft && <span className="sp-card-badge is-pending">待确认</span>}
          {item.enabled === false && !item.isPendingDraft && (
            <span className="sp-card-badge is-disabled">已停用</span>
          )}
          {activePromptId === item.id && canForceLaunchPrompt(item) && (
            <span className="sp-card-badge is-active" data-testid={`sp-active-badge-${idx}`}>
              强制中
            </span>
          )}
        </div>
      </div>
      {item.description && <div className="sp-card-desc">{item.description}</div>}
      {item.tags && item.tags.length > 0 && (
        <div className="sp-card-tags">
          {item.tags.map((tag) => (
            <span className="sp-card-tag" key={tag}>{tag}</span>
          ))}
        </div>
      )}
      <div className="sp-card-preview">{truncate(item.preview)}</div>
      {item.isPendingDraft ? (
        <div className="sp-card-actions">
          <button
            className="btn btn-primary btn-xs"
            data-testid={`sp-pending-continue-btn-${idx}`}
            onClick={() => continuePendingDraft(item)}
          >
            继续确认
          </button>
          <button
            className="btn btn-ghost btn-xs btn-warning"
            data-testid={`sp-pending-discard-btn-${idx}`}
            disabled={deletingId === item.id || deletingId === item.draftKey}
            onClick={() => discardPendingDraft(item)}
          >
            {deletingId === item.id || deletingId === item.draftKey ? '丢弃中...' : '丢弃'}
          </button>
        </div>
      ) : (
        <div className="sp-card-actions">
          <button
            className="btn btn-secondary btn-xs"
            data-testid={`sp-edit-btn-${idx}`}
            disabled={item.isPendingDraft}
            onClick={() => openEdit(item)}
          >
            {editButtonCopy(item, fallbackMode)}
          </button>
          <button
            className="btn btn-ghost btn-xs"
            data-testid={`sp-copy-btn-${idx}`}
            onClick={() => copyPromptContent(item)}
          >
            复制
          </button>
          {activePromptId === item.id && canForceLaunchPrompt(item) ? (
            <button
              className="btn btn-ghost btn-xs"
              data-testid={`sp-clear-launch-btn-${idx}`}
              disabled={activateDisabled}
              onClick={clearLaunchPrompt}
            >
              {activatingId === 'clear' ? '处理中...' : '取消强制'}
            </button>
          ) : canForceLaunchPrompt(item) ? (
            <button
              className="btn btn-ghost btn-xs"
              data-testid={`sp-set-launch-btn-${idx}`}
              disabled={activateDisabled}
              onClick={() => setLaunchPrompt(item)}
            >
              {activatingId === item.id ? '处理中...' : '强制使用'}
            </button>
          ) : null}
          <button
            className="btn btn-ghost btn-xs btn-warning"
            data-testid={`sp-delete-btn-${idx}`}
            disabled={deleteDisabled || item.isPendingDraft}
            onClick={() => deletePrompt(item)}
          >
            {deletingId === item.id ? '删除中...' : '删除'}
          </button>
        </div>
      )}
    </article>
  );
}

function SystemPromptList({
  loading, fallbackMode, createDisabled, openCreate, loadPrompts,
  readonlyBannerMessage, filteredCards, emptyStateCopy, activeTab,
  editorOpen, form, activePromptId, canForceLaunchPrompt,
  truncate, continuePendingDraft, discardPendingDraft, deletingId,
  openEdit, editButtonCopy, copyPromptContent, clearLaunchPrompt,
  activatingId, setLaunchPrompt, deletePrompt, deleteDisabled,
  notice, promptAssetBucket, activateDisabled
}) {
  return (
    <div className="panel-body sp-list-panel" data-testid="sp-body">
      <div className="sp-toolbar" data-testid="sp-toolbar">
        <button
          className="btn btn-secondary"
          data-testid="sp-create-btn"
          disabled={createDisabled}
          onClick={openCreate}
        >
          + 添加给 AI 的内容
        </button>
        <button
          className="btn btn-ghost"
          data-testid="sp-refresh-btn"
          disabled={loading}
          onClick={loadPrompts}
        >
          {loading ? '加载中...' : '刷新'}
        </button>
      </div>

      {fallbackMode && (
        <div className="sp-notice is-warn sp-readonly-banner" data-testid="sp-readonly-banner">
          {readonlyBannerMessage}
        </div>
      )}

      {!loading && filteredCards.length === 0 ? (
        <div className="empty-state" data-testid="sp-empty">
          <div className="es-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" style={{ width: '24px', height: '24px' }}>
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
              <polyline points="14 2 14 8 20 8" />
            </svg>
          </div>
          <h3>暂无内容</h3>
          <p>{emptyStateCopy(fallbackMode, activeTab)}</p>
        </div>
      ) : loading ? (
        <div className="sp-loading" data-testid="sp-loading">
          <div className="sp-spinner"></div>
          <span>加载中...</span>
        </div>
      ) : (
        <div className="sp-card-grid" data-testid="sp-card-grid">
          {filteredCards.map((item, idx) => (
            <SystemPromptCard
              key={item.id}
              item={item}
              idx={idx}
              editorOpen={editorOpen}
              form={form}
              activePromptId={activePromptId}
              canForceLaunchPrompt={canForceLaunchPrompt}
              promptAssetBucket={promptAssetBucket}
              truncate={truncate}
              continuePendingDraft={continuePendingDraft}
              discardPendingDraft={discardPendingDraft}
              deletingId={deletingId}
              openEdit={openEdit}
              editButtonCopy={editButtonCopy}
              copyPromptContent={copyPromptContent}
              clearLaunchPrompt={clearLaunchPrompt}
              activatingId={activatingId}
              activateDisabled={activateDisabled}
              setLaunchPrompt={setLaunchPrompt}
              deletePrompt={deletePrompt}
              deleteDisabled={deleteDisabled}
              fallbackMode={fallbackMode}
            />
          ))}
        </div>
      )}

      {notice.message && !editorOpen && (
        <div className={`sp-notice is-${notice.level}`} data-testid="sp-notice">
          {notice.message}
        </div>
      )}
    </div>
  );
}

function SystemPromptEditorModal({
  editorOpen, fallbackMode, readonlyBannerMessage, form, saving,
  cwdDisplay, closeEditor, editorTitleCopy, editorMode,
  roles, advancedDebugAvailable, advancedDebugOpen,
  matchWhenDirty, saveDisabled, saveButtonCopy, savePrompt,
  currentProjectCwd, setAdvancedDebugOpen
}) {
  if (!editorOpen) return null;

  return (
    <div
      className="modal-overlay sp-editor-overlay"
      data-testid="sp-editor-overlay"
      tabIndex={0}
      onClick={(e) => { if (e.target === e.currentTarget) closeEditor(); }}
      onKeyDown={(e) => { if (e.key === 'Escape') closeEditor(); }}
    >
      <div className="modal-box sp-editor-modal" role="dialog" aria-modal="true" data-testid="sp-editor-panel">
        <div className="sp-editor-head">
          <div>
            <div className="modal-title">{editorTitleCopy(fallbackMode, editorMode)}</div>
            <div className="sp-editor-tip">{form.scope === 'global' ? '全局可用' : '这个项目'} · {cwdDisplay}</div>
          </div>
          <button className="btn btn-ghost" data-testid="sp-editor-close-btn" onClick={closeEditor}>关闭</button>
        </div>

        <div className="sp-editor-body" data-testid="sp-editor-basic">
          {fallbackMode && (
            <div className="sp-notice is-warn" data-testid="sp-editor-readonly-banner">
              {readonlyBannerMessage}
            </div>
          )}

          <div className="sp-scope-copy" data-testid="sp-scope-copy">
            <div>可用范围：{form.scope === 'global' ? '全局可用' : '这个项目'}</div>
            <div className="sp-scope-segmented" data-testid="sp-editor-scope-group">
              <label className={`sp-scope-option ${form.scope !== 'global' ? 'active' : ''}`}>
                <input
                  type="radio"
                  name="editorScope"
                  value="project"
                  checked={form.scope !== 'global'}
                  data-testid="sp-editor-scope-project"
                  disabled={saving || fallbackMode}
                  onChange={(e) => { form.scope = e.target.value; }}
                />
                <span>这个项目</span>
              </label>
              <label className={`sp-scope-option ${form.scope === 'global' ? 'active' : ''}`}>
                <input
                  type="radio"
                  name="editorScope"
                  value="global"
                  checked={form.scope === 'global'}
                  data-testid="sp-editor-scope-global"
                  disabled={saving || fallbackMode}
                  onChange={(e) => { form.scope = e.target.value; }}
                />
                <span>全局可用</span>
              </label>
            </div>
            <div>{form.scope === 'global' ? '说明：其他项目也可以使用；当前项目同名资产优先。' : '说明：只在当前项目的对话中使用。'}</div>
          </div>

          <div className="sp-field">
            <label>名称</label>
            <input
              className="modal-input"
              data-testid="sp-name-input"
              value={form.name}
              placeholder="例如：代码审查专家"
              disabled={saving || fallbackMode}
              onChange={(e) => { form.name = e.target.value; }}
            />
          </div>

          <div className="sp-field">
            <label>一句话描述</label>
            <input
              className="modal-input"
              data-testid="sp-desc-input"
              value={form.description}
              placeholder="简要说明用途"
              disabled={saving || fallbackMode}
              onChange={(e) => { form.description = e.target.value; }}
            />
          </div>

          <div className="sp-field">
            <label>AI 什么时候会使用它</label>
            <textarea
              className="sp-textarea"
              rows={3}
              value={form.whenToUse}
              placeholder="例如：当用户需要代码审查、缺陷定位或提交前风险检查时使用"
              disabled={saving || fallbackMode}
              data-testid="sp-when-to-use-input"
              onChange={(e) => { form.whenToUse = e.target.value; }}
            ></textarea>
          </div>

          <div className="sp-field">
            <label>AI 使用时怎么做</label>
            <textarea
              className="sp-textarea"
              rows={5}
              value={form.content}
              placeholder="写给 AI 的执行说明：先做什么、重点检查什么、输出什么结果"
              disabled={saving || fallbackMode}
              data-testid="sp-execution-input"
              onChange={(e) => { form.content = e.target.value; }}
            ></textarea>
          </div>

          <div className="sp-field">
            <label>保存后 AI 会看到什么</label>
            <textarea
              className="sp-textarea sp-textarea-readonly"
              data-testid="sp-preview-input"
              rows={3}
              value={form.content || form.whenToUse || form.description || '已保存，AI 会在相关场景中使用'}
              readOnly
            ></textarea>
          </div>

          <div className="sp-field">
            <label className="sp-toggle-inline">
              <input
                type="checkbox"
                data-testid="sp-enabled-checkbox"
                checked={!!form.enabled}
                disabled={saving || fallbackMode}
                onChange={(e) => { form.enabled = e.target.checked; }}
              />
              <span>启用状态</span>
            </label>
          </div>

          {advancedDebugAvailable && (
            <details
              className="sp-advanced-debug"
              data-testid="sp-advanced-debug"
              onToggle={(e) => setAdvancedDebugOpen(e.target.open)}
            >
              <summary>高级调试</summary>
              <div className="sp-advanced-body">
                <div className="sp-field">
                  <label>Agent Key</label>
                  <select
                    className="modal-input"
                    value={form.agentKey}
                    disabled={saving || fallbackMode}
                    data-testid="sp-agent-key-select"
                    onChange={(e) => { form.agentKey = e.target.value; }}
                  >
                    <option value="">未分类</option>
                    {roles.map((r) => (
                      <option key={r.key} value={r.key}>{r.key}</option>
                    ))}
                  </select>
                </div>

                <div className="sp-field">
                  <label>场景标签</label>
                  <TagInput
                    modelValue={form.tags}
                    placeholder="输入标签后按回车"
                    disabled={saving || fallbackMode}
                    data-testid="sp-tags-input"
                    onChange={(val) => { form.tags = val; }}
                  />
                </div>

                <div className="sp-field">
                  <label>自动匹配 JSON</label>
                  <textarea
                    className="sp-textarea"
                    rows={4}
                    value={form.matchWhen}
                    placeholder='{"cwd_prefix":"/repo"}'
                    disabled={saving || fallbackMode}
                    data-testid="sp-match-when-input"
                    onChange={(e) => {
                      form.matchWhen = e.target.value;
                      matchWhenDirty.value = true;
                    }}
                  ></textarea>
                </div>

                <div className="sp-field">
                  <label>排序权重</label>
                  <input
                    type="number"
                    className="modal-input"
                    value={form.priority}
                    disabled={saving || fallbackMode}
                    data-testid="sp-priority-input"
                    onChange={(e) => { form.priority = e.target.value === '' ? '' : Number(e.target.value); }}
                  />
                </div>

                {advancedDebugOpen && (
                  <SectionsEditor
                    promptId={form.id}
                    cwd={currentProjectCwd}
                    promptScope={form.scope}
                    fallbackMode={fallbackMode}
                    visible={advancedDebugOpen}
                  />
                )}
              </div>
            </details>
          )}

          {form.notice?.message && (
            <div className={`sp-notice is-${form.notice.level}`} data-testid="sp-editor-notice">
              {form.notice.message}
            </div>
          )}

          <div className="sp-editor-actions" data-testid="sp-editor-actions">
            <button className="btn btn-ghost" onClick={closeEditor}>取消</button>
            <button className="btn btn-primary sp-save-btn" data-testid="sp-save-btn" disabled={saveDisabled} onClick={savePrompt}>
              {saveButtonCopy(fallbackMode, saving)}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

export function SystemPromptPage(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const activeTab = val(vm.activeTab);
  const fallbackMode = val(vm.fallbackMode);
  const scopeFilter = val(vm.scopeFilter);
  const statusFilter = val(vm.statusFilter);
  const filteredCards = val(vm.filteredCards) || [];
  const loading = val(vm.loading);
  const createDisabled = val(vm.createDisabled);
  const readonlyBannerMessage = val(vm.readonlyBannerMessage);
  const editorOpen = val(vm.editorOpen);
  const editorMode = val(vm.editorMode);
  const saving = val(vm.saving);
  const activePromptId = val(vm.activePromptId);
  const deletingId = val(vm.deletingId);
  const deleteDisabled = val(vm.deleteDisabled);
  const activatingId = val(vm.activatingId);
  const activateDisabled = val(vm.activateDisabled);
  const notice = vm.notice || {};
  const form = vm.form || {};
  const currentProjectCwd = val(vm.currentProjectCwd);
  const advancedDebugAvailable = val(vm.advancedDebugAvailable);
  const advancedDebugOpen = val(vm.advancedDebugOpen);
  const saveDisabled = val(vm.saveDisabled);
  const roles = val(vm.roles) || [];
  const matchWhenDirty = vm.matchWhenDirty;

  const intentWizardOpen = val(vm.intentWizardOpen);
  const intentWizardInitialDraft = val(vm.pendingDraftForWizard);

  return (
    <section id="page-prompts" className="page active" data-testid="system-prompt-page">
      <SystemPromptHeader title="AI 能力与资料" cwdDisplay={props.cwdDisplay || `当前项目：${val(vm.currentProjectCwd)}`} />

      <SystemPromptTabs
        activeTab={activeTab}
        fallbackMode={fallbackMode}
        PROMPT_ASSET_TABS={vm.assetTabs}
        switchTab={vm.switchTab}
        scopeFilters={vm.scopeFilters}
        scopeFilter={scopeFilter}
        switchScopeFilter={vm.switchScopeFilter}
        statusFilters={vm.statusFilters}
        statusFilter={statusFilter}
        switchStatusFilter={vm.switchStatusFilter}
      />

      <SystemPromptList
        loading={loading}
        fallbackMode={fallbackMode}
        createDisabled={createDisabled}
        openCreate={vm.openCreate}
        loadPrompts={vm.loadPrompts}
        readonlyBannerMessage={readonlyBannerMessage}
        filteredCards={filteredCards}
        emptyStateCopy={vm.emptyStateCopy}
        activeTab={activeTab}
        editorOpen={editorOpen}
        form={form}
        activePromptId={activePromptId}
        canForceLaunchPrompt={vm.canForceLaunchPrompt}
        truncate={vm.truncate}
        continuePendingDraft={vm.continuePendingDraft}
        discardPendingDraft={vm.discardPendingDraft}
        deletingId={deletingId}
        openEdit={vm.openEdit}
        editButtonCopy={vm.editButtonCopy}
        copyPromptContent={vm.copyPromptContent}
        clearLaunchPrompt={vm.clearLaunchPrompt}
        activatingId={activatingId}
        setLaunchPrompt={vm.setLaunchPrompt}
        deletePrompt={vm.deletePrompt}
        deleteDisabled={deleteDisabled}
        notice={notice}
        promptAssetBucket={vm.promptAssetBucket}
        activateDisabled={activateDisabled}
      />

      <SystemPromptEditorModal
        editorOpen={editorOpen}
        fallbackMode={fallbackMode}
        readonlyBannerMessage={readonlyBannerMessage}
        form={form}
        saving={saving}
        cwdDisplay={props.cwdDisplay || `当前项目：${currentProjectCwd}`}
        closeEditor={vm.closeEditor}
        editorTitleCopy={vm.editorTitleCopy}
        editorMode={editorMode}
        roles={roles}
        advancedDebugAvailable={advancedDebugAvailable}
        advancedDebugOpen={advancedDebugOpen}
        matchWhenDirty={matchWhenDirty}
        saveDisabled={saveDisabled}
        saveButtonCopy={vm.saveButtonCopy}
        savePrompt={vm.savePrompt}
        currentProjectCwd={currentProjectCwd}
        setAdvancedDebugOpen={(val) => { vm.advancedDebugOpen.value = val; }}
      />

      <PromptIntentWizard
        cwd={currentProjectCwd}
        visible={intentWizardOpen}
        fallbackMode={fallbackMode}
        initialDraft={intentWizardInitialDraft}
        onClose={vm.handleIntentClosed}
        onSaved={vm.handleIntentSaved}
      />
    </section>
  );
}
