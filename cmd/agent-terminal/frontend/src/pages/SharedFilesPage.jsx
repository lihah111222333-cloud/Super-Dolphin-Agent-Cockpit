import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { SharedFilesPage as VueComp } from './SharedFilesPage.js';

function SharedFilesHeader({
  searchText,
  sortMode,
  fileCategory,
  itemsLength,
  finalOutputCount,
  workFileCount,
  refreshing,
  vm
}) {
  return (
    <div className="panel-header">
      <div className="ph-bar"></div>
      <div className="ph-text"><h2>文件产物</h2></div>
      <div className="memory-center-toolbar" data-testid="shared-files-toolbar">
        <div className="memory-center-search">
          <span className="memory-center-search-icon" aria-hidden="true">
            <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" strokeWidth="1.5">
              <circle cx="7" cy="7" r="4.5" />
              <path d="M10.5 10.5l3 3" strokeLinecap="round" />
            </svg>
          </span>
          <input
            value={searchText}
            className="memory-center-search-input"
            data-testid="shared-files-search"
            placeholder="搜索文件名 / 内容"
            onChange={(e) => { vm.searchText.value = e.target.value; }}
          />
          {searchText && (
            <button
              className="memory-center-search-clear"
              data-testid="shared-files-search-clear"
              aria-label="清除"
              onClick={vm.clearSearch}
            >×</button>
          )}
        </div>
        <select
          value={sortMode}
          className="memory-center-sort-select"
          data-testid="shared-files-sort"
          onChange={vm.changeSort}
        >
          <option value="updated-desc">最新更新</option>
          <option value="updated-asc">最早更新</option>
          <option value="path-asc">按文件名</option>
        </select>
        <div className="shared-files-category-tabs" data-testid="shared-files-category-tabs" role="tablist" aria-label="文件产物分类">
          <button
            type="button"
            className={`shared-files-category-tab ${fileCategory === 'all' ? 'active' : ''}`}
            data-testid="shared-files-category-tab-all"
            role="tab"
            aria-selected={fileCategory === 'all' ? 'true' : 'false'}
            onClick={() => vm.setFileCategory('all')}
          >
            <span>全部</span>
            <span className="shared-files-category-count">{itemsLength}</span>
          </button>
          <button
            type="button"
            className={`shared-files-category-tab ${fileCategory === 'final' ? 'active' : ''}`}
            data-testid="shared-files-category-tab-final"
            role="tab"
            aria-selected={fileCategory === 'final' ? 'true' : 'false'}
            onClick={() => vm.setFileCategory('final')}
          >
            <span>最终产物</span>
            <span className="shared-files-category-count">{finalOutputCount}</span>
          </button>
          <button
            type="button"
            className={`shared-files-category-tab ${fileCategory === 'work' ? 'active' : ''}`}
            data-testid="shared-files-category-tab-work"
            role="tab"
            aria-selected={fileCategory === 'work' ? 'true' : 'false'}
            onClick={() => vm.setFileCategory('work')}
          >
            <span>工作文件</span>
            <span className="shared-files-category-count">{workFileCount}</span>
          </button>
        </div>
        <button
          className="btn btn-secondary btn-toolbar-sm"
          data-testid="shared-files-refresh"
          disabled={refreshing}
          onClick={vm.handleRefresh}
        >
          {refreshing && <span className="memory-refresh-spin" aria-hidden="true"></span>}
          {refreshing ? '刷新中' : '刷新'}
        </button>
      </div>
    </div>
  );
}

