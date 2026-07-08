import { firstText, listToText, objectValue, textValue, wordListFromText } from '../../shared/pageShared.js';
import { workflowOrderedNodes } from '../adapters/workflowDisplayAdapter.js';
import { firstPresent, optionalArrayField, parseJsonObject } from './workflowEnterpriseTemplateModel.js';

function finalOutputDescriptor(raw) {
  const source = objectValue(firstPresent(raw?.run, raw));
  const metadata = objectValue(source.metadata);
  return source.final_output || source.finalOutput || metadata.final_output || metadata.finalOutput || null;
}

function finalOutputPreviewText(value) {
  if (typeof value === 'string') return value.trim();
  if (value && typeof value === 'object') {
    return firstText(value.text, value.content, value.message, value.output, value.summary, JSON.stringify(value));
  }
  return '';
}

function parsedDagConfig(value, label = 'workflow dag config') {
  if (value === undefined || value === null || value === '') return {};
  if (typeof value === 'object' && !Array.isArray(value)) return value;
  if (typeof value !== 'string') throw new Error(`${label} must be a JSON object`);
  return parseJsonObject(value, label);
}

function cleanObject(payload) {
  return Object.fromEntries(
    Object.entries(payload).filter(([, value]) => value !== undefined && value !== ''),
  );
}

function validThreadIdText(value) {
  const text = textValue(value);
  if (!text || /^launch[_-]/i.test(text)) return '';
  return text;
}

function firstValidThreadId(...values) {
  for (const value of values) {
    const text = validThreadIdText(value);
    if (text) return text;
  }
  return '';
}

function threadIdFromStartResponse(value) {
  return firstValidThreadId(
    value?.threadId,
    value?.thread_id,
    value?.thread?.threadId,
    value?.thread?.thread_id,
    value?.id,
    value?.thread?.id,
    value?.agentId,
    value?.agent_id,
    value?.thread?.agentId,
    value?.thread?.agent_id,
  );
}

function dagNodeFormFromNode(node) {
  const nodeType = textValue(node?.nodeType).toLowerCase();
  const config = parsedDagConfig(node?.config);
  const exec = objectValue(config.exec);
  const agentExec = nodeType === 'hybrid' ? objectValue(exec.verifier) : exec;
  const automationExec = nodeType === 'hybrid' ? objectValue(exec.automation) : exec;
  const outputs = objectValue(config.outputs);
  const toSharedfile = objectValue(outputs.to_sharedfile);
  return {
    nodeKey: textValue(node?.nodeKey),
    nodeType,
    title: textValue(node?.title),
    assignedTo: textValue(node?.assignedTo),
    execProvider: firstText(agentExec.provider, config.provider),
    execModel: firstText(agentExec.model, config.model),
    execAgentKey: firstText(agentExec.agent_key, agentExec.agentKey, config.agent_key, config.agentKey),
    execPromptKey: firstText(agentExec.prompt_key, agentExec.promptKey, config.prompt_key, config.promptKey),
    execCwd: firstText(agentExec.cwd, agentExec.CWD, config.cwd),
    automationKind: firstText(automationExec.kind, 'command_card'),
    commandRef: firstText(automationExec.command_ref, automationExec.commandRef, node?.commandRef),
    dependsOn: listToText(optionalArrayField(node?.dependsOn, 'workflow normalized node dependsOn')),
    firstTurn: firstText(config.first_turn, config.firstTurn, config.prompt),
    outputSharedfilePath: firstText(toSharedfile.path, config.output_file, config.outputFile),
    outputSharedfileLockMode: firstText(toSharedfile.lock_mode, toSharedfile.lockMode, 'exclusive'),
    outputToNodeResult: outputs.to_node_result === true || outputs.toNodeResult === true,
  };
}

