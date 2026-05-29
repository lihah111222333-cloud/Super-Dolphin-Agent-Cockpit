import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { MemoryCenterPage as VueComp } from './MemoryCenterPage.js';

function MemoryBentoPanel({
  totalEntries,
  preferenceEntries,
  projectEntries,
  health,
  healthPrefPercent,
  healthProjPercent,
  autoDreamEnabled,
  autoDreamStatusLabel,
  autoDreamPendingRestart,
  autoDreamToggling,
  vm
}) {
  return (
    <div className={`mc-bento ${!health ? 'mc-bento-2col' : ''}`}>
      <div className="mc-bento-card">
        <div className="mc-bento-label">
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
            <rect x="2" y="2" width="12" height="12" rx="2" />
            <path d="M2 6h12M6 6v8" />
          </svg>
          总览
        </div>
        <div className="mc-bento-num">{totalEntries}</div>
        <div className="mc-bento-sub">
          <span><span className="mc-dot mc-dot-pref"></span>{preferenceEntries.length} 偏好</span>
          <span><span className="mc-dot mc-dot-proj"></span>{projectEntries.length} 项目</span>
        </div>
      </div>

      {health && (
        <div className="mc-bento-card" data-testid="memory-center-health-card">
          <div className="mc-bento-label">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
              <path d="M8 2C5.5 2 2 4.5 2 7.5 2 12 8 14.5 8 14.5S14 12 14 7.5C14 4.5 10.5 2 8 2Z" />
            </svg>
            健康度
          </div>
          <div className="mc-health-row">
            <span className="mc-health-lbl">偏好</span>
            <div className="mc-health-track">
              <div className={`mc-health-fill ${vm.healthBarClass(healthPrefPercent)}`} style={{ width: healthPrefPercent + '%' }}></div>
            </div>
            <span className="mc-health-val">{health.preferenceCount} / {health.maxPerCategory}</span>
          </div>
          <div className="mc-health-row">
            <span className="mc-health-lbl">项目</span>
            <div className="mc-health-track">
              <div className={`mc-health-fill ${vm.healthBarClass(healthProjPercent)}`} style={{ width: healthProjPercent + '%' }}></div>
            </div>
            <span className="mc-health-val">{health.projectCount} / {health.maxPerCategory}</span>
          </div>
          <div style={{ marginTop: '8px', fontSize: '11px', color: 'var(--text-muted)', display: 'flex', alignItems: 'center', gap: '6px' }}>
            <span className="mc-status-dot on"></span> 综合良好
          </div>
        </div>
      )}

      <div className="mc-bento-card" data-testid="memory-center-auto-dream-card">
        <div className="mc-bento-label">
          <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
            <path d="M13.5 8.5a5.5 5.5 0 11-6-6 4 4 0 006 6z" />
          </svg>
          自动沉淀
        </div>
        <div className="mc-auto-status">
          <span className={`mc-status-dot ${autoDreamEnabled ? 'on' : 'off'}`} data-testid="memory-center-auto-dream-status"></span>
          {autoDreamStatusLabel}
        </div>
        <div className="mc-auto-sub">对话结束后自动整理重要内容</div>
        <button className="mc-auto-toggle" disabled={autoDreamToggling} data-testid="memory-center-auto-dream-toggle" onClick={vm.toggleAutoDream}>
          {autoDreamEnabled ? '关闭' : '开启'}
        </button>
        {autoDreamPendingRestart && (
          <div className="mc-auto-pending" data-testid="memory-center-auto-dream-pending">已保存切换，重启 agent-terminal 后生效</div>
        )}
      </div>
    </div>
  );
}

