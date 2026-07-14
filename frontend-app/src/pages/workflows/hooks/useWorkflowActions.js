import { useCallback, useRef } from 'react';
import { errorMessage, firstText, objectValue, textValue } from '../../shared/pageShared.js';
import {
  applyDagOps,
  createAndStartDag,
  deleteDag,
  dispatchDagNode,
  renderWorkflowTemplateDraft,
  startDag,
  startThread,
  startTurn,
  terminateDagRun,
} from '../services/workflowPageService.js';
import { dagCategoryOf, isScheduledTrigger, runKeyOf, withWorkflowActionTimeout } from '../services/workflowDagModel.js';
import { dagNodePatchFromForm, threadIdFromStartResponse } from '../services/workflowNodeModel.js';
import {
  buildEnterpriseWorkflowTemplateBrief,
  DAG_DESIGNER_ENABLED_TOOLS,
  ENTERPRISE_DESIGN_PHASES,
  enterpriseCreateAndStartDAGPayload,
  enterpriseTemplateId,
  enterpriseTemplateTitle,
  enterpriseTemplateVersionNumber,
  firstEnterpriseOutputType,
  uniqueWorkflowActionKey,
  workflowMonotonicTimestamp,
} from '../services/workflowEnterpriseTemplateModel.js';

const workflowActionFacade = Object.freeze({ applyDagOps, dispatchDagNode, terminateDagRun });

function workflowRefreshNotice(message, refreshError) {
  if (!refreshError) return message;
  return `${message}，但刷新状态失败：${errorMessage(refreshError)}`;
}

async function refreshWorkflowListResult(list, fallbackItems) {
  try {
    return { error: null, items: await list.refreshDags() };
  } catch (err) {
    return { error: err, items: fallbackItems };
  }
}

async function refreshWorkflowDetailResult(refresh, targetDagKey, runKey = '') {
  if (!refresh || !textValue(targetDagKey)) return null;
  try {
    await refresh.refreshDetail(targetDagKey, runKey);
    return null;
  } catch (err) {
    return err;
  }
}

function firstWorkflowRefreshError(...errors) {
  return errors.find(Boolean) || null;
}

function isIdempotencyKeyExhaustedError(error) {
  return firstText(error?.message, error?.data?.message, error?.data)
    .toLowerCase()
    .includes('idempotency key exhausted:');
}

async function refreshWorkflowAfterAction({ fallbackItems, list, refresh, runKey = '', targetDagKey }) {
  const listResult = list ? await refreshWorkflowListResult(list, fallbackItems) : { error: null, items: fallbackItems };
  const detailError = refresh ? await refreshWorkflowDetailResult(refresh, targetDagKey, runKey) : null;
  return { error: firstWorkflowRefreshError(listResult.error, detailError), items: listResult.items };
}

function useWorkflowActions(options) {
  const { actionState, derived, list, notices, refresh, selection, setDesignSession, store, workflowCwd } = options;
  /*
   * workflow actions 只提交操作并刷新数据。
   * DAG 的真实状态以后端刷新结果为准，本地只放按钮和提示状态。
   */
  const runSelectedDag = useRunSelectedDagAction({ actionState, derived, list, notices, refresh });
  const stopSelectedDag = useStopSelectedDagAction({ actionState, derived, list, notices, refresh });
  const confirmDeleteDAG = useDeleteDagAction({ actionState, derived, list, notices, selection });
  const saveSchedule = useSaveScheduleAction({ actionState, derived, list, notices, refresh });
  const toggleScheduleEnabled = useToggleScheduleAction({ actionState, derived, list, notices, refresh });
  const saveAgentNode = useSaveAgentNodeAction({ actionState, derived, notices, refresh });
  const dispatchNode = useDispatchDagNodeAction({ actionState, derived, list, notices, refresh });
  const createAndStartTemplate = useCreateAndStartTemplateAction({ actionState, list, notices, refresh, workflowCwd });
  const startDesignFlow = useStartDesignFlowAction({ actionState, notices, setDesignSession, store, workflowCwd });
  return { confirmDeleteDAG, createAndStartTemplate, dispatchNode, runSelectedDag, saveAgentNode, saveSchedule, startDesignFlow, stopSelectedDag, toggleScheduleEnabled };
}

