import React, { useMemo, useState } from 'react';
import { firstText, objectValue, textValue, wordListFromText } from '../../shared/pageShared.js';
import { Panel } from '../../shared/pageComponents.jsx';
import { workflowOrderedNodes } from '../adapters/workflowDisplayAdapter.js';
import { dagStatusLabel } from '../services/workflowDagModel.js';
import { firstPresent, parseJsonObject } from '../services/workflowEnterpriseTemplateModel.js';

function parsedDagConfig(value, label = 'workflow dag config') {
  if (value && typeof value === 'object' && !Array.isArray(value)) return value;
  return parseJsonObject(value, label);
}

function WorkflowStageProgress({ model }) {
  const groups = useMemo(() => workflowStageGroups(model.derived.diagnosticNodes), [model.derived.diagnosticNodes]);
  const [activeNodeKey, setActiveNodeKey] = useState('');
  const flatNodes = groups.flatMap((group) => group.nodes);
  const activeNode = flatNodes.find((node) => node.nodeKey === activeNodeKey) || flatNodes[0] || null;
  if (groups.length === 0) return null;
  return (
    <Panel title="阶段进度">
      <div className="workflow-stage-progress">
        <div className="workflow-stage-track" aria-label="工作流阶段">
          {groups.map((group) => (
            <section className="workflow-stage-group" key={group.key} aria-label={`第 ${group.index + 1} 阶段`}>
              <div className="workflow-stage-group-head">
                <span>第 {group.index + 1} 阶段</span>
                <em>{group.executionLabel}</em>
              </div>
              <div className="workflow-stage-node-grid">
                {group.nodes.map((node) => (
                  <button
                    type="button"
                    className={'workflow-stage-node workflow-stage-node-' + node.statusKind}
                    key={node.nodeKey}
                    onFocus={() => setActiveNodeKey(node.nodeKey)}
                    onMouseEnter={() => setActiveNodeKey(node.nodeKey)}
                    aria-label={`${node.title} ${dagStatusLabel(node.status)}`}
                  >
                    <strong>{node.title}</strong>
                    <span>{dagStatusLabel(node.status)}</span>
                  </button>
                ))}
              </div>
            </section>
          ))}
        </div>
        <WorkflowStageOperationPanel node={activeNode} />
      </div>
    </Panel>
  );
}

function WorkflowStageOperationPanel({ node }) {
  if (!node) return null;
  return (
    <aside className="workflow-stage-operation" aria-label="节点操作说明">
      <span>{node.executionLabel}</span>
      <h4>{node.stageTitle}</h4>
      <p>{node.operationSummary}</p>
      <dl>
        <div>
          <dt>模型操作</dt>
          <dd>{node.modelAction}</dd>
        </div>
        <div>
          <dt>Skill / 工具</dt>
          <dd>{node.skillsText}</dd>
        </div>
        <div>
          <dt>输入来源</dt>
          <dd>{node.inputSourcesText}</dd>
        </div>
        <div>
          <dt>输出文件</dt>
          <dd>{node.outputsText}</dd>
        </div>
      </dl>
    </aside>
  );
}

function workflowStageGroups(nodes = []) {
  const orderedNodes = workflowOrderedNodes(nodes);
  const byKey = new Map(orderedNodes.flatMap((node) => {
    const key = textValue(node?.nodeKey);
    return key ? [[key, node]] : [];
  }));
  const memo = new Map();
  const visiting = new Set();
  const depthFor = (node) => {
    const key = textValue(node?.nodeKey);
    if (!key) return 0;
    if (memo.has(key)) return memo.get(key);
    if (visiting.has(key)) return 0;
    visiting.add(key);
    const deps = Array.isArray(node.dependsOn) ? node.dependsOn : [];
    const depth = deps.reduce((max, depKey) => {
      const dependency = byKey.get(depKey);
      return dependency ? Math.max(max, depthFor(dependency) + 1) : max;
    }, 0);
    visiting.delete(key);
    memo.set(key, depth);
    return depth;
  };
  const groups = new Map();
  orderedNodes.forEach((node) => {
    const depth = depthFor(node);
    if (!groups.has(depth)) groups.set(depth, []);
    groups.get(depth).push(workflowStageNodeView(node, depth));
  });
  return Array.from(groups.entries())
    .sort(([left], [right]) => left - right)
    .map(([depth, groupNodes], index) => ({
      key: `stage:${depth}`,
      index,
      nodes: groupNodes,
      executionLabel: workflowStageGroupExecutionLabel(groupNodes),
    }));
}

