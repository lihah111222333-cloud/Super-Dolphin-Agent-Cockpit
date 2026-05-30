import { parseMultiAgentIntent } from '../utils/multi-agent-intent.js';
import { buildMultiAgentPlan, formatMultiAgentLaunchSummary } from '../utils/multi-agent-plan.js';
import { logInfo, logWarn } from '../services/log.js';

const SUMMARY_AGENT_WAIT_NOTICE = '系统已为你创建汇总 Agent，并已发送汇总任务。它会先产出初版汇总；如果后续需要，可继续把其他子 Agent 的完整结果发给它迭代。';

function randomGroupId() {
  const randomUUID = globalThis.crypto?.randomUUID;
  if (typeof randomUUID === 'function') return `mag_${randomUUID.call(globalThis.crypto)}`;
  return `mag_${Date.now().toString(36)}`;
}

function selectedThreadValue(selectedThreadId) {
  return (selectedThreadId?.value || '').toString().trim();
}

function setSelectedThreadValue(selectedThreadId, value) {
  if (selectedThreadId && typeof selectedThreadId === 'object' && 'value' in selectedThreadId) {
    selectedThreadId.value = (value || '').toString().trim();
  }
}

function normalizeAttachments(attachments) {
  return Array.isArray(attachments) ? attachments.slice() : [];
}

function buildStartOptions(agent, groupId, parentThreadId, providerOptions = {}) {
  return {
    name: agent.name,
    prompt: agent.prompt,
    baseInstructions: agent.baseInstructions,
    deferSpawn: true,
    skipSaveActive: true,
    skipInitialRuntimeSync: true,
    parentAgentId: parentThreadId,
    config: {
      ...(providerOptions.config || {}),
      multiAgentGroupId: groupId,
      parentThreadId,
      parentAgentId: parentThreadId,
      agentType: 'multi-agent-child',
      roleKey: agent.roleKey,
      agentIndex: agent.index + 1,
    },
  };
}

function buildParentStartOptions(plan, intent, attachments, groupId) {
  return {
    name: plan.groupTitle,
    deferSpawn: true,
    focusMode: 'chat',
    skipInitialRuntimeSync: true,
    optimisticUserMessage: {
      text: (intent?.task || '').toString(),
      attachments: normalizeAttachments(attachments),
    },
    config: {
      multiAgentGroupId: groupId,
      agentType: 'multi-agent-parent',
    },
  };
}

async function ensureParentThread({ threadStore, cwd, selectedThreadId, plan, intent, attachments, groupId }) {
  const existing = selectedThreadValue(selectedThreadId);
  if (existing) return existing;
  const threadId = await threadStore.startThread(cwd, buildParentStartOptions(plan, intent, attachments, groupId));
  if (!threadId) throw new Error('主对话创建失败：无法拉起子 Agent。');
  setSelectedThreadValue(selectedThreadId, threadId);
  return threadId;
}

async function launchChildAgent({ threadStore, cwd, agent, groupId, parentThreadId, attachments }) {
  const threadId = await threadStore.startThread(cwd, buildStartOptions(agent, groupId, parentThreadId));
  if (!threadId) throw new Error(`子 Agent 创建失败：${agent.name}`);
  if (agent.autoStart) {
    await threadStore.sendMessage(threadId, agent.prompt, attachments, { cwd, kickoff: true });
  }
  return { agent, threadId };
}

function appendLocalSummary(threadStore, parentThreadId, summary) {
  const id = (parentThreadId || '').toString().trim();
  const text = (summary || '').toString().trim();
  if (!id || !text || !threadStore?.state) return;
  const existing = Array.isArray(threadStore.state.timelinesByThread?.[id]) ? threadStore.state.timelinesByThread[id] : [];
  const item = Object.freeze({
    id: `${id}-multi-agent-summary-${Date.now()}`,
    kind: 'assistant',
    text,
    ts: new Date().toISOString(),
  });
  threadStore.state.timelinesByThread = {
    ...(threadStore.state.timelinesByThread || {}),
    [id]: [...existing, item],
  };
}

export function useMultiAgentLaunch({ threadStore, projectStore, selectedThreadId, composer, resolveCwd, scheduleScrollToBottom }) {
  async function maybeLaunchFromComposer() {
    const text = (composer?.state?.text || '').toString();
    const attachments = normalizeAttachments(composer?.state?.attachments);
    const intent = parseMultiAgentIntent(text);
    if (!intent.enabled) return false;
    const cwd = resolveCwd(projectStore);
    const plan = buildMultiAgentPlan(intent);
    const groupId = randomGroupId();
    const parentThreadId = await ensureParentThread({ threadStore, cwd, selectedThreadId, plan, intent, attachments, groupId });
    logInfo('multi-agent', 'launch.start', {
      group_id: groupId,
      parent_thread_id: parentThreadId,
      agent_count: plan.agents.length,
      task_kind: plan.taskKind,
    });

    if (typeof composer.clearComposer === 'function') composer.clearComposer();

    const launched = [];
    const previousSelection = parentThreadId;
    try {
      for (const agent of plan.agents) {
        const result = await launchChildAgent({ threadStore, cwd, agent, groupId, parentThreadId, attachments });
        launched.push(result);
      }
      setSelectedThreadValue(selectedThreadId, previousSelection);
      if (typeof threadStore.refreshSidebarState === 'function') {
        await threadStore.refreshSidebarState().catch((error) => {
          logWarn('multi-agent', 'launch.refresh_sidebar_failed', { error });
        });
      }
      appendLocalSummary(threadStore, parentThreadId, formatMultiAgentLaunchSummary(plan, launched));
      if (typeof scheduleScrollToBottom === 'function') scheduleScrollToBottom(true);
      logInfo('multi-agent', 'launch.done', {
        group_id: groupId,
        parent_thread_id: parentThreadId,
        launched_count: launched.length,
      });
      return true;
    } catch (error) {
      setSelectedThreadValue(selectedThreadId, previousSelection);
      logWarn('multi-agent', 'launch.failed', {
        group_id: groupId,
        parent_thread_id: parentThreadId,
        launched_count: launched.length,
        error,
      });
      throw error;
    }
  }

  return { maybeLaunchFromComposer };
}