function useRunSelectedDagAction({ actionState, derived, list, notices, refresh }) {
  const runIntentsRef = useRef(new Map());
  return useCallback(async () => {
    if (derived.startDisabledReason) return;
    const targetDagKey = derived.dagKey;
    let intent = runIntentsRef.current.get(targetDagKey);
    if (!intent) {
      intent = { idempotencyKey: uniqueWorkflowActionKey('ui'), pending: false };
      runIntentsRef.current.set(targetDagKey, intent);
    }
    if (intent.pending) return;
    intent.pending = true;
    actionState.setActioning('start');
    actionState.setError('');
    notices.clearNotice();
    try {
      const result = await startDag({ dagKey: targetDagKey, triggerSource: 'manual', idempotencyKey: intent.idempotencyKey });
      runIntentsRef.current.delete(targetDagKey);
      const runKey = runKeyOf(result);
      const refreshResult = await refreshWorkflowAfterAction({ list, refresh, runKey, targetDagKey });
      const warning = textValue(result?.warning);
      const message = warning ? '已启动，后端提示：' + warning : '已启动自动化';
      notices.showTaskNotice(workflowRefreshNotice(message, refreshResult.error), targetDagKey);
    } catch (err) {
      if (isIdempotencyKeyExhaustedError(err)) {
        runIntentsRef.current.delete(targetDagKey);
      } else {
        intent.pending = false;
      }
      actionState.setError('启动自动化失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useStopSelectedDagAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(
    () => stopSelectedDagAction({
      actionState,
      derived,
      facade: workflowActionFacade,
      notices,
      refreshContext: { list, refresh },
    }),
    [actionState, derived, list, notices, refresh],
  );
}

export async function stopSelectedDagAction({ actionState, derived, facade, notices, refreshContext }) {
  if (!derived.dagKey || !derived.activeRunKey) return;
  const targetDagKey = derived.dagKey;
  actionState.setActioning('stop');
  actionState.setError('');
  notices.clearNotice();
  try {
    await withWorkflowActionTimeout(facade.terminateDagRun({ dagKey: targetDagKey, runKey: derived.activeRunKey, reason: 'user_requested' }));
    const refreshResult = await refreshWorkflowAfterAction({ ...refreshContext, targetDagKey });
    notices.showTaskNotice(workflowRefreshNotice('已停止运行', refreshResult.error), targetDagKey);
  } catch (err) {
    actionState.setError('停止运行失败：' + errorMessage(err));
  } finally {
    actionState.setActioning('');
  }
}

function useDeleteDagAction({ actionState, derived, list, notices, selection }) {
  return useCallback(async () => {
    const target = actionState.deleteTarget;
    const targetKey = target?.dagKey || derived.dagKey;
    if (!targetKey) return;
    if (derived.activeRunKey) {
      actionState.setDeleteTarget(null);
      actionState.setError('删除自动化失败：已有运行正在进行，请先停止运行后再删除。');
      return;
    }
    actionState.setActioning('delete');
    actionState.setError('');
    notices.clearNotice();
    try {
      await withWorkflowActionTimeout(deleteDag({ dagKey: targetKey }));
      actionState.setDeleteTarget(null);
      const fallback = list.items.filter((item) => item.dagKey !== targetKey);
      const refreshResult = await refreshWorkflowAfterAction({ fallbackItems: fallback, list, targetDagKey: targetKey });
      const nextItems = refreshResult.items || fallback;
      selection.setSelectedDagKey(nextWorkflowSelectionKey(nextItems, selection.activeCategory));
      notices.showTaskNotice(workflowRefreshNotice('已删除 ' + (target?.title || targetKey), refreshResult.error), targetKey);
    } catch (err) {
      actionState.setError('删除自动化失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, selection]);
}

function nextWorkflowSelectionKey(items, activeCategory) {
  return firstText(items.find((item) => dagCategoryOf(item) === activeCategory)?.dagKey, items[0]?.dagKey);
}

function useSaveScheduleAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(
    (nextCronExpr = '') => saveScheduleAction(
      {
        actionState,
        derived,
        facade: workflowActionFacade,
        notices,
        refreshContext: { list, refresh },
      },
      nextCronExpr,
    ),
    [actionState, derived, list, notices, refresh],
  );
}

export async function saveScheduleAction(
  { actionState, derived, facade, notices, refreshContext },
  nextCronExpr = '',
) {
  const cronExpr = textValue(nextCronExpr) || textValue(actionState.scheduleCron);
  if (!derived.dagKey || !cronExpr) return;
  if (derived.baseVersion === null) { actionState.setError('自动化详情不完整，无法保存定时任务'); return; }
  if (derived.missingRootAssigneeWarning) { actionState.setError('保存定时任务失败：' + derived.missingRootAssigneeWarning); return; }
  const targetDagKey = derived.dagKey;
  actionState.setActioning('schedule');
  actionState.setError('');
  notices.clearNotice();
  try {
    const activeDetailDag = derived.activeDetailDag;
    const schedulePatch = { trigger: 'scheduled', cron_expr: cronExpr };
    const preservingExistingSchedule = isScheduledTrigger(activeDetailDag?.trigger) || Boolean(activeDetailDag?.cronExpr);
    if (preservingExistingSchedule && activeDetailDag?.scheduleEnabled === false) {
      schedulePatch.schedule_enabled = false;
    }
    const ops = [{ op: 'update_dag', patch: schedulePatch }];
    await withWorkflowActionTimeout(facade.applyDagOps({ baseVersion: derived.baseVersion, dagKey: targetDagKey, ops }));
    actionState.setScheduleOpen(false);
    const refreshResult = await refreshWorkflowAfterAction({ ...refreshContext, targetDagKey });
    notices.showTaskNotice(workflowRefreshNotice('已保存定时任务', refreshResult.error), targetDagKey);
  } catch (err) {
    actionState.setError('保存定时任务失败：' + errorMessage(err));
  } finally {
    actionState.setActioning('');
  }
}

function useToggleScheduleAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(async () => {
    if (!derived.dagKey) return;
    if (derived.baseVersion === null) { actionState.setError('自动化详情不完整，无法切换自动运行'); return; }
    const targetDagKey = derived.dagKey;
    const enabled = !derived.activeDetailDag?.scheduleEnabled;
    actionState.setActioning('schedule-toggle');
    actionState.setError('');
    notices.clearNotice();
    try {
      const patch = { schedule_enabled: enabled };
      const ops = [{ op: 'update_dag', patch }];
      await withWorkflowActionTimeout(applyDagOps({ baseVersion: derived.baseVersion, dagKey: targetDagKey, ops }));
      const refreshResult = await refreshWorkflowAfterAction({ list, refresh, targetDagKey });
      notices.showTaskNotice(workflowRefreshNotice(enabled ? '已启用自动运行' : '已暂停自动运行', refreshResult.error), targetDagKey);
    } catch (err) {
      actionState.setError('切换自动运行失败：' + errorMessage(err));
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, derived, list, notices, refresh]);
}

function useSaveAgentNodeAction({ actionState, derived, notices, refresh }) {
  return useCallback(async (form, node) => {
    if (!derived.dagKey || !node?.nodeKey) return;
    if (derived.baseVersion === null) { actionState.setError('自动化详情不完整，无法保存步骤'); return; }
    const targetDagKey = derived.dagKey;
    actionState.setSavingNodeKey(node.nodeKey);
    actionState.setError('');
    notices.clearNotice();
    try {
      const patch = dagNodePatchFromForm(form, node);
      const ops = [{ node_key: node.nodeKey, op: 'update_node', patch }];
      await withWorkflowActionTimeout(applyDagOps({ baseVersion: derived.baseVersion, dagKey: targetDagKey, ops }));
      const refreshError = await refreshWorkflowDetailResult(refresh, targetDagKey);
      notices.showTaskNotice(workflowRefreshNotice('已保存步骤 ' + node.title, refreshError), targetDagKey);
    } catch (err) {
      actionState.setError('保存步骤失败：' + errorMessage(err));
    } finally {
      actionState.setSavingNodeKey('');
    }
  }, [actionState, derived, notices, refresh]);
}

function useDispatchDagNodeAction({ actionState, derived, list, notices, refresh }) {
  return useCallback(
    (node, assignedTo) => dispatchDagNodeAction(
      {
        actionState,
        derived,
        facade: workflowActionFacade,
        notices,
        refreshContext: { list, refresh },
      },
      node,
      assignedTo,
    ),
    [actionState, derived, list, notices, refresh],
  );
}

export async function dispatchDagNodeAction(
  { actionState, derived, facade, notices, refreshContext },
  node,
  assignedTo,
) {
  const assignee = textValue(assignedTo);
  if (!derived.dagKey || !node?.nodeKey) return;
  if (!derived.runId) { actionState.setError('派发节点失败：当前运行缺少 runId，无法定位 runtime node'); return; }
  if (!assignee) { actionState.setError('派发节点失败：请填写执行者 assigned_to'); return; }
  const targetDagKey = derived.dagKey;
  actionState.setDispatchingNodeKey(node.nodeKey);
  actionState.setError('');
  notices.clearNotice();
  try {
    await withWorkflowActionTimeout(facade.dispatchDagNode({
      dagKey: targetDagKey,
      runId: derived.runId,
      nodeKey: node.nodeKey,
      assignedTo: assignee,
    }));
    const refreshResult = await refreshWorkflowAfterAction({ ...refreshContext, targetDagKey });
    notices.showTaskNotice(workflowRefreshNotice(`已派发步骤 ${node.title || node.nodeKey}`, refreshResult.error), targetDagKey);
  } catch (err) {
    actionState.setError('派发节点失败：' + errorMessage(err));
  } finally {
    actionState.setDispatchingNodeKey('');
  }
}

function useCreateAndStartTemplateAction({ actionState, list, notices, refresh, workflowCwd }) {
  return useCallback(async (template) => {
    if (!workflowCwd) {
      actionState.setError('创建模板工作流失败：项目路径不可用。');
      return { ok: false };
    }
    const templateId = enterpriseTemplateId(template);
    const values = objectValue(template?.templateValues);
    actionState.setActioning('template-create');
    actionState.setError('');
    notices.clearNotice();
    try {
      const version = enterpriseTemplateVersionNumber(template);
      const payload = workflowTemplateDraftPayload(templateId, version, values, workflowCwd);
      const rendered = await withWorkflowActionTimeout(renderWorkflowTemplateDraft(payload));
      const draft = rendered?.draft;
      if (!draft) throw new Error('workflowTemplates/renderDag 未返回 DAG 草案');
      const result = await withWorkflowActionTimeout(createAndStartDag(enterpriseCreateAndStartDAGPayload(draft)));
      const dagKey = firstText(result?.dagKey, result?.dag_key, draft.dag_key, draft.dagKey);
      const runKey = runKeyOf(result);
      const refreshResult = await refreshWorkflowAfterAction({ list, refresh, runKey, targetDagKey: dagKey });
      const warning = textValue(result?.warning);
      const message = warning ? '已创建并启动，后端提示：' + warning : '已创建并启动自动化';
      notices.showTaskNotice(workflowRefreshNotice(message, refreshResult.error), dagKey);
      return { ok: true, dagKey, runKey, warning };
    } catch (err) {
      actionState.setError('创建模板工作流失败：' + errorMessage(err));
      return { ok: false, error: errorMessage(err) };
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, list, notices, refresh, workflowCwd]);
}

function captureWorkflowThreadSelection(store) {
  if (typeof store?.captureThreadSelection !== 'function') throw new Error('自动化会话选择快照能力不可用');
  if (typeof store?.setActiveThread !== 'function') throw new Error('自动化会话选择能力不可用');
  if (typeof store?.setActivePage !== 'function') throw new Error('自动化页面导航能力不可用');
  return store.captureThreadSelection();
}

async function navigateToWorkflowThread(store, threadId, selectionSnapshot) {
  if (threadId) {
    const selected = await store.setActiveThread(threadId, { selectionSnapshot });
    if (selected === false) return false;
  }
  store.setActivePage('chat');
  return true;
}

function useStartDesignFlowAction({ actionState, notices, setDesignSession, store, workflowCwd }) {
  return useCallback(async (template = null, options = {}) => {
    if (!workflowCwd) return;
    const isEnterpriseTemplate = Boolean(template);
    const stayOnWorkflow = Boolean(options.stayOnWorkflow);
    actionState.setActioning('design');
    actionState.setError('');
    notices.clearNotice();
    if (isEnterpriseTemplate) {
      setDesignSession?.(enterpriseDesignSessionSnapshot(template, {
        phase: 'starting',
        message: '正在启动 DAG 设计器',
      }));
    } else if (stayOnWorkflow) {
      setDesignSession?.(freeDesignSessionSnapshot({
        phase: 'starting',
        message: '正在启动 AI 设计流程',
      }));
    }
    let selectionSnapshot;
    try {
      if (!isEnterpriseTemplate && !stayOnWorkflow) {
        selectionSnapshot = captureWorkflowThreadSelection(store);
      }
      if (typeof store?.resolveLaunchPreferences !== 'function') throw new Error('自动化启动配置不可用');
      const launchPreferences = await store.resolveLaunchPreferences(workflowCwd);
      const { config: launchConfigRaw, ...launchPayload } = objectValue(launchPreferences);
      const launchConfig = objectValue(launchConfigRaw);
      const response = await withWorkflowActionTimeout(startThread(workflowDesignThreadPayload(workflowCwd, launchConfig, launchPayload)));
      const threadId = threadIdFromStartResponse(response);
      if (template) {
        if (!threadId) throw new Error('thread/start 未返回可用 threadId，无法发送模板需求');
        const submitted = await submitEnterpriseTemplateDesignBrief(actionState, setDesignSession, template, threadId, workflowCwd);
        if (!submitted) return;
      }
      if (isEnterpriseTemplate) return;
      if (stayOnWorkflow) {
        const message = threadId ? 'AI 设计流程已创建，可进入对话继续描述需求。' : 'AI 设计流程已创建。';
        setDesignSession?.(freeDesignSessionSnapshot(workflowTemplateDesignPatch(threadId, 'submitted', message)));
        return;
      }
      await navigateToWorkflowThread(store, threadId, selectionSnapshot);
    } catch (err) {
      actionState.setError((template ? '启动政企模板失败：' : '启动 AI 设计流程失败：') + errorMessage(err));
      if (isEnterpriseTemplate) {
        const failed = workflowTemplateDesignPatch('', 'failed', '启动政企模板失败：' + errorMessage(err));
        setDesignSession?.(enterpriseDesignSessionSnapshot(template, failed));
      } else if (stayOnWorkflow) {
        const failed = workflowTemplateDesignPatch('', 'failed', '启动 AI 设计流程失败：' + errorMessage(err));
        setDesignSession?.(freeDesignSessionSnapshot(failed));
      }
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, notices, setDesignSession, store, workflowCwd]);
}

function enterpriseDesignSessionSnapshot(template, patch = {}) {
  const values = objectValue(template?.templateValues);
  const outputFormat = textValue(values.output_format || template?.selectedOutputFormat || firstEnterpriseOutputType(template)) || 'markdown';
  return {
    templateKey: enterpriseTemplateId(template),
    templateTitle: enterpriseTemplateTitle(template),
    outputFormat,
    phases: ENTERPRISE_DESIGN_PHASES,
    phase: 'starting',
    threadId: '',
    message: '',
    startedAt: workflowMonotonicTimestamp(),
    ...patch,
  };
}

function freeDesignSessionSnapshot(patch = {}) {
  return {
    templateKey: 'free-design',
    templateTitle: '自由设计',
    outputFormat: 'dag',
    phases: ['启动设计器', '创建对话', '描述需求', '生成方案', '创建自动化'],
    phase: 'starting',
    threadId: '',
    message: '',
    startedAt: workflowMonotonicTimestamp(),
    ...patch,
  };
}

function workflowDesignThreadPayload(cwd, launchConfig, launchPayload) {
  return {
    cwd,
    ...launchPayload,
    provider: textValue(launchPayload.provider || launchPayload.modelProvider),
    name: 'AI 设计流程',
    agentKey: 'dag_designer',
    promptKey: 'main/dag_designer_zh',
    deferSpawn: true,
    config: { ...launchConfig, enabledTools: [...DAG_DESIGNER_ENABLED_TOOLS], providerNativeSkills: false },
  };
}

function workflowTemplateStartTurnPayload(workflowCwd, threadId, template) {
  return {
    cwd: workflowCwd,
    input: buildEnterpriseWorkflowTemplateBrief(template),
    threadId,
  };
}

function workflowTemplateDraftPayload(templateId, version, values, workflowCwd) {
  return {
    runtime_context: { cwd: workflowCwd },
    templateId,
    values,
    version,
  };
}

function workflowTemplateDesignPatch(threadId, phase, message) {
  return { message, phase, threadId };
}

async function submitEnterpriseTemplateDesignBrief(actionState, setDesignSession, template, threadId, workflowCwd) {
  const sending = workflowTemplateDesignPatch(threadId, 'sending', '正在发送阶段拆分需求');
  setDesignSession?.(enterpriseDesignSessionSnapshot(template, sending));
  try {
    await withWorkflowActionTimeout(startTurn(workflowTemplateStartTurnPayload(workflowCwd, threadId, template)));
    const submitted = workflowTemplateDesignPatch(threadId, 'submitted', 'DAG 设计器已接收，正在评估阶段拆分和可用资源。');
    setDesignSession?.(enterpriseDesignSessionSnapshot(template, submitted));
    return true;
  } catch (err) {
    actionState.setError('发送政企模板需求失败：' + errorMessage(err));
    const failed = workflowTemplateDesignPatch(threadId, 'failed', '发送政企模板需求失败：' + errorMessage(err));
    setDesignSession?.(enterpriseDesignSessionSnapshot(template, failed));
    return false;
  }
}

export { useWorkflowActions };