function dagNodePatchFromForm(form, node) {
  const nodeType = textValue(form.nodeType || node?.nodeType).toLowerCase();
  const baseConfig = stripLegacyDagNodeConfig(parsedDagConfig(node?.config));
  const config = {
    ...baseConfig,
    exec: dagNodeExecPatchFromForm(form, baseConfig, nodeType),
    outputs: dagNodeOutputsPatchFromForm(form, baseConfig),
  };
  if (nodeType === 'agent') {
    config.first_turn = textValue(form.firstTurn);
  }
  validateDagNodePatchForm(form, nodeType);
  return {
    title: textValue(form.title),
    assigned_to: textValue(form.assignedTo),
    depends_on: wordListFromText(form.dependsOn),
    config: cleanObject(config),
  };
}

function stripLegacyDagNodeConfig(config) {
  const {
    provider: _provider,
    model: _model,
    agent_key: _agentKeySnake,
    agentKey: _agentKey,
    prompt_key: _promptKeySnake,
    promptKey: _promptKey,
    output_file: _outputFileSnake,
    outputFile: _outputFile,
    prompt: _prompt,
    cwd: _cwd,
    ...rest
  } = objectValue(config);
  return rest;
}

function dagNodeExecPatchFromForm(form, config, nodeType) {
  const exec = objectValue(config.exec);
  if (nodeType === 'automation') {
    return cleanObject({
      ...exec,
      kind: textValue(form.automationKind) || 'command_card',
      command_ref: textValue(form.commandRef),
    });
  }
  if (nodeType === 'hybrid') {
    return cleanObject({
      ...exec,
      automation: cleanObject({
        ...objectValue(exec.automation),
        kind: textValue(form.automationKind) || 'command_card',
        command_ref: textValue(form.commandRef),
      }),
      verifier: dagNodeAgentExecPatchFromForm(form, objectValue(exec.verifier)),
    });
  }
  return dagNodeAgentExecPatchFromForm(form, exec);
}

function dagNodeAgentExecPatchFromForm(form, exec) {
  return cleanObject({
    ...objectValue(exec),
    provider: textValue(form.execProvider),
    model: textValue(form.execModel),
    agent_key: textValue(form.execAgentKey),
    prompt_key: textValue(form.execPromptKey),
    cwd: textValue(form.execCwd),
  });
}

function dagNodeOutputsPatchFromForm(form, config) {
  const outputs = { ...objectValue(config.outputs) };
  const path = textValue(form.outputSharedfilePath);
  if (path) {
    outputs.to_sharedfile = cleanObject({
      ...objectValue(outputs.to_sharedfile),
      path,
      lock_mode: textValue(form.outputSharedfileLockMode),
    });
  } else {
    delete outputs.to_sharedfile;
  }
  outputs.to_node_result = Boolean(form.outputToNodeResult);
  return cleanObject(outputs);
}

function validateDagNodePatchForm(form, nodeType) {
  const provider = textValue(form.execProvider);
  if (provider && provider !== 'claude' && provider !== 'codex') throw new Error('config.exec.provider 只能是 claude 或 codex');
  const lockMode = textValue(form.outputSharedfileLockMode);
  if (lockMode && !['exclusive', 'append', 'shared'].includes(lockMode)) throw new Error('outputs.to_sharedfile.lock_mode 只能是 exclusive、append 或 shared');
  if (!textValue(form.outputSharedfilePath) && lockMode && lockMode !== 'exclusive') throw new Error('outputs.to_sharedfile.path 为空时不能设置写入模式');
  if (nodeType === 'automation' || nodeType === 'hybrid') validateAutomationExecForm(form);
  if (nodeType === 'agent' || nodeType === 'hybrid') validateAgentExecForm(form);
}

function validateAutomationExecForm(form) {
  if ((textValue(form.automationKind) || 'command_card') !== 'command_card') throw new Error('config.exec.kind 当前只能是 command_card');
  if (!textValue(form.commandRef)) throw new Error('config.exec.command_ref 不能为空');
}

function validateAgentExecForm(form) {
  if (!dagNodeAgentExecFormHasValues(form)) return;
  if (!textValue(form.execAgentKey) && !textValue(form.execPromptKey)) throw new Error('config.exec.agent_key 或 config.exec.prompt_key 至少填写一个');
  const cwd = textValue(form.execCwd);
  if (cwd && !cwd.startsWith('/')) throw new Error('config.exec.cwd 必须是绝对路径');
}

