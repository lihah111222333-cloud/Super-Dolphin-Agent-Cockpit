export const COLOR_THEMES = Object.freeze({
  dark: 'dark',
  light: 'light',
});

const PAGE_ROUTE_BY_ID = Object.freeze({
  chat: '/',
  prompts: '/prompts',
  workflows: '/dags',
  skills: '/skills',
  memory: '/memory',
  observability: '/observability',
  files: '/files',
  settings: '/settings',
});

const PAGE_ID_BY_ROUTE = Object.freeze({
  '/': 'chat',
  '/chat': 'chat',
  '/prompts': 'prompts',
  '/dags': 'workflows',
  '/workflows': 'workflows',
  '/skills': 'skills',
  '/memory': 'memory',
  '/memory-center': 'memory',
  '/observability': 'observability',
  '/files': 'files',
  '/shared-files': 'files',
  '/settings': 'settings',
});

export const APP_SHELL_STORE_KEYS = Object.freeze([
  'actionNotice',
  'activePage',
  'activeProject',
  'activeThreadId',
  'activeTurnByThread',
  'activityStatsByThread',
  'addProjectFromPicker',
  'addWarning',
  'archiveThread',
  'attachDroppedFilesForComposer',
  'attachPathsForComposer',
  'attachments',
  'bootstrap',
  'bootstrapStatus',
  'beginOpeningThread',
  'copyActiveThreadInfo',
  'cwd',
  'deleteStaleThreads',
  'diffTextByThread',
  'draft',
  'error',
  'forceCompleteActiveThread',
  'hasActiveThreadActions',
  'hasInterruptibleThreadAction',
  'interruptActiveThread',
  'loadOlderThreadMessages',
  'memoryRevision',
  'newThread',
  'openForkDraft',
  'openNewWindow',
  'projects',
  'promptRevision',
  'recoverActiveThread',
  'removeProjectPath',
  'removeAttachment',
  'respondApproval',
  'resolveLaunchPreferences',
  'rightPanelWidth',
  'renameThread',
  'runtimeResultEntries',
  'selectFilesForComposer',
  'sendDraft',
  'sending',
  'setActivePage',
  'setActiveProjectPath',
  'setActiveThread',
  'setDraft',
  'setRightPanelWidth',
  'skillRevision',
  'sidebarThreadsByProject',
  'statuses',
  'syncThreadState',
  'threadDiffReadyByThread',
  'threadMessagePaginationByThread',
  'threadStateLoadingByThread',
  'threadTimelineReadyByThread',
  'threads',
  'timelinesByThread',
  'toggleProviderMode',
  'tokenUsageByThread',
  'warningEntries',
  'workflowRevision',
]);

export function normalizeColorTheme(value) {
  return value === COLOR_THEMES.light || value === COLOR_THEMES.dark ? value : COLOR_THEMES.light;
}

export function normalizeAppPathname(value) {
  const raw = (value || '').toString().trim().toLowerCase();
  if (!raw || raw === '/') return '/';
  return raw.replace(/\/+$/g, '') || '/';
}

export function appPageFromPathname(pathname) {
  return PAGE_ID_BY_ROUTE[normalizeAppPathname(pathname)] || '';
}

export function appRouteForPage(page) {
  return PAGE_ROUTE_BY_ID[page] || PAGE_ROUTE_BY_ID.chat;
}

export function selectAppShellStore(state) {
  return Object.fromEntries(APP_SHELL_STORE_KEYS.map((key) => [key, state[key]]));
}