function workflowStageNodeView(node, depth) {
  const config = parsedDagConfig(node?.config);
  const ui = objectValue(config.ui);
  const outputs = workflowStageOutputPaths(ui, config);
  const skills = listFromMaybe(ui.skills);
  const inputSources = listFromMaybe(firstPresent(ui.input_sources, ui.inputSources));
  const executionMode = firstText(ui.execution_mode, ui.executionMode).toLowerCase();
  return {
    nodeKey: firstText(node?.nodeKey, `stage-node:${depth}`),
    title: firstText(node?.title, ui.stage_title, ui.stageTitle, node?.nodeKey, `阶段 ${depth + 1}`),
    status: textValue(node?.status),
    statusKind: workflowStageStatusKind(node?.status),
    stageTitle: firstText(ui.stage_title, ui.stageTitle, node?.title, node?.nodeKey, `阶段 ${depth + 1}`),
    operationSummary: firstText(ui.operation_summary, ui.operationSummary, workflowStageFallbackOperation(node, config)),
    modelAction: firstText(ui.model_action, ui.modelAction, workflowStageFallbackModelAction(node, config)),
    skillsText: skills.length > 0 ? skills.join('、') : '未声明，等待 DAG 设计器补充',
    inputSourcesText: inputSources.length > 0 ? inputSources.join('、') : workflowStageDependencyText(node),
    outputsText: outputs.length > 0 ? outputs.join('、') : '未声明 sharedfile 输出',
    executionMode,
    executionLabel: executionMode === 'parallel' ? '并行执行' : '顺序执行',
  };
}

function workflowStageGroupExecutionLabel(nodes = []) {
  if (nodes.length > 1 || nodes.some((node) => node.executionMode === 'parallel')) return '并行执行';
  return '顺序执行';
}

function workflowStageStatusKind(status) {
  const value = textValue(status).toLowerCase();
  if (['done', 'succeeded', 'success', 'completed'].includes(value)) return 'done';
  if (['running', 'dispatching', 'active'].includes(value)) return 'active';
  if (['failed', 'error', 'blocked'].includes(value)) return 'failed';
  if (['waiting_for_assignee', 'ready', 'pending'].includes(value)) return 'attention';
  if (['cancelled', 'canceled', 'terminated'].includes(value)) return 'neutral';
  return 'waiting';
}

function workflowStageOutputPaths(ui, config) {
  const expected = Array.isArray(ui.expected_outputs) ? ui.expected_outputs : (Array.isArray(ui.expectedOutputs) ? ui.expectedOutputs : []);
  const paths = expected.flatMap((item) => {
    if (!item) return [];
    if (typeof item === 'string') return [item];
    const path = firstText(item.path, item.sharedfile, item.shared_file);
    return path ? [path] : [];
  });
  const outputs = objectValue(config.outputs);
  const toSharedfile = objectValue(outputs.to_sharedfile);
  const sharedfilePath = textValue(toSharedfile.path);
  if (sharedfilePath) paths.push(sharedfilePath);
  const toArtifact = objectValue(outputs.to_artifact);
  const artifactPath = firstText(toArtifact.path_template, toArtifact.pathTemplate, toArtifact.path);
  if (artifactPath) paths.push(artifactPath);
  return [...new Set(paths)];
}

function workflowStageFallbackOperation(node, config) {
  const outputs = objectValue(config.outputs);
  const toSharedfile = objectValue(outputs.to_sharedfile);
  const outputPath = textValue(toSharedfile.path);
  if (outputPath) return `该节点按配置生成材料并写入 ${outputPath}。`;
  const toArtifact = objectValue(outputs.to_artifact);
  const artifactPath = firstText(toArtifact.path_template, toArtifact.pathTemplate, toArtifact.path);
  if (artifactPath) return `该节点按配置生成最终产物并写入 ${artifactPath}。`;
  return '该节点尚未声明悬停说明，前端根据节点标题和状态展示保守说明。';
}

function workflowStageFallbackModelAction(node, config) {
  const exec = objectValue(config.exec);
  const promptKey = firstText(exec.prompt_key, exec.promptKey, exec.verifier?.prompt_key, exec.verifier?.promptKey);
  const commandRef = firstText(exec.command_ref, exec.commandRef, exec.automation?.command_ref, exec.automation?.commandRef);
  if (promptKey) return `使用已发现的 prompt ${promptKey} 处理该阶段输入。`;
  if (commandRef) return `调用已发现的 command_card ${commandRef} 执行该阶段自动化。`;
  return `处理 ${firstText(node?.title, node?.nodeKey, '当前阶段')} 的输入并产出阶段结果。`;
}

function workflowStageDependencyText(node) {
  const deps = Array.isArray(node?.dependsOn) ? node.dependsOn.filter(Boolean) : [];
  return deps.length > 0 ? deps.join('、') : '首阶段输入或用户提供材料';
}

function listFromMaybe(value) {
  if (Array.isArray(value)) return value.flatMap((item) => {
    const text = textValue(item);
    return text ? [text] : [];
  });
  const text = textValue(value);
  return text ? wordListFromText(text) : [];
}

export { WorkflowStageProgress };
