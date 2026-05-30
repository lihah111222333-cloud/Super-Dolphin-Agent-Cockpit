import React from 'react';
import { useVueSetup, val } from '../utils/vue-compat.js';
import { DagsPage as VueComp } from './DagsPage.js';
import { DagFinalOutputPanel } from '../components/dag/DagFinalOutputPanel.jsx';
import { DagNodeEditForm } from '../components/dag/DagNodeEditForm.jsx';
import { DagNodeList } from '../components/dag/DagNodeList.jsx';
import { DagRunHistoryPanel } from '../components/dag/DagRunHistoryPanel.jsx';
import { DagScheduleModal } from '../components/dag/DagScheduleModal.jsx';
import { DagSharedFilesPanel } from '../components/dag/DagSharedFilesPanel.jsx';
import { DagTopologyPanel } from '../components/dag/DagTopologyPanel.jsx';

function DagsListPane({
  loading, pageErrorText, rows, emptyText, categoryTabs, activeCategory, visibleRows, selectedRow, onSetCategory, onSelectDag
}) {
  return (
    <aside className="dag-console-list-pane" data-testid="dag-console-list">
      {loading ? (
        <div className="empty-state dag-console-empty" data-testid="dag-console-loading">
          <div className="es-icon">D</div>
          <h3>正在加载任务流程</h3>
        </div>
      ) : pageErrorText ? (
        <div className="empty-state dag-console-empty dag-console-error" data-testid="dag-console-error">
          <div className="es-icon">D</div>
          <h3>加载任务流程失败</h3>
          <p>{pageErrorText}</p>
        </div>
      ) : rows.length === 0 ? (
        <div className="empty-state dag-console-empty" data-testid="dag-console-empty">
          <div className="es-icon">D</div>
          <h3>{emptyText || '暂无任务流程'}</h3>
        </div>
      ) : (
        <div className="dag-console-list-wrap">
          <div className="dag-category-tabs" data-testid="dag-category-tabs" role="tablist" aria-label="任务流程分类">
            {categoryTabs.map((tab) => (
              <button
                key={tab.key}
                type="button"
                role="tab"
                className={`dag-category-tab ${activeCategory === tab.key ? 'active' : ''}`}
                aria-selected={activeCategory === tab.key ? 'true' : 'false'}
                data-testid={`dag-category-tab-${tab.key}`}
                onClick={() => onSetCategory(tab.key)}
              >
                <span>{tab.label}</span>
                <span className="dag-category-count">{tab.count}</span>
              </button>
            ))}
          </div>
          {visibleRows.length === 0 ? (
            <div className="dag-console-muted dag-console-category-empty" data-testid="dag-category-empty">
              当前分类暂无任务流程
            </div>
          ) : (
            <div className="dag-console-list">
              {visibleRows.map((row, idx) => (
                <button
                  key={row.listKey}
                  type="button"
                  className={`dag-console-row ${selectedRow && selectedRow.listKey === row.listKey ? 'active' : ''}`}
                  data-testid={`dag-console-row-${idx}`}
                  onClick={() => onSelectDag(row)}
                >
                  <span className="dag-console-row-main">
                    <span className="dag-console-title">{row.title}</span>
                    <span className="dag-console-key">{row.key === '-' ? '' : row.key}</span>
                  </span>
                  <span className="dag-console-row-meta">
                    <span className="dag-console-status">{row.status}</span>
                    <span className="dag-console-trigger">{row.triggerLabel}</span>
                    <span className="dag-console-run">{row.latestRunLabel}</span>
                    {row.hasFinalOutput && (
                      <span className="dag-console-final" data-testid="dag-console-final-marker">
                        最终结果
                      </span>
                    )}
                  </span>
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </aside>
  );
}

function DagsDetailPane({
  loading, pageErrorText, selectedRow, detailState, deleteDisabledReason, scheduleToggleVisible,
  scheduleToggleDisabledReason, scheduleToggleLabel, scheduleActionVisible, scheduleDisabledReason,
  scheduleActionLabel, stopActionVisible, stopDisabledReason, startDisabledReason, startErrorText,
  startWarningText, terminateErrorText, terminateWarningText, deleteErrorText, deleteSuccessText,
  scheduleErrorText, runsErrorText, selectedFinalOutput, detailErrorText, designNodes, editDisabledReason,
  emptyText, onDeleteSelectedDag, onToggleScheduleEnabled, onOpenScheduleModal, onStopSelectedDag,
  onStartSelectedDag, onSelectRun, onOpenChat, onSaveAgentNode
}) {
  return (
    <section className="dag-console-detail-pane" data-testid="dag-console-detail">
      {loading ? (
        <div className="empty-state dag-console-empty" data-testid="dag-console-detail-loading">
          <div className="es-icon">D</div>
          <h3>正在加载任务流程</h3>
        </div>
      ) : pageErrorText ? (
        <div className="empty-state dag-console-empty dag-console-error" data-testid="dag-console-detail-error">
          <div className="es-icon">D</div>
          <h3>加载任务流程失败</h3>
          <p>{pageErrorText}</p>
        </div>
      ) : selectedRow ? (
        <div className="dag-console-detail-grid">
          <div className="dag-console-detail-heading">
            <div>
              <h3>{selectedRow.title}</h3>
              <span>{selectedRow.latestRunLabel === '-' ? '暂无运行' : '最近运行：' + selectedRow.latestRunLabel}</span>
            </div>
            <div className="dag-console-actions">
              <button
                type="button"
                className="btn dag-delete-button"
                data-testid="dag-delete-button"
                disabled={Boolean(deleteDisabledReason)}
                title={deleteDisabledReason || undefined}
                onClick={onDeleteSelectedDag}
              >
                {detailState.deleting ? '删除中' : '删除'}
              </button>
              {scheduleToggleVisible && (
                <button
                  type="button"
                  className="btn"
                  data-testid="dag-schedule-toggle-button"
                  disabled={Boolean(scheduleToggleDisabledReason)}
                  title={scheduleToggleDisabledReason || undefined}
                  onClick={onToggleScheduleEnabled}
                >
                  {detailState.scheduling ? '保存中' : scheduleToggleLabel}
                </button>
              )}
              {scheduleActionVisible && (
                <button
                  type="button"
                  className="btn"
                  data-testid="dag-schedule-button"
                  disabled={Boolean(scheduleDisabledReason)}
                  title={scheduleDisabledReason || undefined}
                  onClick={onOpenScheduleModal}
                >
                  {detailState.scheduling ? '保存中' : scheduleActionLabel}
                </button>
              )}
              {stopActionVisible && (
                <button
                  type="button"
                  className="btn dag-stop-button"
                  data-testid="dag-stop-button"
                  disabled={Boolean(stopDisabledReason)}
                  title={stopDisabledReason || undefined}
                  onClick={onStopSelectedDag}
                >
                  {detailState.terminating ? '停止中' : '停止'}
                </button>
              )}
              <button
                type="button"
                className="btn btn-primary"
                data-testid="dag-start-button"
                disabled={Boolean(startDisabledReason)}
                title={startDisabledReason || undefined}
                onClick={onStartSelectedDag}
              >
                {detailState.starting ? '启动中' : '运行'}
              </button>
            </div>
          </div>
          {startDisabledReason && (
            <div className="dag-console-muted" data-testid="dag-start-disabled-reason">
              {startDisabledReason}
            </div>
          )}
          {deleteDisabledReason && (
            <div className="dag-console-muted" data-testid="dag-delete-disabled-reason">
              {deleteDisabledReason}
            </div>
          )}
          {startErrorText && (
            <div className="dag-console-error-inline" data-testid="dag-start-error">
              {startErrorText}
            </div>
          )}
          {startWarningText && (
            <div className="dag-console-warning-inline" data-testid="dag-start-warning">
              {startWarningText}
            </div>
          )}
          {terminateErrorText && (
            <div className="dag-console-error-inline" data-testid="dag-terminate-error">
              {terminateErrorText}
            </div>
          )}
          {terminateWarningText && (
            <div className="dag-console-warning-inline" data-testid="dag-terminate-warning">
              {terminateWarningText}
            </div>
          )}
          {deleteErrorText && (
            <div className="dag-console-error-inline" data-testid="dag-delete-error">
              {deleteErrorText}
            </div>
          )}
          {deleteSuccessText && (
            <div className="dag-console-success-inline" data-testid="dag-delete-success">
              {deleteSuccessText}
            </div>
          )}
          {scheduleErrorText && (
            <div className="dag-console-error-inline" data-testid="dag-schedule-error">
              {scheduleErrorText}
            </div>
          )}
          {runsErrorText ? (
            <DagFinalOutputPanel
              data-testid="dag-runs-error"
              finalOutput={null}
              runsError={detailState.runsError}
            />
          ) : (
            <DagFinalOutputPanel
              finalOutput={selectedFinalOutput}
              runsError={null}
            />
          )}
          <dl className="dag-console-facts">
            <div>
              <dt>任务状态</dt>
              <dd>{selectedRow.status}</dd>
            </div>
            <div>
              <dt>运行计划</dt>
              <dd>{selectedRow.triggerLabel}</dd>
            </div>
            <div>
              <dt>最近运行</dt>
              <dd>{selectedRow.latestRunLabel}</dd>
            </div>
            <div>
              <dt>最终结果</dt>
              <dd>{selectedRow.hasFinalOutput ? '已记录' : '-'}</dd>
            </div>
          </dl>
          {detailState.loading && (
            <div className="dag-console-muted" data-testid="dag-detail-loading-inline">
              正在加载详情
            </div>
          )}
          {detailErrorText && (
            <div className="dag-console-error-inline" data-testid="dag-detail-load-error">
              {detailErrorText}
            </div>
          )}
          <DagRunHistoryPanel
            runs={detailState.runs}
            selectedRunKey={detailState.selectedRunKey}
            onSelectRun={onSelectRun}
          />
          <details className="dag-steps-section">
            <summary>执行步骤</summary>
            <DagNodeList nodes={detailState.nodes} onOpenChat={onOpenChat} />
          </details>
          <details className="dag-advanced-section">
            <summary>高级设置</summary>
            <DagTopologyPanel nodes={designNodes} />
            <DagNodeEditForm
              nodes={designNodes}
              savingNodeKey={detailState.savingNodeKey}
              saveError={detailState.saveError}
              disabledReason={editDisabledReason}
              onSaveAgentNode={onSaveAgentNode}
            />
            <DagSharedFilesPanel nodes={designNodes} />
          </details>
        </div>
      ) : (
        <div className="empty-state dag-console-empty" data-testid="dag-console-detail-empty">
          <div className="es-icon">D</div>
          <h3>{emptyText || '暂无任务流程'}</h3>
        </div>
      )}
    </section>
  );
}

export function DagsPage(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  React.useEffect(() => {
    const currentRows = val(vm.rows) || [];
    if (!currentRows.length) {
      if (vm.activeCategory && vm.activeCategory.value !== '') {
        vm.activeCategory.value = '';
      }
      if (vm.categoryManuallySelected) {
        vm.categoryManuallySelected.value = false;
      }
      return;
    }
    const currentActive = val(vm.activeCategory);
    const tabs = val(vm.categoryTabs) || [];
    const activeTab = tabs.find((t) => t.key === currentActive);
    const manuallySelected = val(vm.categoryManuallySelected);
    if (!currentActive || !activeTab || (!manuallySelected && activeTab.count === 0)) {
      const nextTab = tabs.find((t) => t.count > 0);
      if (nextTab) {
        if (vm.activeCategory) {
          vm.activeCategory.value = nextTab.key;
        }
        if (vm.categoryManuallySelected) {
          vm.categoryManuallySelected.value = false;
        }
      }
    }
  }, [props.items, vm]);



  const activeCategory = val(vm.activeCategory);
  const categoryTabs = val(vm.categoryTabs) || [];
  const rows = val(vm.rows) || [];
  const visibleRows = val(vm.visibleRows) || [];
  const detailState = vm.detailState || {};
  const deleteConfirmTarget = val(vm.deleteConfirmTarget);
  const deleteDisabledReason = val(vm.deleteDisabledReason);
  const deleteErrorText = val(vm.deleteErrorText);
  const deleteSuccessText = val(vm.deleteSuccessText);
  const detailErrorText = val(vm.detailErrorText);
  const designNodes = val(vm.designNodes) || [];
  const editDisabledReason = val(vm.editDisabledReason);
  const pageErrorText = val(vm.pageErrorText);
  const runsErrorText = val(vm.runsErrorText);
  const selectedRow = val(vm.selectedRow);
  const selectedFinalOutput = val(vm.selectedFinalOutput);
  const stopActionVisible = val(vm.stopActionVisible);
  const stopDisabledReason = val(vm.stopDisabledReason);
  const startDisabledReason = val(vm.startDisabledReason);
  const startErrorText = val(vm.startErrorText);
  const startWarningText = val(vm.startWarningText);
  const terminateErrorText = val(vm.terminateErrorText);
  const terminateWarningText = val(vm.terminateWarningText);

  // Schedule action variables mapped from setup return
  const scheduleConfirmOpen = val(vm.scheduleConfirmOpen);
  const scheduleActionLabel = val(vm.scheduleActionLabel);
  const schedulePreset = val(vm.schedulePreset);
  const scheduleTime = val(vm.scheduleTime);
  const scheduleWeekday = val(vm.scheduleWeekday);
  const scheduleMonthDay = val(vm.scheduleMonthDay);
  const schedulePreviewText = val(vm.schedulePreviewText);
  const scheduleInputError = val(vm.scheduleInputError);
  const scheduleErrorText = val(vm.scheduleErrorText);
  const scheduleToggleVisible = val(vm.scheduleToggleVisible);
  const scheduleToggleDisabledReason = val(vm.scheduleToggleDisabledReason);
  const scheduleToggleLabel = val(vm.scheduleToggleLabel);
  const scheduleActionVisible = val(vm.scheduleActionVisible);
  const scheduleDisabledReason = val(vm.scheduleDisabledReason);

  return (
    <section id="page-dags" className="page active dag-console-page" data-testid="dag-console">
      <div className="panel-header" data-testid="dag-console-header">
        <div className="ph-bar"></div>
        <div className="ph-text"><h2>任务流程</h2></div>
        <button
          type="button"
          className="btn"
          data-testid="dag-design-flow-button"
          onClick={vm.startDesignFlow}
        >
          AI 设计流程
        </button>
      </div>
      <div className="dag-console-shell">
        <DagsListPane
          loading={props.loading}
          pageErrorText={pageErrorText}
          rows={rows}
          emptyText={props.emptyText}
          categoryTabs={categoryTabs}
          activeCategory={activeCategory}
          visibleRows={visibleRows}
          selectedRow={selectedRow}
          onSetCategory={vm.setCategory}
          onSelectDag={vm.selectDag}
        />
        <DagsDetailPane
          loading={props.loading}
          pageErrorText={pageErrorText}
          selectedRow={selectedRow}
          detailState={detailState}
          deleteDisabledReason={deleteDisabledReason}
          scheduleToggleVisible={scheduleToggleVisible}
          scheduleToggleDisabledReason={scheduleToggleDisabledReason}
          scheduleToggleLabel={scheduleToggleLabel}
          scheduleActionVisible={scheduleActionVisible}
          scheduleDisabledReason={scheduleDisabledReason}
          scheduleActionLabel={scheduleActionLabel}
          stopActionVisible={stopActionVisible}
          stopDisabledReason={stopDisabledReason}
          startDisabledReason={startDisabledReason}
          startErrorText={startErrorText}
          startWarningText={startWarningText}
          terminateErrorText={terminateErrorText}
          terminateWarningText={terminateWarningText}
          deleteErrorText={deleteErrorText}
          deleteSuccessText={deleteSuccessText}
          scheduleErrorText={scheduleErrorText}
          runsErrorText={runsErrorText}
          selectedFinalOutput={selectedFinalOutput}
          detailErrorText={detailErrorText}
          designNodes={designNodes}
          editDisabledReason={editDisabledReason}
          emptyText={props.emptyText}
          onDeleteSelectedDag={vm.deleteSelectedDag}
          onToggleScheduleEnabled={vm.toggleScheduleEnabled}
          onOpenScheduleModal={vm.openScheduleModal}
          onStopSelectedDag={vm.stopSelectedDag}
          onStartSelectedDag={vm.startSelectedDag}
          onSelectRun={vm.selectRun}
          onOpenChat={vm.openChat}
          onSaveAgentNode={vm.saveAgentNode}
        />
      </div>
      <DagScheduleModal
        open={scheduleConfirmOpen}
        title={selectedRow ? selectedRow.title : ''}
        actionLabel={scheduleActionLabel}
        preset={schedulePreset}
        time={scheduleTime}
        weekday={scheduleWeekday}
        monthDay={scheduleMonthDay}
        previewText={schedulePreviewText}
        inputError={scheduleInputError}
        scheduleErrorText={scheduleErrorText}
        saving={detailState.scheduling}
        onUpdatePreset={vm.updateSchedulePreset}
        onUpdateTime={vm.updateScheduleTime}
        onUpdateWeekday={vm.updateScheduleWeekday}
        onUpdateMonthDay={vm.updateScheduleMonthDay}
        onCancel={vm.cancelScheduleDAG}
        onConfirm={vm.confirmScheduleDAG}
      />
      {deleteConfirmTarget && (
        <div className="modal-overlay dag-delete-overlay" data-testid="dag-delete-overlay" onClick={(e) => { if (e.target === e.currentTarget) vm.cancelDeleteDAG(); }}>
          <div className="modal-box dag-delete-modal" role="dialog" aria-modal="true" data-testid="dag-delete-modal">
            <div className="dag-delete-modal-head">
              <div>
                <div className="dag-delete-modal-title">删除任务流程</div>
                <div className="dag-delete-modal-tip">{deleteConfirmTarget.title}</div>
              </div>
              <button
                type="button"
                className="btn btn-ghost"
                data-testid="dag-delete-close"
                disabled={detailState.deleting}
                onClick={vm.cancelDeleteDAG}
              >
                关闭
              </button>
            </div>
            <div className="dag-delete-form-helper">确认删除任务流程 “{deleteConfirmTarget.title}”？该操作不可撤销。</div>
            {deleteErrorText && (
              <div className="dag-console-error-inline" style={{ color: 'var(--color-danger,#f87171)' }}>
                {deleteErrorText}
              </div>
            )}
            <div className="dag-delete-modal-actions">
              <button
                type="button"
                className="btn btn-ghost"
                data-testid="dag-delete-cancel"
                disabled={detailState.deleting}
                onClick={vm.cancelDeleteDAG}
              >
                取消
              </button>
              <button
                type="button"
                className="btn btn-danger"
                data-testid="dag-delete-confirm"
                disabled={detailState.deleting}
                onClick={vm.confirmDeleteDAG}
              >
                {detailState.deleting ? '删除中...' : '确认删除'}
              </button>
            </div>
          </div>
        </div>
      )}
    </section>
  );
}
