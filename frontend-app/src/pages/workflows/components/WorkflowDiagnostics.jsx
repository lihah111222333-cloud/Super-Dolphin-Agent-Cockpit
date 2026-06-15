import React, { useState } from 'react';
import { textValue } from '../../shared/pageShared.js';
import { Panel } from '../../shared/pageComponents.jsx';
import { workflowSharedFileRows, workflowTopologyRows } from '../adapters/workflowDisplayAdapter.js';

function WorkflowDiagnostics({ model }) {
  /*
   * 诊断面板只显示 WorkflowPage 算好的 diagnostics。
   * ready-no-wakeup、blocked、failed 不在子组件里再猜一遍。
   */
  const { derived } = model;
  return (
    <div className="workflow-diagnostics">
      <WorkflowDiagnosticPanel model={model} />
      <WorkflowTopologyPanel nodes={derived.diagnosticNodes} />
      <WorkflowSharedFilesPanel nodes={derived.diagnosticNodes} />
    </div>
  );
}

function WorkflowDiagnosticPanel({ model }) {
  const { derived } = model;
  return (
    <Panel title="运行诊断">
      {derived.diagnostics.length === 0 ? <p>暂无 blocked/ready-no-wakeup/failed 诊断</p> : (
        <div className="workflow-diagnostic-list">
          {derived.diagnostics.map((diagnostic) => <WorkflowDiagnosticRow diagnostic={diagnostic} key={diagnostic.key} model={model} />)}
        </div>
      )}
    </Panel>
  );
}

function WorkflowDiagnosticRow({ diagnostic, model }) {
  const { actionState, actions, derived } = model;
  const [assignee, setAssignee] = useState(textValue(diagnostic.node?.assignedTo));
  const canDispatch = diagnostic.recovery === 'dispatch' && Boolean(derived.runId);
  const dispatching = actionState.dispatchingNodeKey === diagnostic.node?.nodeKey;
  return (
    <article className={`workflow-diagnostic-row ${diagnostic.severity || ''}`}>
      <strong>{diagnostic.title}</strong>
      <span>{diagnostic.message}</span>
      {diagnostic.recovery === 'dispatch' ? (
        <div className="workflow-diagnostic-actions">
          <label>
            恢复执行者
            <input value={assignee} onChange={(event) => setAssignee(event.target.value)} aria-label="恢复执行者" />
          </label>
          <button type="button" onClick={() => { void actions.dispatchNode(diagnostic.node, assignee); }} disabled={!canDispatch || dispatching} title={canDispatch ? '' : '当前运行缺少 runId'}>
            {dispatching ? '派发中...' : '指派并派发'}
          </button>
        </div>
      ) : null}
    </article>
  );
}

function WorkflowTopologyPanel({ nodes }) {
  const rows = workflowTopologyRows(nodes);
  return (
    <Panel title="流程图">
      {rows.length === 0 ? <p>暂无流程图</p> : <pre className="workflow-topology-source">{rows.join('\n')}</pre>}
    </Panel>
  );
}

function WorkflowSharedFilesPanel({ nodes }) {
  const rows = workflowSharedFileRows(nodes);
  return (
    <Panel title="工作文件">
      {rows.length === 0 ? <p>暂无工作文件读写</p> : (
        <div className="workflow-shared-files">
          {rows.map((row) => (
            <div className="workflow-shared-file-row" key={`${row.nodeKey}:${row.access}:${row.path}`}>
              <span>{row.stepLabel}</span>
              <strong>{row.path}</strong>
              <small>{row.access}</small>
            </div>
          ))}
        </div>
      )}
    </Panel>
  );
}

export { WorkflowDiagnostics };
