import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { SectionsEditor as VueComp } from './SectionsEditor.js';

function SectionsEditorCard({
  item, idx, sectionCardKey, isSectionExpanded, normalizeTriggerType,
  toggleSection, sectionDisplayName, sectionSummary, fallbackMode,
  openEdit, sectionDeletingKey, deleteSection
}) {
  const cardKey = sectionCardKey(item, idx);
  const isExpanded = isSectionExpanded(item, idx);
  const triggerType = normalizeTriggerType(item.trigger_type);
  const classes = [
    'data-card-vue',
    'sp-section-card',
    item.enabled === false ? 'is-disabled' : '',
    isExpanded ? 'is-open' : '',
    triggerType === 'recall' ? 'is-recall' : ''
  ].filter(Boolean).join(' ');

  return (
    <article
      key={cardKey}
      className={classes}
      data-testid={`sp-section-card-${idx}`}
    >
      <button
        type="button"
        className="sp-section-toggle"
        data-testid={`sp-section-toggle-${idx}`}
        onClick={() => toggleSection(item, idx)}
      >
        <span className="sp-section-caret">{isExpanded ? '▾' : '▸'}</span>
        <span className="sp-section-title-block">
          <span className="sp-section-friendly-name">{sectionDisplayName(item)}</span>
          <span className="sp-section-key">{item.section_key}</span>
        </span>
        <span className="sp-section-summary">{sectionSummary(item.body, 150)}</span>
        {triggerType === 'recall' && <span className="sp-section-badge is-recall">🔗 Recall</span>}
        {item.enabled === false && <span className="sp-section-badge is-disabled">已停用</span>}
      </button>

      {isExpanded && (
        <div className="sp-section-expanded">
          <textarea
            className="sp-textarea sp-textarea-readonly sp-section-body-textarea"
            rows={5}
            value={item.body || ''}
            readOnly
          ></textarea>
          <details className="sp-section-advanced">
            <summary>高级字段</summary>
            <dl className="sp-section-fields">
              <div><dt>region</dt><dd>{item.region || 'dynamic'}</dd></div>
              <div><dt>ordinal</dt><dd>{item.ordinal || 0}</dd></div>
              <div><dt>trigger_type</dt><dd>{triggerType}</dd></div>
              {item.recall_topic && <div><dt>recall_topic</dt><dd>{item.recall_topic}</dd></div>}
              {item.enable_when && <div><dt>enable_when</dt><dd><code>{JSON.stringify(item.enable_when)}</code></dd></div>}
            </dl>
          </details>
          <div className="sp-section-actions">
            <button
              className="btn btn-secondary btn-xs"
              data-testid={`sp-section-edit-btn-${idx}`}
              disabled={fallbackMode}
              onClick={() => openEdit(item)}
            >
              编辑
            </button>
            <button
              className="btn btn-ghost btn-xs btn-warning"
              data-testid={`sp-section-delete-btn-${idx}`}
              disabled={fallbackMode || sectionDeletingKey === item.section_key}
              onClick={() => deleteSection(item)}
            >
              {sectionDeletingKey === item.section_key ? '删除中...' : '删除'}
            </button>
          </div>
        </div>
      )}
    </article>
  );
}

