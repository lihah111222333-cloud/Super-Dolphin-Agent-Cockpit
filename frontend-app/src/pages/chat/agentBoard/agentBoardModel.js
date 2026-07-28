/*
 * Agent 看板展示层模型。
 * 只把 selector 输出的稳定 view model 映射为层级缩进和选中项；
 * 计数、排序和层级顺序一律以 selector 输出为准，这里不重复实现。
 */

const MAX_AGENT_DEPTH = 32;

function agentDepth(agent, agentsById) {
  let depth = 0;
  let current = agent;
  while (current.parentAgentId && agentsById.has(current.parentAgentId) && depth < MAX_AGENT_DEPTH) {
    current = agentsById.get(current.parentAgentId);
    depth += 1;
  }
  return depth;
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
  agentDepth,
  hasOnlyRootAgent,
  resolveSelectedAgentId,
};
