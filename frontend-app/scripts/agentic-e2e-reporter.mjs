export function summarizeDiscovery({ flows = [] } = {}) {
  const actions = [];
  flows.forEach((flow, flowIndex) => {
    assertFlow(flow, flowIndex);
    discoveryActions(flow, flowIndex).forEach((action) => actions.push(action));
  });
  const flowRecords = reviewFlowRecords(flows);
  const uniqueActions = summarizeActions(actions);
  return {
    totalFlows: flows.length,
    allowedActions: actions.filter((action) => action.safety === 'allowed').length,
    blockedActions: actions.filter((action) => action.safety === 'blocked').length,
    uniqueAllowedActions: uniqueActions.filter((action) => action.safety === 'allowed').length,
    uniqueBlockedActions: uniqueActions.filter((action) => action.safety === 'blocked').length,
    reviewReadyFlows: flowRecords.filter((flow) => flow.category === 'business-candidate').length,
    stableGoalCandidates: flowRecords.filter((flow) => flow.suggestedGoal).length,
    shellControlFlows: flowRecords.filter((flow) => flow.category === 'shell-control').length,
    contextualFlows: flowRecords.filter((flow) => flow.category === 'contextual-control').length,
  };
}

export function renderDiscoveryMarkdown({ summary, flows = [] } = {}) {
  const computedSummary = summarizeDiscovery({ flows });
  const effectiveSummary = summary ? { ...computedSummary, ...summary } : computedSummary;
  const flowRecords = reviewFlowRecords(flows);
  const stableGoalCandidates = flowRecords.filter((flow) => flow.suggestedGoal);
  const otherBusinessCandidates = flowRecords.filter((flow) => flow.category === 'business-candidate' && !flow.suggestedGoal);
  const filteredControls = flowRecords.filter((flow) => flow.category !== 'business-candidate');
  const actionInventory = summarizeActions(discoveryActionsFromFlows(flows));
  const allowedActions = actionInventory.filter((action) => action.safety === 'allowed');
  const blockedActions = actionInventory.filter((action) => action.safety === 'blocked');
  const lines = [
    '# Agentic E2E Business Flow Discovery',
    '',
    `- Total flows: ${effectiveSummary.totalFlows}`,
    `- Review-ready business flows: ${effectiveSummary.reviewReadyFlows}`,
    `- Stable goal candidates: ${effectiveSummary.stableGoalCandidates}`,
    `- Shell/control flows filtered: ${effectiveSummary.shellControlFlows}`,
    `- Contextual duplicate flows: ${effectiveSummary.contextualFlows}`,
    `- Raw allowed actions: ${effectiveSummary.allowedActions}`,
    `- Raw blocked actions: ${effectiveSummary.blockedActions}`,
    `- Unique allowed actions: ${effectiveSummary.uniqueAllowedActions}`,
    `- Unique blocked actions: ${effectiveSummary.uniqueBlockedActions}`,
    '',
  ];
  renderFlowTable(lines, 'Stable Goal Candidates', stableGoalCandidates, true);
  renderFlowTable(lines, 'Other Business Candidates', otherBusinessCandidates, false);
  renderFlowTable(lines, 'First-Pass Filtered Controls', filteredControls, false);
  renderActionTable(lines, 'Unique Allowed Actions', allowedActions);
  renderActionTable(lines, 'Unique Blocked Actions', blockedActions);
  renderFlowTable(lines, 'Raw Flow Index', flowRecords, true);
  return `${lines.join('\n').trim()}\n`;
}

const STABLE_GOAL_BY_LABEL = Object.freeze([
  { pattern: /^新对话$/u, goal: 'chat-composer' },
  { pattern: /^链路追踪$/u, goal: 'observability-latest-logs' },
  { pattern: /^插件与技能$/u, goal: 'plugins-skills-open' },
  { pattern: /^自动化$/u, goal: 'automation-open' },
  { pattern: /^提示词$/u, goal: 'prompts-open' },
  { pattern: /^共享文件$/u, goal: 'shared-files-open' },
  { pattern: /^记忆中心$/u, goal: 'memory-open' },
  { pattern: /^设置$/u, goal: 'settings-open' },
]);

const SHELL_CONTROL_LABELS = Object.freeze([
  /^切换到\s*English$/iu,
  /^切换到黑夜模式$/u,
  /^折叠侧栏$/u,
  /^调整.*宽度$/u,
  /^关闭工作台$/u,
]);

const CONTEXTUAL_CONTROL_LABELS = Object.freeze([
  /^选择项目\s+.+/u,
  /^新对话\s+.+/u,
]);

