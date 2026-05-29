import React from 'react';
import { useVueSetup, val } from '../../utils/vue-compat.js';
import { DagNodeEditForm as VueComp } from './DagNodeEditForm.js';

export function DagNodeEditForm(props) {
  const emit = (event, ...args) => {
    const propName = 'on' + event.split('-').map(s => s[0].toUpperCase() + s.slice(1)).join('');
    props[propName]?.(...args);
  };
  const vm = useVueSetup(VueComp.setup, props, emit);

  const agentNodes = val(vm.agentNodes) || [];
  const editingDisabled = val(vm.editingDisabled);
  const saveErrorText = val(vm.saveErrorText);
  const modelOptions = val(vm.modelOptions) || [];
  const form = vm.form; // Reactive object

  if (!form) return null;

  return (
    <section className="dag-detail-section dag-node-edit-form" data-testid="dag-node-edit-form">
      <div className="dag-section-title">步骤设置</div>
      {agentNodes.length === 0 ? (
        <div className="dag-console-muted" data-testid="dag-node-edit-empty">暂无可配置步骤</div>
      ) : (
        <form className="dag-node-edit-grid" onSubmit={(e) => { e.preventDefault(); vm.submit(); }}>
          {editingDisabled && (
            <div className="dag-console-muted dag-node-edit-wide" data-testid="dag-node-edit-disabled-reason">
              {props.disabledReason}
            </div>
          )}
          <label>
            <span>步骤</span>
            <select
              value={form.nodeKey}
              data-testid="dag-node-edit-select"
              disabled={editingDisabled}
              onChange={vm.chooseNode}
            >
              {agentNodes.map((node, index) => {
                const nodeKeyVal = node.node_key || node.nodeKey || node.key || node.id || `node-${index}`;
                return (
                  <option key={nodeKeyVal} value={nodeKeyVal}>
                    {vm.displayNodeLabel(node, index)}
                  </option>
                );
              })}
            </select>
          </label>
          <label>
            <span>名称</span>
            <input
              value={form.title}
              data-testid="dag-node-edit-title"
              disabled={editingDisabled}
              onChange={(e) => { form.title = e.target.value; }}
            />
          </label>
          <label>
            <span>执行引擎</span>
            <select
              value={form.provider}
              data-testid="dag-node-edit-provider"
              disabled={editingDisabled}
              onChange={(e) => { form.provider = e.target.value; }}
            >
              <option value="">默认</option>
              <option value="claude">claude</option>
              <option value="codex">codex</option>
            </select>
          </label>
          <label>
            <span>模型</span>
            <select
              value={form.model}
              data-testid="dag-node-edit-model"
              disabled={editingDisabled}
              onChange={(e) => { form.model = e.target.value; }}
            >
              <option value="">默认</option>
              {modelOptions.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>提示词</span>
            <input
              value={form.promptKey}
              data-testid="dag-node-edit-prompt-key"
              disabled={editingDisabled}
              onChange={(e) => { form.promptKey = e.target.value; }}
            />
          </label>
          <label>
            <span>依赖步骤</span>
            <input
              value={form.dependsOn}
              data-testid="dag-node-edit-depends-on"
              disabled={editingDisabled}
              onChange={(e) => { form.dependsOn = e.target.value; }}
            />
          </label>
          <label className="dag-node-edit-wide">
            <span>指令</span>
            <textarea
              value={form.firstTurn}
              rows={4}
              data-testid="dag-node-edit-first-turn"
              disabled={editingDisabled}
              onChange={(e) => { form.firstTurn = e.target.value; }}
            />
          </label>
          <label>
            <span>输入步骤</span>
            <input
              value={form.fromNodes}
              data-testid="dag-node-edit-input-nodes"
              disabled={editingDisabled}
              onChange={(e) => { form.fromNodes = e.target.value; }}
            />
          </label>
          <label>
            <span>输入文件</span>
            <input
              value={form.fromSharedfiles}
              data-testid="dag-node-edit-input-sharedfiles"
              disabled={editingDisabled}
              onChange={(e) => { form.fromSharedfiles = e.target.value; }}
            />
          </label>
          <label>
            <span>输出文件</span>
            <input
              value={form.toSharedfilePath}
              data-testid="dag-node-edit-output-file"
              disabled={editingDisabled}
              onChange={(e) => { form.toSharedfilePath = e.target.value; }}
            />
          </label>
          <label>
            <span>写入方式</span>
            <select
              value={form.lockMode}
              data-testid="dag-node-edit-lock-mode"
              disabled={editingDisabled}
              onChange={(e) => { form.lockMode = e.target.value; }}
            >
              <option value="exclusive">独占写入</option>
              <option value="append">追加写入</option>
              <option value="shared">共享读取</option>
            </select>
          </label>
          <label className="dag-node-edit-check">
            <input
              type="checkbox"
              checked={form.toNodeResult}
              data-testid="dag-node-edit-to-result"
              disabled={editingDisabled}
              onChange={(e) => { form.toNodeResult = e.target.checked; }}
            />
            <span>作为步骤结果</span>
          </label>
          {saveErrorText && (
            <div className="dag-console-error-inline dag-node-edit-wide" data-testid="dag-node-edit-error">
              {saveErrorText}
            </div>
          )}
          <div className="dag-node-edit-actions">
            <button
              type="submit"
              className="btn btn-primary"
              data-testid="dag-node-edit-save"
              disabled={editingDisabled || props.savingNodeKey === form.nodeKey}
            >
              {props.savingNodeKey === form.nodeKey ? '保存中' : '保存步骤'}
            </button>
          </div>
        </form>
      )}
    </section>
  );
}