function SimilarGroupBar({
  health,
  mergingAll,
  mergingGroup,
  ignoringGroup,
  similarExpanded,
  vm
}) {
  const similarGroups = health?.similarGroups || [];
  if (similarGroups.length === 0) return null;

  return (
    <div className="mc-similar-bar stack-folder">
      <div className="stack-sheet-1"></div>
      <div className="stack-sheet-2"></div>
      <div className="mc-similar-head">
        <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5" style={{ flexShrink: 0 }}>
          <path d="M8 1.5L14.5 13H1.5Z" strokeLinejoin="round" />
          <path d="M8 6v3" strokeLinecap="round" />
          <circle cx="8" cy="11.5" r="0.5" fill="currentColor" />
        </svg>
        <span>{similarGroups.length} 组条目内容相似</span>
        {similarGroups.length > 1 && (
          <button className="mc-similar-btn" disabled={mergingAll || mergingGroup !== null || ignoringGroup !== null} onClick={vm.mergeAllGroups}>
            {mergingAll ? '整合中...' : '一键整合全部'}
          </button>
        )}
        <button className="mc-similar-btn" onClick={vm.toggleSimilarExpand}>
          {similarExpanded ? '收起' : '展开'}
        </button>
      </div>
      {similarExpanded && (
        <div className="mc-similar-list">
          {similarGroups.map((group, gi) => (
            <div key={vm.pairKey(group)} className="mc-similar-item">
              <span className="mc-similar-names">「{group.nameA}」与「{group.nameB}」</span>
              <span className="mc-similar-score">{vm.formatScore(group.score)}</span>
              <button className="btn btn-secondary btn-xs" disabled={mergingGroup !== null || mergingAll || ignoringGroup !== null} onClick={() => vm.askMergeGroup(group, gi)}>整合</button>
              <button className="btn btn-ghost btn-xs" style={{ opacity: 0.5 }} disabled={ignoringGroup !== null || mergingAll || mergingGroup !== null} onClick={() => vm.ignoreGroup(group)}>{ignoringGroup === vm.pairKey(group) ? '...' : '忽略'}</button>
            </div>
          ))}
        </div>
      )}
    </div>
  );
}

function MemoryEntryCard({
  entry,
  idx,
  busyPath,
  vm
}) {
  return (
    <article
      className={`mc-entry-card ${entry.type === 'project' || entry.type === 'reference' ? 'type-proj' : 'type-pref'}`}
    >
      <div className="mc-entry-head">
        <div className="mc-entry-title">{entry.title || vm.headlineOf(entry.description) || entry.name || '未命名'}</div>
        <div className="mc-entry-badges">
          <span className={`jr-badge ${vm.typeBadgeClass(entry.type)}`}>{vm.typeBadgeLabel(entry.type)}</span>
        </div>
      </div>
      {entry.description && <div className="mc-entry-desc">{entry.description}</div>}
      <pre className="mc-entry-preview" onClick={(e) => e.currentTarget.classList.toggle('is-expanded')}>{entry.preview || '暂无预览'}</pre>
      <div className="mc-entry-foot">
        <span className="mc-entry-time">{vm.formatTimestamp(entry.updatedAt)}</span>
        <div className="mc-entry-actions">
          <button className="btn btn-secondary btn-xs" data-testid={'mc-entry-edit-' + idx} disabled={busyPath === entry._target + ':' + entry.path} onClick={() => vm.memoryEditor.openEdit(entry._target, entry)}>
            {busyPath === entry._target + ':' + entry.path ? '加载中...' : '编辑'}
          </button>
          <button className="btn btn-danger btn-xs" data-testid={'mc-entry-delete-' + idx} onClick={() => vm.inlineDelete.ask(entry._target, entry)}>删除</button>
        </div>
      </div>
    </article>
  );
}

function MemoryDeleteModal({ inlineDelete }) {
  if (!inlineDelete.target) return null;

  return (
    <div className="modal-overlay mc-modal-overlay" data-testid="memory-center-inline-delete-overlay" onClick={(e) => { if (e.target === e.currentTarget) inlineDelete.cancel(); }}>
      <div className="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-inline-delete-modal">
        <div className="mc-panel-head">
          <div>
            <div className="modal-title">删除记忆</div>
            <div className="mc-panel-tip">{inlineDelete.target.name} · {inlineDelete.target.target}</div>
          </div>
          <button className="btn btn-ghost" disabled={inlineDelete.deleting} onClick={inlineDelete.cancel}>关闭</button>
        </div>
        <div className="mc-form-helper">删除后无法恢复。如果后续可能重用，建议先“编辑”备份内容。</div>
        <div className="mc-panel-actions">
          <button className="btn btn-ghost" disabled={inlineDelete.deleting} onClick={inlineDelete.cancel}>取消</button>
          <button className="btn btn-danger" data-testid="memory-center-inline-delete-confirm" disabled={inlineDelete.deleting} onClick={inlineDelete.confirm}>
            {inlineDelete.deleting ? '删除中...' : '确认删除'}
          </button>
        </div>
      </div>
    </div>
  );
}