function SharedFileCard({
  item,
  idx,
  loadingDetailPath,
  exportingPath,
  deletingPath,
  vm
}) {
  const isFinal = vm.isFinalOutputFile(item);
  const isProtected = vm.isDeletionProtected(item);
  return (
    <article
      className={`data-card-vue shared-files-card ${isFinal ? 'is-final-output' : ''}`}
    >
      <div className="shared-files-card-main">
        <div className="shared-files-card-head">
          <div className="shared-files-card-title" title={item.path}>{vm.splitPath(item.path).base}</div>
          {isFinal && (
            <span
              className="jr-badge jr-badge-success"
              data-testid="shared-files-final-badge"
            >最终产物</span>
          )}
        </div>
        <div className="shared-files-card-meta">
          <span>{vm.fileRoleLabel(item)}</span>
          <span>{vm.formatTimestamp(vm.fileUpdatedAt(item))}</span>
          <span>{vm.formatBytes(vm.fileContent(item).length)}</span>
        </div>
        <div className="memory-sr-only">{item.path}</div>
        <div className="shared-files-card-summary">{vm.summaryText(item.content)}</div>
      </div>
      <div className="memory-card-actions shared-files-actions">
        <button
          className="btn btn-secondary btn-xs"
          data-testid={'shared-files-view-' + idx}
          disabled={loadingDetailPath === item.path}
          onClick={() => vm.openViewer(item)}
        >{loadingDetailPath === item.path ? '加载中...' : '打开'}</button>
        <button
          className="btn btn-secondary btn-xs"
          data-testid={'shared-files-export-' + idx}
          disabled={loadingDetailPath === item.path || !!exportingPath}
          onClick={() => vm.exportSharedFile(item)}
        >{exportingPath === item.path ? '导出中...' : '导出'}</button>
        <button
          className="btn btn-danger btn-xs"
          data-testid={'shared-files-delete-' + idx}
          disabled={deletingPath === item.path || isProtected}
          title={vm.deletionProtectionLabel(item)}
          onClick={() => vm.askDelete(item)}
        >{vm.deleteActionLabel(item)}</button>
        <button
          className="btn btn-ghost btn-xs"
          data-testid={'shared-files-fork-' + idx}
          title="以此文件为背景新建一个继承对话"
          onClick={() => vm.startInheritedChat(item)}
        >用此文件继续对话</button>
      </div>
    </article>
  );
}

function SharedFilesViewerModal({
  selectedFile,
  exportingPath,
  copiedInViewer,
  vm
}) {
  return (
    <div className="modal-overlay" data-testid="shared-files-viewer-overlay" onClick={(e) => { if (e.target === e.currentTarget) vm.closeViewer(); }}>
      <div className="modal-box memory-modal shared-files-viewer-modal" role="dialog" aria-modal="true" data-testid="shared-files-viewer">
        <div className="memory-modal-head">
          <div className="shared-files-viewer-title">
            <div className="modal-title">文件预览</div>
            <div className="memory-modal-tip" title={selectedFile.path}>{selectedFile.path}</div>
          </div>
          <div className="memory-modal-head-actions">
            <button
              className="btn btn-secondary btn-xs"
              data-testid="shared-files-viewer-export"
              disabled={!selectedFile.path || !!exportingPath}
              onClick={() => vm.exportSharedFile(selectedFile)}
            >{exportingPath === selectedFile.path ? '导出中...' : '导出'}</button>
            <button
              className="btn btn-secondary btn-xs"
              data-testid="shared-files-viewer-copy"
              disabled={!selectedFile.content}
              onClick={vm.copyViewerContent}
            >{copiedInViewer ? '已复制' : '复制内容'}</button>
            <button className="btn btn-ghost" data-testid="shared-files-viewer-close" onClick={vm.closeViewer}>关闭</button>
          </div>
        </div>
        <div className="shared-files-viewer-meta">
          <div className="shared-files-viewer-meta-item">
            <span className="shared-files-viewer-meta-label">来源</span>
            <span className="shared-files-viewer-meta-value">{selectedFile.updatedBy || '-'}</span>
          </div>
          <div className="shared-files-viewer-meta-item">
            <span className="shared-files-viewer-meta-label">更新时间</span>
            <span className="shared-files-viewer-meta-value">{vm.formatTimestamp(selectedFile.updatedAt)}</span>
          </div>
        </div>
        <pre className="memory-entry-preview shared-files-content-preview">{selectedFile.content || '文件为空'}</pre>
      </div>
    </div>
  );
}

function SharedFilesDeleteModal({
  confirmDeletePath,
  deletingPath,
  vm
}) {
  return (
    <div className="modal-overlay" data-testid="shared-files-delete-overlay" onClick={(e) => { if (e.target === e.currentTarget) vm.cancelDelete(); }}>
      <div className="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="shared-files-delete-modal">
        <div className="memory-modal-head">
          <div>
            <div className="modal-title">删除文件</div>
            <div className="memory-modal-tip">{confirmDeletePath}</div>
          </div>
          <button
            className="btn btn-ghost"
            data-testid="shared-files-delete-close"
            disabled={deletingPath === confirmDeletePath}
            onClick={vm.cancelDelete}
          >关闭</button>
        </div>
        <div className="memory-form-helper">
          文件删除后无法恢复。删除前请确认这份内容不再需要。
        </div>
        <div className="memory-editor-actions">
          <button
            className="btn btn-ghost"
            data-testid="shared-files-delete-cancel"
            disabled={deletingPath === confirmDeletePath}
            onClick={vm.cancelDelete}
          >取消</button>
          <button
            className="btn btn-danger"
            data-testid="shared-files-delete-confirm"
            disabled={deletingPath === confirmDeletePath}
            onClick={vm.confirmDelete}
          >{deletingPath === confirmDeletePath ? '删除中...' : '确认删除'}</button>
        </div>
      </div>
    </div>
  );
}

