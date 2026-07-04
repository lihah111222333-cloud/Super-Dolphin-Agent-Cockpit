export const DEFAULT_AGENTIC_GOAL = Object.freeze({
  id: 'frontend_navigation_probe',
  composerText: 'Agentic E2E feasibility probe',
});

export function normalizeGoal(goal = {}) {
  const id = normalizeString(goal.id) || DEFAULT_AGENTIC_GOAL.id;
  const composerText = normalizeString(goal.composerText) || DEFAULT_AGENTIC_GOAL.composerText;
  return { id, composerText };
}

export function decideNextAction(rawFacts = {}, rawGoal = DEFAULT_AGENTIC_GOAL) {
  const facts = normalizeFacts(rawFacts);
  const goal = normalizeGoal(rawGoal);

  if (!facts.url || facts.url === 'about:blank') {
    return action('goto', { path: '/', reason: 'open app root before reading UI structure' });
  }
  if (!facts.hasFrontendApp) {
    return action('fail', { reason: 'frontend app root is not visible' });
  }
  if (facts.consoleErrors.length > 0) {
    return action('fail', { reason: `console errors detected: ${facts.consoleErrors.join('; ')}` });
  }
  if (facts.observabilityPageVisible) {
    if (!facts.recentLogsVisible) {
      return action('click', {
        target: { type: 'role', role: 'button', name: '查询最新日志' },
        reason: 'trigger an observability read path without starting a provider turn',
      });
    }
    return action('done', { reason: 'agentic frontend probe reached all success conditions' });
  }
  if (!facts.hasChatPage) {
    return action('click', {
      target: { type: 'role', role: 'button', name: '新对话', exact: true },
      reason: 'navigate back to the chat workbench',
    });
  }
  if (!facts.composerVisible) {
    return action('fail', { reason: 'composer input is not visible on chat page' });
  }
  if (facts.composerValue !== goal.composerText) {
    return action('fill', {
      target: { type: 'testId', value: 'composer-input' },
      value: goal.composerText,
      reason: 'prove the agent can identify and fill the composer from page structure',
    });
  }
  if (!facts.observabilityPageVisible) {
    return action('click', {
      target: {
        type: 'nestedRole',
        parentTestId: 'sidebar-secondary-nav',
        role: 'button',
        name: '链路追踪',
      },
      reason: 'navigate to observability using the discovered sidebar structure',
    });
  }
  return action('fail', { reason: 'planner reached an impossible frontend probe state' });
}

export function normalizeFacts(facts = {}) {
  return {
    url: normalizeString(facts.url),
    hasFrontendApp: Boolean(facts.hasFrontendApp),
    hasChatPage: Boolean(facts.hasChatPage),
    composerVisible: Boolean(facts.composerVisible),
    composerValue: normalizeString(facts.composerValue),
    chatActionsMenuVisible: Boolean(facts.chatActionsMenuVisible),
    runtimePanelVisible: Boolean(facts.runtimePanelVisible),
    observabilityPageVisible: Boolean(facts.observabilityPageVisible),
    recentLogsVisible: Boolean(facts.recentLogsVisible),
    consoleErrors: Array.isArray(facts.consoleErrors)
      ? facts.consoleErrors.map((item) => normalizeString(item)).filter(Boolean)
      : [],
  };
}

function action(type, fields = {}) {
  return { type, ...fields };
}

function normalizeString(value) {
  return String(value ?? '').trim();
}
