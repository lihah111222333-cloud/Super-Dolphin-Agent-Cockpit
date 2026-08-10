const WAITING_AGENT_STATUSES = new Set(['idle', 'turn_queued', 'awaiting_user_input']);
const FAILED_AGENT_STATUSES = new Set(['failed', 'stopped']);

function agentStatusView(agent) {
  if (agent.outcome?.kind === 'success') return { category: 'completed', text: '已完成' };
  if (agent.outcome?.kind === 'failure') return { category: 'failed', text: '失败' };
  if (agent.outcome?.kind === 'stopped') return { category: 'failed', text: '已停止' };
  if (FAILED_AGENT_STATUSES.has(agent.progress.status)) {
    return { category: 'failed', text: agent.progress.status === 'stopped' ? '已停止' : '失败' };
  }
  if (WAITING_AGENT_STATUSES.has(agent.progress.status)) return { category: 'waiting', text: '等待中' };
  return { category: 'running', text: '运行中' };
}

function compareAgents(left, right) {
  const assignedAtOrder = left.assignment.assignedAt.localeCompare(right.assignment.assignedAt);
  if (assignedAtOrder !== 0) return assignedAtOrder;
  return left.id.localeCompare(right.id);
}

function stableHierarchy(agents) {
  const byId = new Map();
  for (const agent of agents) {
    if (byId.has(agent.id)) throw new Error(`duplicate agent id: ${agent.id}`);
    byId.set(agent.id, agent);
  }
  const childrenByParent = new Map();
  const roots = [];
  for (const agent of agents) {
    if (!agent.parentAgentId || !byId.has(agent.parentAgentId)) {
      roots.push(agent);
      continue;
    }
    let siblings = childrenByParent.get(agent.parentAgentId);
    if (siblings === undefined) siblings = [];
    siblings.push(agent);
    childrenByParent.set(agent.parentAgentId, siblings);
  }
  roots.sort(compareAgents);
  for (const children of childrenByParent.values()) children.sort(compareAgents);
  const ordered = [];
  const visited = new Set();
  const visiting = new Set();
  const visit = (agent) => {
    if (visiting.has(agent.id)) throw new Error(`agent parent cycle: ${agent.id}`);
    if (visited.has(agent.id)) return;
    visiting.add(agent.id);
    ordered.push(agent);
    const children = childrenByParent.get(agent.id);
    if (children !== undefined) {
      for (const child of children) visit(child);
    }
    visiting.delete(agent.id);
    visited.add(agent.id);
  };
  for (const root of roots) visit(root);
  for (const agent of [...agents].sort(compareAgents)) visit(agent);
  return ordered;
}

function mergeAgentBoardPatch(agents, operation) {
  if (!operation) return agents;
  if (operation.kind === 'remove') {
    return agents.filter((agent) => agent.threadId !== operation.threadId);
  }
  const index = agents.findIndex((agent) => agent.id === operation.agent.id);
  if (index < 0) return [...agents, operation.agent];
  return agents.map((agent, agentIndex) => (agentIndex === index ? operation.agent : agent));
}

function countAgentStates(agents) {
  const counts = { running: 0, waiting: 0, completed: 0, failed: 0 };
  for (const agent of agents) {
    counts[agentStatusView(agent).category] += 1;
  }
  return counts;
}

function agentsForActiveThread(agents, activeThreadId, threads) {
  if (typeof activeThreadId !== 'string' || !activeThreadId) return agents;
  if (!Array.isArray(threads)) throw new TypeError('client store threads must be an array');
  const identities = new Set([activeThreadId]);
  const identityFields = ['id', 'agentId', 'providerThreadId', 'sessionId'];
  for (const thread of threads) {
    const values = identityFields.map((field) => thread?.[field]).filter((value) => typeof value === 'string' && value);
    if (values.some((value) => identities.has(value))) values.forEach((value) => identities.add(value));
  }
  const byId = new Map(agents.map((agent) => [agent.id, agent]));
  const rootIds = new Set();
  for (const agent of agents) {
    if (!identities.has(agent.id) && !identities.has(agent.threadId)) continue;
    let root = agent;
    const visited = new Set();
    while (root.parentAgentId && byId.has(root.parentAgentId)) {
      if (visited.has(root.id)) throw new Error(`agent parent cycle: ${root.id}`);
      visited.add(root.id);
      root = byId.get(root.parentAgentId);
    }
    rootIds.add(root.id);
  }
  if (rootIds.size === 0) return [];
  const included = new Set(rootIds);
  let changed = true;
  while (changed) {
    changed = false;
    for (const agent of agents) {
      if (!included.has(agent.id) && included.has(agent.parentAgentId)) {
        included.add(agent.id);
        changed = true;
      }
    }
  }
  return agents.filter((agent) => included.has(agent.id));
}

function selectAgentBoardViewModel(state, options) {
  if (!options || (options.mode !== 'floating' && options.mode !== 'docked')) {
    throw new TypeError('agent board mode must be floating or docked');
  }
  if (typeof options.selectedAgentId !== 'string') throw new TypeError('selectedAgentId must be a string');
  if (typeof options.loading !== 'boolean') throw new TypeError('loading must be a boolean');
  if (options.error !== null && typeof options.error !== 'string') throw new TypeError('error must be a string or null');
  if (!Array.isArray(state.agents)) throw new TypeError('client store agents must be an array');
  const scopedAgents = agentsForActiveThread(state.agents, options.activeThreadId, options.threads);
  const orderedAgents = stableHierarchy(scopedAgents);
  const agents = orderedAgents.map((agent) => ({ ...agent, statusView: agentStatusView(agent) }));
  const structuredRootAgent = agents.find((agent) => !agent.parentAgentId);
  const structuredRoot = structuredRootAgent === undefined ? '' : structuredRootAgent.id;
  const rootAgentId = agents.some((agent) => agent.id === state.mainAgentId)
    ? state.mainAgentId
    : structuredRoot;
  return {
    mode: options.mode,
    rootAgentId,
    selectedAgentId: options.selectedAgentId,
    counts: countAgentStates(orderedAgents),
    agents,
    loading: options.loading,
    error: options.error,
  };
}

export {
  agentsForActiveThread,
  countAgentStates,
  mergeAgentBoardPatch,
  selectAgentBoardViewModel,
  stableHierarchy,
};
