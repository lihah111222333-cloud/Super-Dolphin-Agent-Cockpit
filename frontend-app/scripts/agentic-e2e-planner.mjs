import {
  DEFAULT_AGENTIC_GOAL,
  normalizeGoal as normalizeAgenticGoal,
} from './agentic-e2e-goals.mjs';

export {
  AGENTIC_GOAL_IDS,
  DEFAULT_AGENTIC_GOAL,
  normalizeGoal,
} from './agentic-e2e-goals.mjs';

export function decideNextAction(rawFacts = {}, rawGoal = DEFAULT_AGENTIC_GOAL) {
  const facts = normalizeFacts(rawFacts);
  const goal = normalizeAgenticGoal(rawGoal);

  if (!facts.url || facts.url === 'about:blank') {
    return action('goto', { path: '/', reason: 'open app root before reading UI structure' });
  }
  if (!facts.hasFrontendApp) {
    return action('fail', { reason: 'frontend app root is not visible' });
  }
  if (facts.consoleErrors.length > 0) {
    return action('fail', { reason: `console errors detected: ${facts.consoleErrors.join('; ')}` });
  }
  if (goal.kind === 'chat-composer') return decideChatComposer(facts, goal);
  if (goal.kind === 'chat-send-mocked') return decideChatSendMocked(facts, goal);
  if (goal.kind === 'project-add-sandbox') return decideProjectAddSandbox(facts, goal);
  if (goal.kind === 'file-attach-sandbox') return decideFileAttachSandbox(facts, goal);
  if (goal.kind === 'settings-video-key-save-mocked') return decideSettingsVideoKeySaveMocked(facts, goal);
  if (goal.kind === 'settings-provider-save-mocked') return decideSettingsProviderSaveMocked(facts, goal);
  if (goal.kind === 'observability-latest-logs') return decideObservabilityLatestLogs(facts, goal);
  if (goal.kind === 'open-route') return decideOpenRouteGoal(facts, goal);
  if (goal.kind !== 'frontend-navigation-probe') {
    return action('fail', { reason: `unsupported normalized agentic e2e goal kind: ${goal.kind}` });
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
    return action('goto', {
      path: '/',
      expectRoute: '/',
      reason: 'navigate back to the chat workbench without invoking new conversation',
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

function decideChatComposer(facts, goal) {
  if (!routeMatches(facts.url, goal.targetRoute)) {
    return action('goto', {
      path: goal.targetRoute,
      expectRoute: goal.targetRoute,
      reason: `open ${goal.id} target route`,
    });
  }
  if (!facts.hasChatPage) {
    return action('fail', { reason: 'composer goal target route did not expose the chat page' });
  }
  if (!facts.composerVisible) {
    return action('fail', { reason: 'composer input is not visible on chat page' });
  }
  if (facts.composerValue !== goal.composerText) {
    return action('fill', {
      target: { type: 'testId', value: 'composer-input' },
      value: goal.composerText,
      reason: `fill composer for ${goal.id}`,
    });
  }
  return action('done', { reason: `${goal.id} reached composer-filled state` });
}

function decideChatSendMocked(facts, goal) {
  if (hasObservedRPCs(facts, goal.requiredRPCs)) {
    return action('done', { reason: `${goal.id} observed ${goal.requiredRPCs.join(' -> ')}` });
  }
  const routeAction = ensureChatRoute(facts, goal);
  if (routeAction) return routeAction;
  if (!facts.composerVisible) {
    return action('fail', { reason: 'chat send goal target route did not expose the composer input' });
  }
  if (facts.composerValue !== goal.composerText) {
    return action('fill', {
      target: { type: 'testId', value: 'composer-input' },
      value: goal.composerText,
      reason: `fill composer before ${goal.id}`,
    });
  }
  return action('click', {
    target: goal.sendTarget,
    reason: 'click the real send button while strict Wails mock prevents provider launch',
  });
}

function decideProjectAddSandbox(facts, goal) {
  if (hasObservedRPCs(facts, goal.requiredRPCs)) {
    return action('done', { reason: `${goal.id} observed sandbox project selection and activation` });
  }
  const routeAction = ensureChatRoute(facts, goal);
  if (routeAction) return routeAction;
  return action('click', {
    target: goal.addProjectTarget,
    reason: 'click the real sidebar add-project button while mock selects the sandbox project',
  });
}

function decideFileAttachSandbox(facts, goal) {
  if (hasObservedRPCs(facts, goal.requiredRPCs) && facts.attachmentCount > 0) {
    return action('done', { reason: `${goal.id} observed sandbox file picker and attachment UI` });
  }
  const routeAction = ensureChatRoute(facts, goal);
  if (routeAction) return routeAction;
  return action('click', {
    target: goal.addFileTarget,
    reason: 'click the real add-file button while mock returns a sandbox file',
  });
}

function decideSettingsVideoKeySaveMocked(facts, goal) {
  if (hasObservedRPCs(facts, goal.requiredRPCs)) {
    return action('done', { reason: `${goal.id} observed mocked settings save RPC` });
  }
  if (!routeMatches(facts.url, goal.targetRoute) || !facts.settingsPageVisible) {
    return action('click', {
      target: goal.navigationTarget,
      expectRoute: goal.targetRoute,
      reason: `open ${goal.id} target route`,
    });
  }
  if (!facts.settingsApiKeyVisible) {
    return action('fail', { reason: 'settings video key input is not visible' });
  }
  if (facts.settingsApiKeyValue !== goal.settingsValue) {
    return action('fill', {
      target: goal.inputTarget,
      value: goal.settingsValue,
      reason: 'fill the settings video API key field with a harmless mock value',
    });
  }
  return action('click', {
    target: goal.saveTarget,
    reason: 'click the real settings save button while mock records the write',
  });
}

function decideSettingsProviderSaveMocked(facts, goal) {
  if (hasObservedRPCs(facts, goal.requiredRPCs)) {
    return action('done', { reason: `${goal.id} observed mocked provider preferences save RPC` });
  }
  if (!routeMatches(facts.url, goal.targetRoute) || !facts.settingsPageVisible) {
    return action('click', {
      target: goal.navigationTarget,
      expectRoute: goal.targetRoute,
      reason: `open ${goal.id} target route`,
    });
  }
  if (!facts.settingsProviderSaveVisible) {
    return action('fail', { reason: 'Provider settings save button is not visible' });
  }
  if (facts.settingsProviderModelValue !== goal.modelValue) {
    return action('select', {
      target: goal.modelTarget,
      value: goal.modelValue,
      reason: `select model before ${goal.id}`,
    });
  }
  if (facts.settingsProviderEffortValue !== goal.effortValue) {
    return action('select', {
      target: goal.effortTarget,
      value: goal.effortValue,
      reason: `select effort before ${goal.id}`,
    });
  }
  if (facts.settingsProviderPersonalityValue !== goal.personalityValue) {
    return action('select', {
      target: goal.personalityTarget,
      value: goal.personalityValue,
      reason: `select personality before ${goal.id}`,
    });
  }
  if (facts.settingsProviderCodexHomeValue !== goal.codexHomeValue) {
    return action('fill', {
      target: goal.codexHomeTarget,
      value: goal.codexHomeValue,
      reason: `fill Codex home before ${goal.id}`,
    });
  }
  if (facts.settingsProviderInstanceKeyValue !== goal.instanceKeyValue) {
    return action('fill', {
      target: goal.instanceKeyTarget,
      value: goal.instanceKeyValue,
      reason: `fill provider instance key before ${goal.id}`,
    });
  }
  if (facts.settingsProviderWritableRootsValue !== goal.writableRootsValue) {
    return action('fill', {
      target: goal.writableRootsTarget,
      value: goal.writableRootsValue,
      reason: `fill writable roots before ${goal.id}`,
    });
  }
  return action('click', {
    target: goal.saveTarget,
    reason: `click the real provider settings save button for ${goal.id}`,
  });
}

function decideObservabilityLatestLogs(facts, goal) {
  if (!routeMatches(facts.url, goal.targetRoute) || !facts.observabilityPageVisible) {
    return action('click', {
      target: goal.navigationTarget,
      expectRoute: goal.targetRoute,
      reason: `open ${goal.id} target route`,
    });
  }
  if (!facts.recentLogsVisible) {
    return action('click', {
      target: goal.queryTarget,
      reason: 'trigger an observability read path without starting a provider turn',
    });
  }
  return action('done', { reason: `${goal.id} reached latest logs state` });
}

function decideOpenRouteGoal(facts, goal) {
  if (routeMatches(facts.url, goal.targetRoute)) {
    return action('done', { reason: `${goal.id} reached ${goal.targetRoute}` });
  }
  return action('click', {
    target: goal.navigationTarget,
    expectRoute: goal.targetRoute,
    reason: `open ${goal.id} target route`,
  });
}

function ensureChatRoute(facts, goal) {
  if (!routeMatches(facts.url, goal.targetRoute)) {
    return action('goto', {
      path: goal.targetRoute,
      expectRoute: goal.targetRoute,
      reason: `open ${goal.id} target route`,
    });
  }
  if (!facts.hasChatPage) {
    return action('fail', { reason: `${goal.id} target route did not expose the chat page` });
  }
  return null;
}

export function normalizeFacts(facts = {}) {
  return {
    url: normalizeString(facts.url),
    hasFrontendApp: Boolean(facts.hasFrontendApp),
    hasChatPage: Boolean(facts.hasChatPage),
    composerVisible: Boolean(facts.composerVisible),
    composerValue: normalizeString(facts.composerValue),
    chatActionsMenuVisible: Boolean(facts.chatActionsMenuVisible),
    projectMenuVisible: Boolean(facts.projectMenuVisible),
    attachmentCount: nonNegativeInt(facts.attachmentCount),
    runtimePanelVisible: Boolean(facts.runtimePanelVisible),
    observabilityPageVisible: Boolean(facts.observabilityPageVisible),
    recentLogsVisible: Boolean(facts.recentLogsVisible),
    settingsPageVisible: Boolean(facts.settingsPageVisible),
    settingsApiKeyVisible: Boolean(facts.settingsApiKeyVisible),
    settingsApiKeyValue: normalizeString(facts.settingsApiKeyValue),
    settingsVideoNoticeVisible: Boolean(facts.settingsVideoNoticeVisible),
    settingsProviderSaveVisible: Boolean(facts.settingsProviderSaveVisible),
    settingsProviderModelValue: normalizeString(facts.settingsProviderModelValue),
    settingsProviderEffortValue: normalizeString(facts.settingsProviderEffortValue),
    settingsProviderPersonalityValue: normalizeString(facts.settingsProviderPersonalityValue),
    settingsProviderCodexHomeValue: normalizeString(facts.settingsProviderCodexHomeValue),
    settingsProviderInstanceKeyValue: normalizeString(facts.settingsProviderInstanceKeyValue),
    settingsProviderWritableRootsValue: normalizeString(facts.settingsProviderWritableRootsValue),
    mockWailsCallMethods: Array.isArray(facts.mockWailsCallMethods)
      ? facts.mockWailsCallMethods.map((item) => normalizeString(item)).filter(Boolean)
      : [],
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

function hasObservedRPCs(facts, methods = []) {
  return methods.every((method) => facts.mockWailsCallMethods.includes(method));
}

function nonNegativeInt(value) {
  const parsed = Number(value || 0);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 0;
}

function routeMatches(url, route) {
  return normalizePathname(routeFromURL(url)) === normalizePathname(route);
}

function routeFromURL(value) {
  try {
    return new URL(value).pathname || '/';
  }
  catch {
    return '/';
  }
}

function normalizePathname(value) {
  const normalized = normalizeString(value).replace(/\/+$/g, '');
  return normalized || '/';
}