function MemoryMergeModal({ mergeConfirm, mergingGroup, vm, resetMergeConfirm, confirmMergeGroup }) {
  if (!mergeConfirm.target) return null;

  return (
    <div className="modal-overlay mc-modal-overlay" data-testid="memory-center-merge-overlay" onClick={(e) => { if (e.target === e.currentTarget) resetMergeConfirm(); }}>
      <div className="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-merge-modal">
        <div className="mc-panel-head">
          <div>
            <div className="modal-title">整合相似记忆</div>
            <div className="mc-panel-tip">相似度 {vm.formatScore(mergeConfirm.target.score)}</div>
          </div>
          <button className="btn btn-ghost" disabled={mergingGroup !== null} onClick={resetMergeConfirm}>关闭</button>
        </div>
        <div className="mc-form-helper">
          <div>合并到：{mergeConfirm.target.nameA} · {mergeConfirm.target.targetA}（内容将合并）</div>
          <div>移除：{mergeConfirm.target.nameB} · {mergeConfirm.target.targetB}（合并后删除）</div>
          {mergeConfirm.crossScope && <div className="mc-notice is-warning" style={{ marginTop: '8px' }}>跨作用域相似条目不会自动整合，请手动整理。</div>}
        </div>
        <div className="mc-panel-actions">
          <button className="btn btn-ghost" disabled={mergingGroup !== null} onClick={resetMergeConfirm}>取消</button>
          <button className="btn btn-primary" data-testid="memory-center-merge-confirm" disabled={mergingGroup !== null || mergeConfirm.crossScope} onClick={confirmMergeGroup}>
            {mergingGroup !== null ? '整合中...' : '确认整合'}
          </button>
        </div>
      </div>
    </div>
  );
}

function MemoryEditPanel({ memoryEditor, memoryIdentityLocked, askEditorDelete }) {
  if (!memoryEditor.open) return null;

  return (
    <>
      <div className="mc-panel-overlay" onClick={memoryEditor.close}></div>
      <div className="mc-panel is-open" data-testid="memory-center-editor">
        <div className="mc-panel-head">
          <div>
            <div className="modal-title">{memoryEditor.mode === 'edit' ? '编辑记忆' : '新建记忆'}</div>
            <div className="mc-panel-tip">{memoryEditor.form.target === 'team' ? '团队记忆' : '私有记忆'}</div>
          </div>
          <button className="btn btn-ghost" data-testid="memory-center-editor-close" onClick={memoryEditor.close}>×</button>
        </div>
        <div className="mc-form-row">
          <label className="mc-form-label">目标</label>
          <select value={memoryEditor.form.target} className="modal-input" data-testid="memory-center-editor-target" disabled={memoryIdentityLocked} onChange={(e) => { memoryEditor.form.target = e.target.value; }}>
            <option value="private">私有</option>
            <option value="team">团队</option>
          </select>
        </div>
        <div className="mc-form-row">
          <label className="mc-form-label">类型</label>
          <select value={memoryEditor.form.type} className="modal-input" data-testid="memory-center-editor-type" disabled={memoryIdentityLocked} onChange={(e) => { memoryEditor.form.type = e.target.value; }}>
            <option value="feedback">偏好</option>
            <option value="project">项目</option>
          </select>
        </div>
        <div className="mc-form-row">
          <label className="mc-form-label">标识名</label>
          <input value={memoryEditor.form.name || ''} className="modal-input" data-testid="memory-center-editor-name" disabled={memoryIdentityLocked} placeholder="内部标识，如 reply-in-chinese" onChange={(e) => { memoryEditor.form.name = e.target.value; }} />
        </div>
        <div className="mc-form-row">
          <label className="mc-form-label">描述</label>
          <input value={memoryEditor.form.description || ''} className="modal-input" data-testid="memory-center-editor-description" placeholder="一句话描述为什么值得长期保留" onChange={(e) => { memoryEditor.form.description = e.target.value; }} />
        </div>
        <div className="mc-form-row">
          <label className="mc-form-label">卡片标题</label>
          <input value={memoryEditor.form.title || ''} className="modal-input" data-testid="memory-center-editor-title" placeholder="卡片上显示的短标题，留空则自动截取描述" onChange={(e) => { memoryEditor.form.title = e.target.value; }} />
        </div>
        {memoryIdentityLocked && (
          <div className="mc-form-helper">
            现有记忆的标识名和类型暂时锁定；如需修改，请删除后重建。
          </div>
        )}
        <div className="mc-form-row">
          <label className="mc-form-label">内容</label>
          <textarea value={memoryEditor.form.content || ''} rows={12} className="modal-input mc-form-textarea" data-testid="memory-center-editor-content" onChange={(e) => { memoryEditor.form.content = e.target.value; }}></textarea>
        </div>
        <div className="mc-form-helper">
          <button className="btn btn-secondary btn-xs" data-testid="memory-center-editor-template" onClick={memoryEditor.fillTemplate}>套用当前类型模板</button>
        </div>
        <div className="mc-panel-actions">
          <button className="btn btn-ghost" data-testid="memory-center-editor-cancel" onClick={memoryEditor.close}>取消</button>
          {memoryEditor.form.existingPath && (
            <button className="btn btn-danger" data-testid="memory-center-editor-delete" disabled={memoryEditor.deleting} onClick={askEditorDelete}>
              删除
            </button>
          )}
          <button
            className="btn btn-primary"
            data-testid="memory-center-editor-save"
            disabled={memoryEditor.saving || !(memoryEditor.form.name || '').trim() || !(memoryEditor.form.description || '').trim() || !(memoryEditor.form.content || '').trim()}
            onClick={memoryEditor.save}
          >
            {memoryEditor.saving ? '保存中...' : '保存'}
          </button>
        </div>
      </div>
    </>
  );
}