function SectionsEditorList({
  promptId, sectionsLoading, fallbackMode, sectionsList, expandedKeys,
  sectionDeletingKey, openCreate, loadSections, sectionCardKey,
  isSectionExpanded, toggleSection, sectionDisplayName, sectionSummary,
  normalizeTriggerType, openEdit, deleteSection, vm
}) {
  return (
    <>
      <div className="sp-sections-toolbar" data-testid="sp-sections-toolbar">
        <button
          className="btn btn-secondary btn-xs"
          data-testid="sp-section-create-btn"
          disabled={!promptId || sectionsLoading || fallbackMode}
          onClick={openCreate}
        >
          + 新增分段
        </button>
        <button
          className="btn btn-ghost btn-xs"
          data-testid="sp-sections-refresh-btn"
          disabled={!promptId || sectionsLoading}
          onClick={loadSections}
        >
          {sectionsLoading ? '加载中...' : '刷新'}
        </button>
      </div>

      {sectionsLoading ? (
        <div className="sp-loading" data-testid="sp-sections-loading">
          <div className="sp-spinner"></div><span>加载中...</span>
        </div>
      ) : !promptId ? (
        <div className="empty-state" data-testid="sp-sections-unsaved">
          <h3>请先保存提示词</h3>
          <p>保存后即可添加分段。</p>
        </div>
      ) : sectionsList.length === 0 ? (
        <div className="empty-state" data-testid="sp-sections-empty">
          <h3>尚未添加分段</h3>
          <p>点击“新增分段”开始维护注入内容。</p>
        </div>
      ) : (
        <div className="sp-sections-list" data-testid="sp-sections-list">
          {sectionsList.map((item, idx) => (
            <SectionsEditorCard
              key={sectionCardKey(item, idx)}
              item={item}
              idx={idx}
              sectionCardKey={sectionCardKey}
              isSectionExpanded={isSectionExpanded}
              normalizeTriggerType={normalizeTriggerType}
              toggleSection={toggleSection}
              sectionDisplayName={sectionDisplayName}
              sectionSummary={sectionSummary}
              fallbackMode={fallbackMode}
              openEdit={openEdit}
              sectionDeletingKey={sectionDeletingKey}
              deleteSection={deleteSection}
            />
          ))}
        </div>
      )}
    </>
  );
}

