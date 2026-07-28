const WAITING_AGENT_STATUSES = new Set(['idle', 'turn_queued', 'awaiting_user_input']);
const FAILED_AGENT_STATUSES = new Set(['failed', 'stopped']);

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
    if (agent.outcome?.kind === 'success') {
      counts.completed += 1;
    } else if (agent.outcome || FAILED_AGENT_STATUSES.has(agent.progress.status)) {
      counts.failed += 1;
    } else if (WAITING_AGENT_STATUSES.has(agent.progress.status)) {
      counts.waiting += 1;
    } else {
      counts.running += 1;
    }
  }
  return counts;
}

function selectAgentBoardViewModel(state, options) {
  if (!options || (options.mode !== 'floating' && options.mode !== 'docked')) {
    throw new TypeError('agent board mode must be floating or docked');
  }
  if (typeof options.selectedAgentId !== 'string') throw new TypeError('selectedAgentId must be a string');
  if (typeof options.loading !== 'boolean') throw new TypeError('loading must be a boolean');
  if (options.error !== null && typeof options.error !== 'string') throw new TypeError('error must be a string or null');
  if (!Array.isArray(state.agents)) throw new TypeError('client store agents must be an array');
  const agents = stableHierarchy(state.agents);
  const structuredRootAgent = agents.find((agent) => !agent.parentAgentId);
  const structuredRoot = structuredRootAgent === undefined ? '' : structuredRootAgent.id;
  const rootAgentId = agents.some((agent) => agent.id === state.mainAgentId)
    ? state.mainAgentId
    : structuredRoot;
  return {
    mode: options.mode,
    rootAgentId,
    selectedAgentId: options.selectedAgentId,
    counts: countAgentStates(agents),
    agents,
    loading: options.loading,
    error: options.error,
  };
}

export {
  countAgentStates,
  mergeAgentBoardPatch,
  selectAgentBoardViewModel,
  stableHierarchy,
};