export function MemoryCenterPage(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const notice = vm.notice || {};
  const busyPath = val(vm.busyPath);
  const searchText = val(vm.searchText);
  const refreshing = val(vm.refreshing);
  const activeTab = val(vm.activeTab);
  const createMenuOpen = val(vm.createMenuOpen);
  const searchExpanded = val(vm.searchExpanded);
  const health = val(vm.health);
  const healthPrefPercent = val(vm.healthPrefPercent);
  const healthProjPercent = val(vm.healthProjPercent);
  const autoDreamEnabled = val(vm.autoDreamEnabled);
  const autoDreamStatusLabel = val(vm.autoDreamStatusLabel);
  const autoDreamPendingRestart = val(vm.autoDreamPendingRestart);
  const autoDreamToggling = val(vm.autoDreamToggling);
  const similarExpanded = val(vm.similarExpanded);
  const mergingAll = val(vm.mergingAll);
  const mergingGroup = val(vm.mergingGroup);
  const mergeConfirm = vm.mergeConfirm || {};
  const inlineDelete = vm.inlineDelete || {};
  const memoryEditor = vm.memoryEditor || {};
  const memoryIdentityLocked = val(vm.memoryIdentityLocked);
  const preferenceEntries = val(vm.preferenceEntries) || [];
  const projectEntries = val(vm.projectEntries) || [];
  const totalEntries = val(vm.totalEntries);
  const visibleEntries = val(vm.visibleEntries) || [];
  const isLoading = val(vm.isLoading);
  const model = props.model || {};

  return (
    <section id="page-memory-center" className="page active mc-page" data-testid="memory-center-page">
      <div className="panel-header">
        <div className="ph-bar"></div>
        <div className="ph-text">
          <h2><span className="mc-toolbar-icon">M</span> 记忆中心</h2>
        </div>
        <div className="mc-toolbar" data-testid="memory-center-toolbar">
          <div className={`mc-search-wrap ${searchExpanded ? 'is-open' : ''}`}>
            <button className="mc-search-toggle" aria-label="搜索" onClick={(e) => { e.stopPropagation(); vm.toggleSearch(); }}>
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
                <circle cx="7" cy="7" r="4.5" />
                <path d="M10.5 10.5l3 3" strokeLinecap="round" />
              </svg>
            </button>
            <input
              value={searchText}
              className="mc-search-input"
              data-testid="memory-center-search"
              placeholder="搜索 name / description / path"
              onChange={(e) => { vm.searchText.value = e.target.value; }}
            />
            {searchText && <button className="mc-search-clear" data-testid="memory-center-search-clear" aria-label="清除" onClick={vm.clearSearch}>×</button>}
          </div>
          <button className="btn btn-ghost btn-toolbar-sm" data-testid="memory-center-refresh" disabled={refreshing} onClick={vm.handleRefresh}>
            {refreshing && <span className="memory-refresh-spin" aria-hidden="true"></span>}
            {refreshing ? '刷新中' : '刷新'}
          </button>
          <div className="mc-create-dropdown" onClick={(e) => e.stopPropagation()}>
            <button className="btn btn-primary btn-toolbar-sm" onClick={vm.toggleCreateMenu}>+ 新建 ▾</button>
            {createMenuOpen && (
              <div className="mc-create-menu">
                <button className="mc-create-option" onClick={vm.handleCreatePreference}>新建偏好</button>
                <button className="mc-create-option" onClick={vm.handleCreateProject}>新建项目</button>
              </div>
            )}
          </div>
        </div>
      </div>

      <div className="panel-body mc-body" data-testid="memory-center-body">
        <MemoryBentoPanel
          totalEntries={totalEntries}
          preferenceEntries={preferenceEntries}
          projectEntries={projectEntries}
          health={health}
          healthPrefPercent={healthPrefPercent}
          healthProjPercent={healthProjPercent}
          autoDreamEnabled={autoDreamEnabled}
          autoDreamStatusLabel={autoDreamStatusLabel}
          autoDreamPendingRestart={autoDreamPendingRestart}
          autoDreamToggling={autoDreamToggling}
          vm={vm}
        />

        <SimilarGroupBar
          health={health}
          mergingAll={mergingAll}
          mergingGroup={mergingGroup}
          ignoringGroup={val(vm.ignoringGroup)}
          similarExpanded={similarExpanded}
          vm={vm}
        />

        {notice.message && <div className={`mc-notice memory-notice-fade is-${notice.level}`} data-testid="memory-center-notice">{notice.message}</div>}
        {isLoading && <div className="mc-notice is-info" data-testid="memory-center-loading">正在加载记忆中心...</div>}
        {model.error && <div className="mc-notice is-error" data-testid="memory-center-error">{model.error}</div>}

        <div className="mc-tabs">
          <div className={`mc-tab ${activeTab === 'pref' ? 'active' : ''}`} onClick={() => vm.switchTab('pref')}>
            <span className="mc-dot mc-dot-pref"></span>
            偏好 <span className="mc-tab-count">{preferenceEntries.length}</span>
          </div>
          <div className={`mc-tab ${activeTab === 'proj' ? 'active' : ''}`} onClick={() => vm.switchTab('proj')}>
            <span className="mc-dot mc-dot-proj"></span>
            项目 <span className="mc-tab-count">{projectEntries.length}</span>
          </div>
          <div className={`mc-tab ${activeTab === 'all' ? 'active' : ''}`} onClick={() => vm.switchTab('all')}>
            全部 <span className="mc-tab-count">{totalEntries}</span>
          </div>
        </div>

        {visibleEntries.length === 0 ? (
          <div className="mc-empty">
            <svg className="mc-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
              <path d="M10 14h28v26H10z" opacity="0.35" />
              <path d="M14 20h20M14 26h20M14 32h14" strokeLinecap="round" opacity="0.6" />
              <circle cx="34" cy="14" r="5" fill="var(--surface)" stroke="currentColor" />
              <path d="M32 14h4M34 12v4" strokeLinecap="round" />
            </svg>
            <div className="mc-empty-title">{searchText ? '没有匹配的条目' : '暂无记忆'}</div>
            {!searchText && <div className="mc-empty-text">点击右上角“新建”按钮开始添加记忆。</div>}
            {searchText && (
              <div className="mc-empty-actions">
                <button className="btn btn-secondary btn-toolbar-sm" onClick={vm.clearSearch}>清空搜索</button>
              </div>
            )}
          </div>
        ) : (
          <div className="mc-entry-grid">
            {visibleEntries.map((entry, idx) => (
              <MemoryEntryCard
                key={entry._target + ':' + (entry.path || entry.name || idx)}
                entry={entry}
                idx={idx}
                busyPath={busyPath}
                vm={vm}
              />
            ))}
          </div>
        )}
      </div>

      <MemoryDeleteModal inlineDelete={inlineDelete} />

      <MemoryMergeModal
        mergeConfirm={mergeConfirm}
        mergingGroup={mergingGroup}
        vm={vm}
        resetMergeConfirm={vm.resetMergeConfirm}
        confirmMergeGroup={vm.confirmMergeGroup}
      />

      <MemoryEditPanel
        memoryEditor={memoryEditor}
        memoryIdentityLocked={memoryIdentityLocked}
        askEditorDelete={vm.askEditorDelete}
      />
    </section>
  );
}