export function SharedFilesPage(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const notice = vm.notice || {};
  const viewing = val(vm.viewing);
  const loadingDetailPath = val(vm.loadingDetailPath);
  const exportingPath = val(vm.exportingPath);
  const deletingPath = val(vm.deletingPath);
  const confirmDeletePath = val(vm.confirmDeletePath);
  const selectedFile = vm.selectedFile || {};
  const searchText = val(vm.searchText);
  const sortMode = val(vm.sortMode);
  const refreshing = val(vm.refreshing);
  const copiedInViewer = val(vm.copiedInViewer);

  const fileCategory = val(vm.fileCategory);
  const finalOutputCount = val(vm.finalOutputCount);
  const workFileCount = val(vm.workFileCount);
  const categoryEmptyTitle = val(vm.categoryEmptyTitle);
  const categoryEmptyText = val(vm.categoryEmptyText);
  const visibleItems = val(vm.visibleItems) || [];
  const items = val(vm.items) || [];

  return (
    <section id="page-shared-files" className="page active shared-files-page" data-testid="shared-files-page">
      <SharedFilesHeader
        searchText={searchText}
        sortMode={sortMode}
        fileCategory={fileCategory}
        itemsLength={items.length}
        finalOutputCount={finalOutputCount}
        workFileCount={workFileCount}
        refreshing={refreshing}
        vm={vm}
      />

      <div className="panel-body memory-center-body memory-center-body-has-toolbar" data-testid="shared-files-body">
        {notice.message && (
          <div className={`settings-prompt-notice memory-notice-fade is-${notice.level}`} data-testid="shared-files-notice">
            {notice.message}
          </div>
        )}

        <div className="memory-center-callout" data-testid="shared-files-callout">
          <div className="memory-center-callout-icon">📂</div>
          <div className="memory-center-callout-content">
            <h3>共享文件 · Agent 协作中转站</h3>
            <p>Agent 在运行过程中产生的所有数据产物（如测试报告、草稿、临时配置文件等）都保存在这里。你可将重要的共享文件“提升为长期记忆”以指导后续生成，或直接修改和删除它们。</p>
          </div>
          <div className="memory-center-callout-actions">
            <button className="btn btn-secondary btn-toolbar-sm" data-testid="shared-files-open-center" onClick={props.onOpenMemoryCenter}>打开记忆中心</button>
          </div>
        </div>

        {items.length === 0 ? (
          <div className="memory-empty" data-testid="shared-files-empty">
            <svg className="memory-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" strokeWidth="1.4" aria-hidden="true">
              <rect x="10" y="10" width="28" height="32" rx="3" opacity="0.4" />
              <path d="M16 18h16M16 24h16M16 30h10" strokeLinecap="round" opacity="0.6" />
              <circle cx="36" cy="36" r="7" fill="var(--surface)" stroke="currentColor" />
              <path d="M33 36h6M36 33v6" strokeLinecap="round" />
            </svg>
            <div className="memory-empty-title">还没有文件产物</div>
            <div className="memory-empty-text">
              Agent 生成报告、草稿或数据文件后，会显示在这里。
            </div>
          </div>
        ) : visibleItems.length === 0 ? (
          <div className="shared-files-text-empty" data-testid="shared-files-category-empty">
            <div className="shared-files-text-empty-title">{categoryEmptyTitle}</div>
            {(fileCategory !== 'final' || searchText) && (
              <div className="shared-files-text-empty-body">{categoryEmptyText}</div>
            )}
            <div className="memory-empty-actions">
              {searchText && <button className="btn btn-secondary btn-toolbar-sm" onClick={vm.clearSearch}>清空搜索</button>}
            </div>
          </div>
        ) : (
          <div className="shared-files-list" data-testid="shared-files-list">
            {visibleItems.map((item, idx) => (
              <SharedFileCard
                key={item.path || idx}
                item={item}
                idx={idx}
                loadingDetailPath={loadingDetailPath}
                exportingPath={exportingPath}
                deletingPath={deletingPath}
                vm={vm}
              />
            ))}
          </div>
        )}
      </div>

      {viewing && (
        <SharedFilesViewerModal
          selectedFile={selectedFile}
          exportingPath={exportingPath}
          copiedInViewer={copiedInViewer}
          vm={vm}
        />
      )}

      {confirmDeletePath && (
        <SharedFilesDeleteModal
          confirmDeletePath={confirmDeletePath}
          deletingPath={deletingPath}
          vm={vm}
        />
      )}
    </section>
  );
}
