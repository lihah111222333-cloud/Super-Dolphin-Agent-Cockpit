export const COLOR_THEMES = Object.freeze({
  dark: 'dark',
  light: 'light',
});

import { requiredAppStoragePort } from '../shared/api/browser/browserStorage.js';

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

const THREAD_STATUS_BUSY_STATES = new Set([
  'starting',
  'preparing',
  'thinking',
  'running',
  'editing',
  'waiting',
  'syncing',
  'responding',
  'force_completing',
  'interrupting',
]);

const THREAD_STATUS_ALIASES = Object.freeze({
  工作中: 'running',
  发送中: 'preparing',
  pending: 'starting',
  recovering: 'syncing',
  create: 'idle',
  created: 'idle',
  错误: 'error',
  失败: 'failed',
  空闲: 'idle',
  等待指示: 'idle',
});

export const APP_SHELL_STORE_KEYS = Object.freeze([
  'actionNotice',
  'activePage',
  'activeProject',
  'activeThreadId',
  'activeTurnByThread',
  'activityStatsByThread',
  'activityThreadAtById',
  'addComposerCapability',
  'addProjectFromPicker',
  'addWarning',
  'archiveThread',
  'attachDroppedFilesForComposer',
  'attachPathsForComposer',
  'attachments',
  'bootstrap',
  'bootstrapStatus',
  'captureThreadSelection',
  'chatSurfaceLoadingCwd',
  'clearComposer',
  'closeForkDraft',
  'composerCapabilities',
  'continueWithSharedFile',
  'copyActiveThreadInfo',
  'cwd',
  'deleteStaleThreads',
  'diffTextByThread',
  'dismissActionNotice',
  'draft',
  'error',
  'forceCompleteActiveThread',
  'forkDraft',
  'hasActiveThreadActions',
  'hasForceCompleteThreadAction',
  'hasInterruptibleThreadAction',
  'interruptActiveThread',
  'loadOlderThreadMessages',
  'loadThreadConfig',
  'memoryRevision',
  'newThread',
  'notifyAction',
  'openForkDraft',
  'openNewWindow',
  'openThreadById',
  'pinnedThreadAtById',
  'projects',
  'promptRevision',
  'provider',
  'providerConfig',
  'reconcileComposerCapabilities',
  'recoverActiveThread',
  'removeAttachment',
  'removeComposerCapability',
  'removeProjectPath',
  'renameThread',
  'resolveLaunchPreferences',
  'respondApproval',
  'restoreComposerModelInheritance',
  'runtimeResultEntries',
  'saveComposerModelConfig',
  'selectFilesForComposer',
  'sendDraft',
  'sending',
  'setActivePage',
  'setActiveProjectPath',
  'setActiveThread',
  'setDraft',
  'sharedFilesRevision',
  'skillRevision',
  'smoothStreaming',
  'statuses',
  'submitForkThread',
  'syncThreadState',
  'threadArchiveLoadingByThread',
  'threadConfigByThread',
  'threadConfigLoadingByThread',
  'threadConfigSaving',
  'threadDiffReadyByThread',
  'threadMessagePaginationByThread',
  'threadRecoveryPendingByThread',
  'threadStateLoadingByThread',
  'threadTimelineReadyByThread',
  'threads',
  'timelinesByThread',
  'toggleForkDraftSharedFile',
  'toggleThreadPin',
  'tokenUsageByThread',
  'warningEntries',
  'workflowRevision',
]);

export function normalizeColorTheme(value) {
  if (value === COLOR_THEMES.light || value === COLOR_THEMES.dark) {
    return value;
  }
  throw new Error('invalid color theme');
}

export function normalizeAppPathname(value) {
  const raw = value === null || value === undefined ? '' : value.toString().trim().toLowerCase();
  if (!raw || raw === '/') return '/';
  const normalized = raw.replace(/\/+$/g, '');
  return normalized ? normalized : '/';
}

export function appPageFromPathname(pathname) {
  const page = PAGE_ID_BY_ROUTE[normalizeAppPathname(pathname)];
  return page === undefined ? '' : page;
}

export function appRouteForPage(page) {
  return PAGE_ROUTE_BY_ID[page] || PAGE_ROUTE_BY_ID.chat;
}

export function threadStatusBusy(status) {
  const raw = status === null || status === undefined ? '' : status.toString().trim();
  if (!raw) return false;
  const alias = THREAD_STATUS_ALIASES[raw] || raw;
  return THREAD_STATUS_BUSY_STATES.has(alias.toLowerCase().replace(/-/g, '_'));
}

export function selectAppShellStore(state) {
  return Object.fromEntries(APP_SHELL_STORE_KEYS.map((key) => [key, state[key]]));
}

export const THEME_STORAGE_KEY = 'super-dolphin-theme';

export function getStoredTheme() {
  if (typeof window === 'undefined' || typeof document === 'undefined') {
    throw new Error('window or document is unavailable');
  }
  const value = requiredAppStoragePort('theme storage').get(THEME_STORAGE_KEY);
  if (value === null) {
    return COLOR_THEMES.light;
  }
  return normalizeColorTheme(value);
}

export function syncThemeDOM(theme) {
  if (typeof document === 'undefined' || !document.documentElement || !document.body) {
    throw new Error('theme document is unavailable');
  }
  const normalized = normalizeColorTheme(theme);
  document.documentElement.setAttribute('data-theme', normalized);
  document.body.setAttribute('data-theme', normalized);
}
