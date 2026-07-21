import { useCallback } from 'react';
import { firstText, objectValue, textValue } from '../../shared/pageShared.js';
import {
  createAndStartDag,
  renderWorkflowTemplateDraft,
  startThread,
  startTurn,
} from '../services/workflowPageService.js';
import { runKeyOf, withWorkflowActionTimeout } from '../services/workflowDagModel.js';
import { threadIdFromStartResponse } from '../services/workflowNodeModel.js';
import {
  buildEnterpriseWorkflowTemplateBrief,
  DAG_DESIGNER_ENABLED_TOOLS,
  ENTERPRISE_DESIGN_PHASES,
  enterpriseCreateAndStartDAGPayload,
  enterpriseTemplateId,
  enterpriseTemplateTitle,
  enterpriseTemplateVersionNumber,
  firstEnterpriseOutputType,
  workflowMonotonicTimestamp,
} from '../services/workflowEnterpriseTemplateModel.js';
import {
  refreshWorkflowAfterAction,
  workflowRefreshNotice,
} from './workflowActionRefresh.js';

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
      const result = await withWorkflowActionTimeout(
        createAndStartDag(enterpriseCreateAndStartDAGPayload(draft)),
      );
      const dagKey = firstText(result?.dagKey, result?.dag_key, draft.dag_key, draft.dagKey);
      const runKey = runKeyOf(result);
      const refreshResult = await refreshWorkflowAfterAction({
        list,
        refresh,
        runKey,
        targetDagKey: dagKey,
      });
      const warning = textValue(result?.warning);
      const message = warning ? '已创建并启动，后端提示：' + warning : '已创建并启动自动化';
      notices.showTaskNotice(workflowRefreshNotice(message, refreshResult.error), dagKey);
      return { ok: true, dagKey, runKey, warning };
    } catch (err) {
      actionState.setError('创建模板工作流失败，请重试。');
      throw err;
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, list, notices, refresh, workflowCwd]);
}

function captureWorkflowThreadSelection(store) {
  if (typeof store?.captureThreadSelection !== 'function') {
    throw new Error('自动化会话选择快照能力不可用');
  }
  if (typeof store?.setActiveThread !== 'function') {
    throw new Error('自动化会话选择能力不可用');
  }
  if (typeof store?.setActivePage !== 'function') {
    throw new Error('自动化页面导航能力不可用');
  }
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
      if (typeof store?.resolveLaunchPreferences !== 'function') {
        throw new Error('自动化启动配置不可用');
      }
      const launchPreferences = await store.resolveLaunchPreferences(workflowCwd);
      const { config: launchConfigRaw, ...launchPayload } = objectValue(launchPreferences);
      const launchConfig = objectValue(launchConfigRaw);
      const response = await withWorkflowActionTimeout(
        startThread(workflowDesignThreadPayload(workflowCwd, launchConfig, launchPayload)),
      );
      const threadId = threadIdFromStartResponse(response);
      if (template) {
        if (!threadId) throw new Error('thread/start 未返回可用 threadId，无法发送模板需求');
        const submitted = await submitEnterpriseTemplateDesignBrief(
          actionState,
          setDesignSession,
          template,
          threadId,
          workflowCwd,
        );
        if (!submitted) return;
      }
      if (isEnterpriseTemplate) return;
      if (stayOnWorkflow) {
        const message = threadId
          ? 'AI 设计流程已创建，可进入对话继续描述需求。'
          : 'AI 设计流程已创建。';
        setDesignSession?.(freeDesignSessionSnapshot(
          workflowTemplateDesignPatch(threadId, 'submitted', message),
        ));
        return;
      }
      await navigateToWorkflowThread(store, threadId, selectionSnapshot);
    } catch (err) {
      actionState.setError(
        template ? '启动政企模板失败，请重试。' : '启动 AI 设计流程失败，请重试。',
      );
      if (isEnterpriseTemplate) {
        const failed = workflowTemplateDesignPatch('', 'failed', '启动政企模板失败，请重试。');
        setDesignSession?.(enterpriseDesignSessionSnapshot(template, failed));
      } else if (stayOnWorkflow) {
        const failed = workflowTemplateDesignPatch('', 'failed', '启动 AI 设计流程失败，请重试。');
        setDesignSession?.(freeDesignSessionSnapshot(failed));
      }
      throw err;
    } finally {
      actionState.setActioning('');
    }
  }, [actionState, notices, setDesignSession, store, workflowCwd]);
}

function enterpriseDesignSessionSnapshot(template, patch = {}) {
  const values = objectValue(template?.templateValues);
  const outputFormat = textValue(
    values.output_format || template?.selectedOutputFormat || firstEnterpriseOutputType(template),
  ) || 'markdown';
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
    config: {
      ...launchConfig,
      enabledTools: [...DAG_DESIGNER_ENABLED_TOOLS],
      providerNativeSkills: false,
    },
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
    await withWorkflowActionTimeout(
      startTurn(workflowTemplateStartTurnPayload(workflowCwd, threadId, template)),
    );
    const submitted = workflowTemplateDesignPatch(
      threadId,
      'submitted',
      'DAG 设计器已接收，正在评估阶段拆分和可用资源。',
    );
    setDesignSession?.(enterpriseDesignSessionSnapshot(template, submitted));
    return true;
  } catch (err) {
    actionState.setError('发送政企模板需求失败，请重试。');
    const failed = workflowTemplateDesignPatch(threadId, 'failed', '发送政企模板需求失败，请重试。');
    setDesignSession?.(enterpriseDesignSessionSnapshot(template, failed));
    throw err;
  }
}

export { useCreateAndStartTemplateAction, useStartDesignFlowAction };
