import { firstText, textValue } from '../../shared/pageShared.js';

/*
 * workflow display adapter 只把 DAG 节点变成展示用行。
 * 它不改节点，也不调用后端。
 */

function parsedWorkflowConfig(value) {
  if (!value) return {};
  if (typeof value === 'object' && !Array.isArray(value)) return value;
  if (typeof value !== 'string') return {};
  try {
    const parsed = JSON.parse(value);
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function finalOutputPath(value) {
  if (!value || typeof value !== 'object') return '';
  return firstText(value.path, value.sharedfile?.path, value.sharedFile?.path, value.shared_file?.path);
}

function finalOutputKind(value) {
  if (!value || typeof value !== 'object') return '';
  const kind = firstText(value.kind, value.type);
  const labels = {
    file: '文件',
    sharedfile: '文件',
    shared_file: '文件',
    text: '文本',
    json: '数据',
  };
  return labels[kind.toLowerCase()] || kind || (finalOutputPath(value) ? '文件' : '');
}

function workflowLockModeLabel(value) {
  const mode = textValue(value).toLowerCase();
  if (mode === 'exclusive') return '独占写入';
  if (mode === 'append') return '追加写入';
  if (mode === 'shared') return '共享读取';
  return mode || '-';
}

function workflowSharedFileRows(nodes = []) {
  const rows = [];
  nodes.forEach((node, index) => {
    const config = parsedWorkflowConfig(node?.config);
    const inputs = parsedWorkflowConfig(config.inputs);
    const outputs = parsedWorkflowConfig(config.outputs);
    const stepLabel = `第 ${index + 1} 步`;
    const nodeKey = node?.nodeKey || `node:${index}`;
    const reads = Array.isArray(inputs.from_sharedfiles) ? inputs.from_sharedfiles : [];
    reads.forEach((path) => {
      const filePath = textValue(path);
      if (filePath) rows.push({ nodeKey, stepLabel, path: filePath, access: '读取' });
    });
    const target = parsedWorkflowConfig(outputs.to_sharedfile);
    const outputPath = textValue(target.path);
    if (outputPath) {
      rows.push({
        nodeKey,
        stepLabel,
        path: outputPath,
        access: `写入 · ${workflowLockModeLabel(target.lock_mode || target.lockMode)}`,
      });
    }
  });
  return rows;
}

function workflowOrderedNodes(nodes = []) {
  /*
   * 按 dependsOn 排序，遇到缺失依赖或环也继续展示。
   * 缺失依赖只在图里标成“外部依赖”。
   */
  const source = Array.isArray(nodes) ? nodes : [];
  const byKey = new Map(source.filter((node) => textValue(node?.nodeKey)).map((node) => [node.nodeKey, node]));
  const ordered = [];
  const visited = new Set();
  const visiting = new Set();

  const visit = (node) => {
    const key = textValue(node?.nodeKey);
    if (!key) {
      ordered.push(node);
      return;
    }
    if (visited.has(key) || visiting.has(key)) return;
    visiting.add(key);
    if (Array.isArray(node.dependsOn)) {
      node.dependsOn.forEach((depKey) => {
        const dependency = byKey.get(depKey);
        if (dependency) visit(dependency);
      });
    }
    visiting.delete(key);
    visited.add(key);
    ordered.push(node);
  };

  source.forEach((node) => visit(node));
  return ordered;
}

function workflowTopologyRows(nodes = []) {
  const orderedNodes = workflowOrderedNodes(nodes);
  const known = new Map(orderedNodes.map((node, index) => [node.nodeKey, node.title || `步骤 ${index + 1}`]));
  const missingLabels = new Map();
  const labelForMissing = (key) => {
    if (!missingLabels.has(key)) missingLabels.set(key, `外部依赖 ${missingLabels.size + 1}`);
    return missingLabels.get(key);
  };
  const edgeRows = orderedNodes.flatMap((node) => {
    const title = node.title || node.nodeKey;
    if (!Array.isArray(node.dependsOn) || node.dependsOn.length === 0) return [];
    return node.dependsOn.map((depKey) => `${known.get(depKey) || labelForMissing(depKey)} --> ${title}`);
  });
  if (edgeRows.length > 0) return edgeRows;
  return orderedNodes.map((node, index) => node.title || node.nodeKey || `步骤 ${index + 1}`);
}

export {
  finalOutputKind,
  finalOutputPath,
  workflowOrderedNodes,
  workflowSharedFileRows,
  workflowTopologyRows,
};