function dagNodeAgentExecFormHasValues(form) {
  return Boolean(
    textValue(form.execProvider)
    || textValue(form.execModel)
    || textValue(form.execAgentKey)
    || textValue(form.execPromptKey)
    || textValue(form.execCwd),
  );
}

function workflowDiagnosticNodes(detail, run) {
  const runtimeNodes = Array.isArray(run?.selectedRun?.nodes) ? run.selectedRun.nodes : [];
  return workflowOrderedNodes(runtimeNodes.length > 0 ? runtimeNodes : detail.nodes);
}

function workflowNodeDiagnostics(nodes = []) {
  return nodes.flatMap((node) => {
    const status = textValue(node?.status).toLowerCase();
    const diagnostics = [];
    if ((status === 'ready' || status === 'pending') && !textValue(node?.assignedTo)) {
      diagnostics.push({
        key: `${node.nodeKey}:waiting_for_assignee`,
        node,
        severity: 'blocked',
        title: '等待执行者',
        message: `${node.title || node.nodeKey} 缺少 assigned_to，后端不会自动 enqueue wakeup。`,
        recovery: 'dispatch',
      });
    }
    if (status === 'ready' && !node?.activeWakeupId) {
      diagnostics.push({
        key: `${node.nodeKey}:ready_no_wakeup`,
        node,
        severity: 'blocked',
        title: 'ready-no-wakeup',
        message: `${node.title || node.nodeKey} 已 ready 但没有 active_wakeup_id，请指派执行者后手动派发。`,
        recovery: textValue(node?.assignedTo) ? 'dispatch' : '',
      });
    }
    if (status === 'blocked') {
      diagnostics.push({
        key: `${node.nodeKey}:blocked`,
        node,
        severity: 'blocked',
        title: 'blocked',
        message: workflowNodeFailureText(node) || `${node.title || node.nodeKey} 被后端标记为 blocked。`,
      });
    }
    if (status === 'failed') {
      diagnostics.push({
        key: `${node.nodeKey}:failed`,
        node,
        severity: 'failed',
        title: 'failed',
        message: workflowNodeFailureText(node) || `${node.title || node.nodeKey} 执行失败，请查看节点结果或对话。`,
      });
    }
    return diagnostics;
  });
}

function workflowNodeFailureText(node) {
  const result = parsedDagConfig(firstPresent(node?.result, node?.raw?.result));
  return firstText(
    result.error_summary,
    result.errorSummary,
    result.reason,
    result.message,
    result.failure_class,
    result.failureClass,
  );
}

function rootNodesMissingAssignee(nodes = []) {
  if (!Array.isArray(nodes) || nodes.length === 0) return [];
  return nodes.filter((node) => {
    const dependsOn = Array.isArray(node?.dependsOn) ? node.dependsOn : [];
    return dependsOn.length === 0 && !textValue(node?.assignedTo);
  });
}

function rootAssigneeWarning(nodes = []) {
  const missing = rootNodesMissingAssignee(nodes);
  if (missing.length === 0) return '';
  const names = missing.flatMap((node) => {
    const name = node.title || node.nodeKey;
    return name ? [name] : [];
  }).join('、');
  return names ? `首个步骤「${names}」缺少执行者，请先在高级设置中填写执行者。` : '首个步骤缺少执行者，请先在高级设置中填写执行者。';
}

export {
  dagNodeAgentExecFormHasValues,
  dagNodeFormFromNode,
  dagNodePatchFromForm,
  finalOutputDescriptor,
  finalOutputPreviewText,
  firstValidThreadId,
  rootAssigneeWarning,
  threadIdFromStartResponse,
  validThreadIdText,
  validateDagNodePatchForm,
  workflowDiagnosticNodes,
  workflowNodeDiagnostics,
  workflowNodeFailureText,
};
