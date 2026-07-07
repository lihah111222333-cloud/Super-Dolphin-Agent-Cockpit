export const DEFAULT_AGENTIC_COMPOSER_TEXT = 'Agentic E2E feasibility probe';
export const DEFAULT_AGENTIC_GOAL_ID = 'frontend_navigation_probe';

const sidebarNav = (name) => Object.freeze({
  type: 'nestedRole',
  parentTestId: 'sidebar-nav',
  role: 'button',
  name,
});

const secondarySidebarNav = (name) => Object.freeze({
  type: 'nestedRole',
  parentTestId: 'sidebar-secondary-nav',
  role: 'button',
  name,
});

const appSidebarNav = (name) => Object.freeze({
  type: 'nestedRole',
  parentTestId: 'app-sidebar',
  role: 'button',
  name,
});

const roleTarget = (role, name, exact = false) => Object.freeze({
  type: 'role',
  role,
  name,
  ...(exact ? { exact: true } : {}),
});

const latestLogsAction = Object.freeze({
  type: 'role',
  role: 'button',
  name: '查询最新日志',
});

export const AGENTIC_GOAL_DEFINITIONS = Object.freeze({
  [DEFAULT_AGENTIC_GOAL_ID]: Object.freeze({
    id: DEFAULT_AGENTIC_GOAL_ID,
    kind: 'frontend-navigation-probe',
    targetRoute: '/observability',
    navigationTarget: secondarySidebarNav('链路追踪'),
    queryTarget: latestLogsAction,
    composerText: DEFAULT_AGENTIC_COMPOSER_TEXT,
  }),
  'chat-composer': Object.freeze({
    id: 'chat-composer',
    kind: 'chat-composer',
    targetRoute: '/',
    composerText: DEFAULT_AGENTIC_COMPOSER_TEXT,
  }),
  'observability-latest-logs': Object.freeze({
    id: 'observability-latest-logs',
    kind: 'observability-latest-logs',
    targetRoute: '/observability',
    navigationTarget: secondarySidebarNav('链路追踪'),
    queryTarget: latestLogsAction,
  }),
  'plugins-skills-open': Object.freeze({
    id: 'plugins-skills-open',
    kind: 'open-route',
    targetRoute: '/skills',
    navigationTarget: sidebarNav('插件与技能'),
  }),
  'automation-open': Object.freeze({
    id: 'automation-open',
    kind: 'open-route',
    targetRoute: '/dags',
    navigationTarget: sidebarNav('自动化'),
  }),
  'prompts-open': Object.freeze({
    id: 'prompts-open',
    kind: 'open-route',
    targetRoute: '/prompts',
    navigationTarget: sidebarNav('提示词'),
  }),
  'shared-files-open': Object.freeze({
    id: 'shared-files-open',
    kind: 'open-route',
    targetRoute: '/files',
    navigationTarget: sidebarNav('共享文件'),
  }),
  'memory-open': Object.freeze({
    id: 'memory-open',
    kind: 'open-route',
    targetRoute: '/memory',
    navigationTarget: secondarySidebarNav('记忆中心'),
  }),
  'settings-open': Object.freeze({
    id: 'settings-open',
    kind: 'open-route',
    targetRoute: '/settings',
    navigationTarget: appSidebarNav('设置'),
  }),
  'chat-send-mocked': Object.freeze({
    id: 'chat-send-mocked',
    kind: 'chat-send-mocked',
    targetRoute: '/',
    composerText: DEFAULT_AGENTIC_COMPOSER_TEXT,
    sendTarget: roleTarget('button', '发送消息', true),
    requiredRPCs: Object.freeze(['thread/start', 'turn/start']),
    requiresMockWails: true,
    requiresSandbox: true,
  }),
  'project-add-sandbox': Object.freeze({
    id: 'project-add-sandbox',
    kind: 'project-add-sandbox',
    targetRoute: '/',
    addProjectTarget: Object.freeze({ type: 'nestedRole', parentTestId: 'app-sidebar', role: 'button', name: '添加项目目录' }),
    requiredRPCs: Object.freeze(['ui/selectProjectDir', 'ui/projects/add']),
    requiresMockWails: true,
    requiresSandbox: true,
  }),
  'file-attach-sandbox': Object.freeze({
    id: 'file-attach-sandbox',
    kind: 'file-attach-sandbox',
    targetRoute: '/',
    addFileTarget: roleTarget('button', '添加文件', true),
    requiredRPCs: Object.freeze(['ui/selectFiles']),
    requiresMockWails: true,
    requiresSandbox: true,
  }),
  'settings-video-key-save-mocked': Object.freeze({
    id: 'settings-video-key-save-mocked',
    kind: 'settings-video-key-save-mocked',
    targetRoute: '/settings',
    navigationTarget: appSidebarNav('设置'),
    inputTarget: Object.freeze({ type: 'css', value: '#settings-sf-key' }),
    saveTarget: Object.freeze({ type: 'nestedRole', parentTestId: 'settings-video-card', role: 'button', name: '保存' }),
    settingsValue: 'agentic-e2e-video-key',
    requiredRPCs: Object.freeze(['ui/video/setApiKey']),
    requiresMockWails: true,
    requiresSandbox: true,
  }),
});

export const AGENTIC_GOAL_IDS = Object.freeze(Object.keys(AGENTIC_GOAL_DEFINITIONS));

export const DEFAULT_AGENTIC_GOAL = Object.freeze({
  id: DEFAULT_AGENTIC_GOAL_ID,
  composerText: DEFAULT_AGENTIC_COMPOSER_TEXT,
});

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

export function normalizeGoal(goal = {}) {
  const rawGoal = isRecord(goal) ? goal : { id: goal };
  const id = normalizeString(rawGoal.id) || DEFAULT_AGENTIC_GOAL_ID;
  const definition = AGENTIC_GOAL_DEFINITIONS[id];
  if (!definition) {
    throw new Error(`unsupported agentic e2e goal: ${id}. Supported goals: ${AGENTIC_GOAL_IDS.join(', ')}`);
  }
  const composerText = normalizeString(rawGoal.composerText) || definition.composerText || DEFAULT_AGENTIC_COMPOSER_TEXT;
  return Object.freeze({
    ...definition,
    composerText,
  });
}

export function suggestedGoalForLabel(label) {
  const normalized = normalizeString(label);
  return STABLE_GOAL_BY_LABEL.find((candidate) => candidate.pattern.test(normalized))?.goal || '';
}

function isRecord(value) {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function normalizeString(value) {
  return String(value ?? '').trim();
}
