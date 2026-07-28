/*
 * Agent 看板展示层模型。
 * 只把 selector 输出的稳定 view model 映射为展示用标签、层级缩进和悬浮卡片重点条目；
 * 计数、排序和层级顺序一律以 selector 输出为准，这里不重复实现。
 */

const WAITING_DISPLAY_STATUSES = new Set(['idle', 'turn_queued', 'awaiting_user_input']);
const FAILED_DISPLAY_STATUSES = new Set(['failed', 'stopped']);

const CATEGORY_PRIORITY = Object.freeze({
  failed: 0,
  running: 1,
  waiting: 2,
  completed: 3,
});

const FLOATING_KEY_AGENT_LIMIT = 3;
const MAX_AGENT_DEPTH = 32;

function agentDisplayCategory(agent) {
  if (agent.outcome?.kind === 'success') return 'completed';
  if (agent.outcome || FAILED_DISPLAY_STATUSES.has(agent.progress.status)) return 'failed';
  if (WAITING_DISPLAY_STATUSES.has(agent.progress.status)) return 'waiting';
  return 'running';
}

function agentStatusLabel(agent) {
  const category = agentDisplayCategory(agent);
  if (agent.outcome?.kind === 'success') return { category, text: '已完成' };
  if (agent.outcome?.kind === 'failure') return { category, text: '失败' };
  if (agent.outcome?.kind === 'stopped') return { category, text: '已停止' };
  if (category === 'failed') return { category, text: agent.progress.status === 'stopped' ? '已停止' : '失败' };
  if (category === 'waiting') return { category, text: '等待中' };
  return { category, text: '运行中' };
}

function agentDepth(agent, agentsById) {
  let depth = 0;
  let current = agent;
  while (current.parentAgentId && agentsById.has(current.parentAgentId) && depth < MAX_AGENT_DEPTH) {
    current = agentsById.get(current.parentAgentId);
    depth += 1;
  }
  return depth;
}

function keyAgentsForFloating(agents, limit = FLOATING_KEY_AGENT_LIMIT) {
  const prioritized = [...agents].sort(
    (left, right) => CATEGORY_PRIORITY[agentDisplayCategory(left)] - CATEGORY_PRIORITY[agentDisplayCategory(right)],
  );
  return prioritized.slice(0, limit);
}

function resolveSelectedAgentId(viewModel) {
  const agents = viewModel.agents;
  if (viewModel.selectedAgentId && agents.some((agent) => agent.id === viewModel.selectedAgentId)) {
    return viewModel.selectedAgentId;
  }
  if (viewModel.rootAgentId && agents.some((agent) => agent.id === viewModel.rootAgentId)) {
    return viewModel.rootAgentId;
  }
  return agents.length > 0 ? agents[0].id : '';
}

function hasOnlyRootAgent(viewModel) {
  return viewModel.agents.length === 1 && viewModel.agents[0].id === viewModel.rootAgentId;
}

export {
  FLOATING_KEY_AGENT_LIMIT,
  agentDepth,
  agentDisplayCategory,
  agentStatusLabel,
  hasOnlyRootAgent,
  keyAgentsForFloating,
  resolveSelectedAgentId,
};