function SectionsEditorModal({
  sectionEditorOpen, sectionEditorMode, sectionSaving, fallbackMode,
  sectionForm, closeSectionEditor, saveSection, vm
}) {
  if (!sectionEditorOpen) return null;

  return (
    <div
      className="modal-overlay sp-section-editor-overlay"
      data-testid="sp-section-editor-overlay"
      onClick={(e) => { if (e.target === e.currentTarget) closeSectionEditor(); }}
      onKeyDown={(e) => { if (e.key === 'Escape') closeSectionEditor(); }}
    >
      <div className="modal-box sp-section-editor-modal" role="dialog" aria-modal="true">
        <div className="sp-editor-head">
          <div className="modal-title">{sectionEditorMode === 'create' ? '新增分段' : '编辑分段'}</div>
          <button className="btn btn-ghost" onClick={closeSectionEditor}>关闭</button>
        </div>
        <div className="sp-editor-body">
          <div className="sp-field">
            <label>段名（section_key）</label>
            <input
              className="modal-input"
              value={sectionForm.sectionKey}
              placeholder="identity / tool_prefs / sqlc-workflow"
              disabled={sectionSaving || fallbackMode}
              data-testid="sp-section-key-input"
              onChange={(e) => { sectionForm.sectionKey = e.target.value; }}
            />
          </div>
          <div className="sp-field">
            <label>内容（body）</label>
            <textarea
              className="sp-textarea"
              rows={8}
              value={sectionForm.body}
              placeholder="本段要注入的文本内容..."
              disabled={sectionSaving || fallbackMode}
              data-testid="sp-section-body-input"
              onChange={(e) => { sectionForm.body = e.target.value; }}
            ></textarea>
          </div>
          <details className="sp-section-advanced" open={sectionForm.triggerType === 'recall' || !!sectionForm.enableWhen}>
            <summary>高级字段</summary>
            <div className="sp-section-advanced-form">
              <div className="sp-field">
                <label>region</label>
                <select
                  className="modal-input"
                  value={sectionForm.region}
                  disabled={sectionSaving || fallbackMode}
                  data-testid="sp-section-region-select"
                  onChange={(e) => { sectionForm.region = e.target.value; }}
                >
                  <option value="static">static</option>
                  <option value="dynamic">dynamic</option>
                </select>
              </div>
              <div className="sp-field">
                <label>ordinal</label>
                <input
                  type="number"
                  className="modal-input"
                  value={sectionForm.ordinal}
                  disabled={sectionSaving || fallbackMode}
                  data-testid="sp-section-ordinal-input"
                  onChange={(e) => { sectionForm.ordinal = e.target.value === '' ? '' : Number(e.target.value); }}
                />
              </div>
              <div className="sp-field">
                <label>trigger_type</label>
                <select
                  className="modal-input"
                  value={sectionForm.triggerType}
                  disabled={sectionSaving || fallbackMode}
                  data-testid="sp-section-trigger-select"
                  onChange={(e) => { sectionForm.triggerType = e.target.value; }}
                >
                  <option value="always">always</option>
                  <option value="keyword">keyword</option>
                  <option value="recall">recall</option>
                </select>
              </div>
              {sectionForm.triggerType === 'recall' && (
                <div className="sp-field">
                  <label>recall_topic</label>
                  <input
                    className="modal-input"
                    value={sectionForm.recallTopic}
                    placeholder="sqlc-workflow"
                    disabled={sectionSaving || fallbackMode}
                    data-testid="sp-section-recall-topic-input"
                    onChange={(e) => { sectionForm.recallTopic = e.target.value; }}
                  />
                  <div className="sp-field-meta">lowercase-dash, length &lt; 64</div>
                </div>
              )}
              <div className="sp-field">
                <label>enable_when</label>
                <textarea
                  className="sp-textarea"
                  rows={3}
                  value={sectionForm.enableWhen}
                  placeholder='{"language":"zh","isWorktree":true}'
                  disabled={sectionSaving || fallbackMode}
                  data-testid="sp-section-enable-when-input"
                  onChange={(e) => { sectionForm.enableWhen = e.target.value; }}
                ></textarea>
              </div>
              <div className="sp-field">
                <label className="sp-toggle-inline">
                  <input
                    type="checkbox"
                    checked={!!sectionForm.enabled}
                    disabled={sectionSaving || fallbackMode}
                    data-testid="sp-section-enabled-checkbox"
                    onChange={(e) => { sectionForm.enabled = e.target.checked; }}
                  />
                  <span>enabled</span>
                </label>
              </div>
            </div>
          </details>
          <div className="sp-editor-actions">
            <button className="btn btn-ghost" onClick={closeSectionEditor}>取消</button>
            <button
              className="btn btn-primary"
              disabled={sectionSaving || fallbackMode}
              data-testid="sp-section-save-btn"
              onClick={saveSection}
            >
              {sectionSaving ? '保存中...' : '保存分段'}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}

export function SectionsEditor(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const sectionsLoading = val(vm.sectionsLoading);
  const sectionsList = val(vm.sectionsList) || [];
  const sectionEditorOpen = val(vm.sectionEditorOpen);
  const sectionEditorMode = val(vm.sectionEditorMode);
  const sectionSaving = val(vm.sectionSaving);
  const sectionDeletingKey = val(vm.sectionDeletingKey);
  const notice = vm.notice || {}; // Reactive
  const sectionForm = vm.sectionForm || {}; // Reactive

  return (
    <div className="sp-sections-panel" data-testid="sp-sections-panel">
      <SectionsEditorList
        promptId={props.promptId}
        sectionsLoading={sectionsLoading}
        fallbackMode={props.fallbackMode}
        sectionsList={sectionsList}
        expandedKeys={vm.expandedKeys}
        sectionDeletingKey={sectionDeletingKey}
        openCreate={vm.openCreate}
        loadSections={vm.loadSections}
        sectionCardKey={vm.sectionCardKey}
        isSectionExpanded={vm.isSectionExpanded}
        toggleSection={vm.toggleSection}
        sectionDisplayName={vm.sectionDisplayName}
        sectionSummary={vm.sectionSummary}
        normalizeTriggerType={vm.normalizeTriggerType}
        openEdit={vm.openEdit}
        deleteSection={vm.deleteSection}
        vm={vm}
      />

      {notice.message && (
        <div className={`sp-notice is-${notice.level}`} data-testid="sp-sections-notice">
          {notice.message}
        </div>
      )}

      <SectionsEditorModal
        sectionEditorOpen={sectionEditorOpen}
        sectionEditorMode={sectionEditorMode}
        sectionSaving={sectionSaving}
        fallbackMode={props.fallbackMode}
        sectionForm={sectionForm}
        closeSectionEditor={vm.closeSectionEditor}
        saveSection={vm.saveSection}
        vm={vm}
      />
    </div>
  );
}
