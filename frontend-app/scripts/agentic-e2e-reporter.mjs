export function summarizeDiscovery({ flows = [] } = {}) {
  const actions = [];
  flows.forEach((flow, flowIndex) => {
    assertFlow(flow, flowIndex);
    discoveryActions(flow, flowIndex).forEach((action) => actions.push(action));
  });
  return {
    totalFlows: flows.length,
    allowedActions: actions.filter((action) => action.safety === 'allowed').length,
    blockedActions: actions.filter((action) => action.safety === 'blocked').length,
  };
}

export function renderDiscoveryMarkdown({ summary = summarizeDiscovery(), flows = [] } = {}) {
  const lines = [
    '# Agentic E2E Business Flow Discovery',
    '',
    `- Total flows: ${summary.totalFlows}`,
    `- Allowed actions: ${summary.allowedActions}`,
    `- Blocked actions: ${summary.blockedActions}`,
    '',
  ];
  for (const [flowIndex, flow] of flows.entries()) {
    assertFlow(flow, flowIndex);
    lines.push(`## ${flow.entry?.label || flow.id}`);
    lines.push('');
    lines.push(`- ID: \`${flow.id}\``);
    lines.push(`- Entry: ${flow.entry?.source || 'unknown'} from \`${flow.entry?.route || '/'}\``);
    lines.push(`- Page: \`${flow.page?.route || '/'}\`${flow.page?.heading ? `, heading "${flow.page.heading}"` : ''}`);
    lines.push(`- Result: ${flow.result?.status || 'candidate'} - ${flow.result?.summary || 'No summary'}`);
    lines.push('');
    lines.push('| Safety | Type | Label | Reason |');
    lines.push('|---|---|---|---|');
    for (const action of discoveryActions(flow, flowIndex)) {
      lines.push(`| ${escapeCell(action.safety)} | ${escapeCell(action.type)} | ${escapeCell(action.label)} | ${escapeCell(action.reason)} |`);
    }
    lines.push('');
  }
  return `${lines.join('\n').trim()}\n`;
}

function escapeCell(value) {
  return String(value ?? '').replace(/\|/g, '\\|').replace(/\n+/g, ' ').trim();
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
