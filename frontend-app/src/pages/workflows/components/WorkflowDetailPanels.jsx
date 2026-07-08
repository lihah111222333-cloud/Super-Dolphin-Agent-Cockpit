import React, { useMemo, useState } from 'react';
import { textValue } from '../../shared/pageShared.js';
import { formatDagRunStartedAt, dagRunStartedAtSortText, dagStatusLabel } from '../services/workflowDagModel.js';
import { Panel } from '../../shared/pageComponents.jsx';
import { ConfirmDagDeleteModal, DagNodeEditor, DagScheduleModal } from './WorkflowNodeEditorPanels.jsx';

const DAG_RUN_HISTORY_VISIBLE_LIMIT = 10;

function chronologicalWorkflowRuns(runs) {
  return [...runs].sort((left, right) => dagRunStartedAtSortText(left).localeCompare(dagRunStartedAtSortText(right)));
}

function WorkflowRunHistory({ model }) {
  const { detail, run, selection } = model;
  const [expandedState, setExpandedState] = useState({ expanded: false, selectedDagKey: selection.selectedDagKey });
  if (expandedState.selectedDagKey !== selection.selectedDagKey) {
    setExpandedState({ expanded: false, selectedDagKey: selection.selectedDagKey });
  }
  const expanded = expandedState.selectedDagKey === selection.selectedDagKey ? expandedState.expanded : false;
  const orderedRuns = useMemo(() => chronologicalWorkflowRuns(detail.runs), [detail.runs]);
  const hiddenCount = Math.max(orderedRuns.length - DAG_RUN_HISTORY_VISIBLE_LIMIT, 0);
  const visibleRuns = expanded || hiddenCount === 0 ? orderedRuns : orderedRuns.slice(hiddenCount);
  return (
    <Panel title="运行历史">
      <div className="dag-run-list">
        {detail.runs.length === 0 ? <p>暂无运行记录</p> : null}
        {hiddenCount > 0 ? (
          <button
            type="button"
            className="dag-run-list-toggle"
            aria-expanded={expanded}
            onClick={() => setExpandedState((current) => ({
              expanded: !(current.selectedDagKey === selection.selectedDagKey ? current.expanded : false),
              selectedDagKey: selection.selectedDagKey,
            }))}
          >
            {expanded ? '收起较早运行记录' : `展开较早 ${hiddenCount} 次运行`}
          </button>
        ) : null}
        {visibleRuns.map((item, index) => (
          <WorkflowRunRow
            index={expanded || hiddenCount === 0 ? index : hiddenCount + index}
            key={item.id}
            run={item}
            runState={run}
          />
        ))}
      </div>
    </Panel>
  );
}

function WorkflowRunRow({ index, run, runState }) {
  const active = run.runKey === runState.selectedRunKey;
  const startedAt = textValue(run.startedAt);
  return (
    <button type="button" className={'run-row ' + (active ? 'active' : '')} onClick={() => { void runState.loadRunDetail(run.runKey); }}>
      <span>{'第 ' + (index + 1) + ' 次运行'}</span>
      <em>{dagStatusLabel(run.status)}</em>
      <time dateTime={startedAt || undefined} title={startedAt || undefined}>{formatDagRunStartedAt(startedAt)}</time>
    </button>
  );
}

function WorkflowNodeList({ model }) {
  const { derived, store } = model;
  return (
    <Panel title="执行步骤">
      <div className="dag-node-list">
        {derived.diagnosticNodes.length === 0 ? <p>暂无步骤</p> : null}
        {derived.diagnosticNodes.map((node) => <WorkflowNodeRow key={node.id} node={node} store={store} />)}
      </div>
    </Panel>
  );
}

function WorkflowNodeRow({ node, store }) {
  return (
    <article className="dag-node-row">
      <strong>{node.title}</strong>
      <em>{dagStatusLabel(node.status)}</em>
      {node.threadId ? <button type="button" onClick={() => openWorkflowNodeThread(store, node)}>查看对话</button> : null}
    </article>
  );
}

function openWorkflowNodeThread(store, nodeOrThreadId) {
  if (typeof store?.openThreadById !== 'function') return;
  const node = nodeOrThreadId && typeof nodeOrThreadId === 'object' ? nodeOrThreadId : null;
  const threadId = node ? node.threadId : nodeOrThreadId;
  const dagNode = node ? { ...node, result: node.raw?.result ?? node.result } : null;
  void store.openThreadById(threadId, { source: 'dag-node', ...(dagNode ? { dagNode } : {}) }).then((opened) => {
    if (opened) store?.setActivePage?.('chat');
  });
}

function WorkflowAdvanced({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <details className="workflow-advanced">
      <summary>高级设置</summary>
      {derived.configurableNodes.length > 0 ? <DagNodeEditor nodes={derived.configurableNodes} savingNodeKey={actionState.savingNodeKey} onSave={actions.saveAgentNode} /> : <p className="console-message">暂无可配置步骤</p>}
    </details>
  );
}

function WorkflowModals({ model }) {
  const { actionState, actions, derived } = model;
  return (
    <>
      {actionState.scheduleOpen ? <DagScheduleModal cron={actionState.scheduleCron} actionLabel={derived.scheduleActionLabel} saving={actionState.actioning === 'schedule'} onClose={() => actionState.setScheduleOpen(false)} onSave={actions.saveSchedule} /> : null}
      {actionState.deleteTarget ? <ConfirmDagDeleteModal dag={actionState.deleteTarget} deleting={actionState.actioning === 'delete'} onClose={() => actionState.setDeleteTarget(null)} onConfirm={actions.confirmDeleteDAG} /> : null}
    </>
  );
}

export { WorkflowAdvanced, WorkflowModals, WorkflowNodeList, WorkflowRunHistory };