function renderFlowTable(lines, heading, records, includeGoal) {
  lines.push(`## ${heading}`);
  lines.push('');
  if (records.length === 0) {
    lines.push('None.');
    lines.push('');
    return;
  }
  const goalColumn = includeGoal ? ' | Suggested Goal' : '';
  const goalSeparator = includeGoal ? '|---' : '';
  lines.push(`| Entry | Category | Source | Target Route | Observed Page | Result${goalColumn} |`);
  lines.push(`|---|---|---|---|---|---${goalSeparator}|`);
  for (const record of records) {
    const goal = includeGoal ? ` | ${escapeCell(record.suggestedGoal || '')}` : '';
    lines.push(`| ${escapeCell(record.label)} | ${escapeCell(record.category)} | ${escapeCell(record.source)} | ${escapeCell(record.targetRoute)} | ${escapeCell(record.pageRoute)} | ${escapeCell(record.result)}${goal} |`);
  }
  lines.push('');
}

function renderActionTable(lines, heading, actions) {
  lines.push(`## ${heading}`);
  lines.push('');
  if (actions.length === 0) {
    lines.push('None.');
    lines.push('');
    return;
  }
  lines.push('| Type | Label | Count | Reason |');
  lines.push('|---|---|---:|---|');
  for (const action of actions) {
    lines.push(`| ${escapeCell(action.type)} | ${escapeCell(action.label)} | ${action.count} | ${escapeCell(action.reason)} |`);
  }
  lines.push('');
}

function escapeCell(value) {
  return String(value ?? '').replace(/\|/g, '\\|').replace(/\n+/g, ' ').trim();
}

function reviewFlowRecords(flows = []) {
  return flows.map((flow, index) => {
    assertFlow(flow, index);
    const label = normalizeString(flow.entry?.label || flow.id || `index ${index}`);
    const category = reviewCategoryForLabel(label);
    return {
      id: normalizeString(flow.id || `index-${index}`),
      label,
      category,
      source: normalizeString(flow.entry?.source || 'unknown'),
      entryRoute: normalizeString(flow.entry?.route || '/'),
      targetRoute: normalizeString(flow.entry?.targetRoute || ''),
      pageRoute: normalizeString(flow.page?.route || '/'),
      result: `${normalizeString(flow.result?.status || 'candidate')} - ${normalizeString(flow.result?.summary || 'No summary')}`,
      suggestedGoal: category === 'business-candidate' ? suggestedGoalForLabel(label) : '',
    };
  });
}

function reviewCategoryForLabel(label) {
  if (SHELL_CONTROL_LABELS.some((pattern) => pattern.test(label))) return 'shell-control';
  if (CONTEXTUAL_CONTROL_LABELS.some((pattern) => pattern.test(label))) return 'contextual-control';
  return 'business-candidate';
}

function suggestedGoalForLabel(label) {
  return STABLE_GOAL_BY_LABEL.find((candidate) => candidate.pattern.test(label))?.goal || '';
}

function discoveryActionsFromFlows(flows = []) {
  return flows.flatMap((flow, flowIndex) => discoveryActions(flow, flowIndex));
}

function summarizeActions(actions = []) {
  const byKey = new Map();
  for (const action of actions) {
    const label = canonicalActionLabel(action.label);
    const key = `${action.safety}|${action.type}|${label}|${action.reason}`;
    const current = byKey.get(key);
    if (current) {
      current.count += 1;
      continue;
    }
    byKey.set(key, {
      safety: action.safety,
      type: action.type,
      label,
      reason: action.reason,
      count: 1,
    });
  }
  return Array.from(byKey.values()).sort(compareActionSummary);
}

function canonicalActionLabel(label) {
  return normalizeString(label).replace(/\b[0-9a-f]{24,}\b/giu, '<id>');
}

function compareActionSummary(left, right) {
  if (left.safety !== right.safety) return left.safety.localeCompare(right.safety);
  if (right.count !== left.count) return right.count - left.count;
  return left.label.localeCompare(right.label, 'zh-Hans-CN');
}

function discoveryActions(flow, flowIndex) {
  const actions = flow.actions || [];
  if (!Array.isArray(actions)) {
    throw new Error(`invalid discovery actions for flow ${flowID(flow, flowIndex)}: expected array`);
  }
  actions.forEach((action, actionIndex) => assertAction(action, flowID(flow, flowIndex), actionIndex));
  return actions;
}

function assertFlow(flow, index) {
  if (!isRecord(flow)) {
    throw new Error(`invalid discovery flow at index ${index}: expected object`);
  }
}

function assertAction(action, flowIDValue, index) {
  if (!isRecord(action)) {
    throw new Error(`invalid discovery action at flow ${flowIDValue} index ${index}: expected object`);
  }
}

function flowID(flow, index) {
  return flow.id ? String(flow.id) : `index ${index}`;
}

function isRecord(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function normalizeString(value) {
  return String(value ?? '').trim();
}
