import React, { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, ChevronDown, CircleUserRound, Folder, FolderOpen, Menu, MessageSquare, Moon, PanelLeftClose, Plus, Puzzle, RefreshCw, Search, Settings as SettingsIcon, SquarePlus, Sun, X } from 'lucide-react';
import { useShallow } from 'zustand/react/shallow';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { checkAppUpdate, installLatestAppUpdate } from './shared/api/backendApi.js';
import { dashboardQueryKey, errorMessage, fetchMemoryDashboard, memoryHealth, normalizeMemorySnapshot, optionalSettingsCwd, useDashboardFocusInvalidation, textValue } from './pages/shared/pageShared.js';
import { ProjectSelector } from './pages/chat/components/ProjectSelector.jsx';
import superDolphinLogo from './assets/super-dolphin-logo.png';

function lazyNamedPage(loader, exportName) {
  return lazy(() => loader().then((module) => ({ default: module[exportName] })));
}

const ChatPage = lazyNamedPage(() => import('./pages/chat/ChatPage.jsx'), 'ChatPage');
const FilesPage = lazyNamedPage(() => import('./pages/files/FilesPage.jsx'), 'FilesPage');
const MemoryPage = lazyNamedPage(() => import('./pages/memory/MemoryPage.jsx'), 'MemoryPage');
const ObservabilityPage = lazyNamedPage(() => import('./pages/observability/ObservabilityPage.jsx'), 'ObservabilityPage');
const PromptPage = lazyNamedPage(() => import('./pages/prompts/PromptPage.jsx'), 'PromptPage');
const SettingsPage = lazyNamedPage(() => import('./pages/settings/SettingsPage.jsx'), 'SettingsPage');
const SkillsPage = lazyNamedPage(() => import('./pages/skills/SkillsPage.jsx'), 'SkillsPage');
const WorkflowPage = lazyNamedPage(() => import('./pages/workflows/WorkflowPage.jsx'), 'WorkflowPage');

const primaryNavItems = [
  { id: 'skills', label: '插件与技能', displayLabel: '插件', icon: Puzzle },
  { id: 'workflows', label: '自动化', icon: RefreshCw },
  { id: 'prompts', label: '提示词', displayLabel: '定制角色', icon: CircleUserRound },
  { id: 'files', label: '共享文件', icon: FolderOpen },
];

const secondaryNavItems = [
  { id: 'memory', label: '记忆中心', icon: Brain },
  { id: 'observability', label: '链路追踪', icon: Search },
];

const pageLabels = Object.freeze({
  chat: '聊天页面',
  skills: '插件与技能',
  prompts: '提示词',
  workflows: '自动化',
  memory: '记忆中心',
  files: '共享文件',
  observability: '链路追踪',
  settings: '设置',
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

const DASHBOARD_QUERY_STALE_MS = 30_000;

const UPDATE_CHECK_DELAY_MS = 2_000;
const UPDATE_BANNER_DISMISSED_PREFIX = 'super-dolphin-update-dismissed:';

const DASHBOARD_QUERY_GC_MS = 10 * 60_000;

export const APP_PROFILER_ID = 'App';

const THEME_STORAGE_KEY = 'super-dolphin-theme';

const COLOR_THEMES = Object.freeze({
  dark: 'dark',
  light: 'light',
});

function normalizeColorTheme(value) {
  return value === COLOR_THEMES.light || value === COLOR_THEMES.dark ? value : COLOR_THEMES.light;
}

function normalizeAppPathname(value) {
  const raw = (value || '').toString().trim().toLowerCase();
  if (!raw || raw === '/') return '/';
  return raw.replace(/\/+$/g, '') || '/';
}

function appPageFromPathname(pathname) {
  return PAGE_ID_BY_ROUTE[normalizeAppPathname(pathname)] || '';
}

function appPageFromLocation() {
  if (typeof window === 'undefined') return 'chat';
  return appPageFromPathname(window.location?.pathname) || 'chat';
}

function hasExplicitAppPageRoute() {
  if (typeof window === 'undefined') return false;
  const path = normalizeAppPathname(window.location?.pathname);
  return path !== '/' && Boolean(PAGE_ID_BY_ROUTE[path]);
}

function appRouteForPage(page) {
  return PAGE_ROUTE_BY_ID[page] || PAGE_ROUTE_BY_ID.chat;
}

function useColorTheme() {
  const [theme, setTheme] = useState(() => normalizeColorTheme(window.localStorage.getItem(THEME_STORAGE_KEY)));

  const toggleTheme = useCallback(() => {
    setTheme((current) => {
      const next = current === COLOR_THEMES.dark ? COLOR_THEMES.light : COLOR_THEMES.dark;
      window.localStorage.setItem(THEME_STORAGE_KEY, next);
      return next;
    });
  }, []);

  return { theme, toggleTheme };
}

function createDashboardQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: {
        gcTime: DASHBOARD_QUERY_GC_MS,
        retry: false,
        staleTime: DASHBOARD_QUERY_STALE_MS,
        refetchOnMount: 'always',
        refetchOnWindowFocus: 'always',
      },
    },
  });
}

function memorySimilarGroupCount(response) {
  const snapshot = response && typeof response === 'object' && Array.isArray(response.entries)
    ? response
    : normalizeMemorySnapshot(response);
  const counts = {
    preference: snapshot.entries.filter((entry) => entry.category === 'preference').length,
    project: snapshot.entries.filter((entry) => entry.category === 'project').length,
    all: snapshot.entries.length,
  };
  const health = memoryHealth(snapshot.overview, counts);
  return health?.similarGroups?.length || 0;
}

function useLatestValueRef(value) {
  const ref = useRef(value);
  useEffect(() => {
    ref.current = value;
  }, [value]);
  return ref;
}

function useRoutePopStateSync({ activePageRef, explicitRouteRef, setActivePage, suppressNextPushRef }) {
  useEffect(() => {
    const locationPage = appPageFromLocation();
    if (explicitRouteRef.current && locationPage && locationPage !== activePageRef.current) {
      suppressNextPushRef.current = true;
      setActivePage(locationPage);
    }
    const onPopState = () => {
      const nextPage = appPageFromLocation();
      suppressNextPushRef.current = true;
      setActivePage(nextPage);
    };
    window.addEventListener('popstate', onPopState);
    return () => window.removeEventListener('popstate', onPopState);
  }, [activePageRef, explicitRouteRef, setActivePage, suppressNextPushRef]);
}

function useRoutePushSync({ activePage, routeBootstrapPending, setActivePage }) {
  const initializedRef = useRef(false);
  const explicitRouteRef = useRef(null);
  if (explicitRouteRef.current === null) {
    explicitRouteRef.current = hasExplicitAppPageRoute();
  }
  const suppressNextPushRef = useRef(false);
  const activePageRef = useLatestValueRef(activePage);
  useRoutePopStateSync({ activePageRef, explicitRouteRef, setActivePage, suppressNextPushRef });
  useEffect(() => {
    if (!initializedRef.current) return;
    const locationPage = appPageFromLocation();
    if (explicitRouteRef.current && locationPage) {
      if (locationPage !== activePage) {
        suppressNextPushRef.current = true;
        setActivePage(locationPage);
        return;
      }
      if (routeBootstrapPending) return;
      explicitRouteRef.current = false;
    }

    if (suppressNextPushRef.current) {
      suppressNextPushRef.current = false;
      return;
    }

    const nextPath = appRouteForPage(activePage);
    if (normalizeAppPathname(window.location?.pathname) === normalizeAppPathname(nextPath)) return;
    window.history.pushState({ activePage }, '', nextPath);
  }, [activePage, routeBootstrapPending, setActivePage]);
  useEffect(() => {
    initializedRef.current = true;
  }, []);
}

function useActivePageHistory(activePage, setActivePage, routeBootstrapPending = false) {
  useRoutePushSync({ activePage, routeBootstrapPending, setActivePage });
}

function useMemoryBadgeState(store, projectPath) {
  const queryClient = useQueryClient();
  const addWarning = store.addWarning;
  const memoryRevision = Number(store.memoryRevision || 0);
  const memoryCwd = optionalSettingsCwd(projectPath);
  const [memoryPageSimilarState, setMemoryPageSimilarState] = useState({ page: store.activePage, cwd: memoryCwd, count: null });
  if (memoryPageSimilarState.page !== store.activePage || memoryPageSimilarState.cwd !== memoryCwd) {
    setMemoryPageSimilarState({ page: store.activePage, cwd: memoryCwd, count: null });
  }
  const setMemoryPageSimilarCount = useCallback((count) => {
    setMemoryPageSimilarState((current) => ({ ...current, count }));
  }, []);
  useDashboardFocusInvalidation(memoryCwd, 'memory');
  const memoryBadgeQuery = useQuery({
    queryKey: dashboardQueryKey(memoryCwd, 'memory'),
    queryFn: async () => {
      try {
        return await fetchMemoryDashboard(memoryCwd);
      }
      catch (error) {
        addWarning('warn', 'memory.badge.refresh.failed', { error: errorMessage(error) });
        throw error;
      }
    },
    enabled: Boolean(memoryCwd),
    select: memorySimilarGroupCount,
  });
  const memorySimilarCount = Math.max(0, Number(memoryBadgeQuery.data) || 0);
  const memoryPageSimilarCount = (
    memoryPageSimilarState.page === store.activePage && memoryPageSimilarState.cwd === memoryCwd
      ? memoryPageSimilarState.count
      : null
  );

  useEffect(() => {
    if (!memoryCwd || memoryRevision <= 0) return;
    void queryClient.invalidateQueries({ queryKey: dashboardQueryKey(memoryCwd, 'memory') });
  }, [memoryCwd, memoryRevision, queryClient]);

  return {
    memoryRevision,
    memorySimilarCount: memoryPageSimilarCount ?? memorySimilarCount,
    setMemoryPageSimilarCount,
  };
}

function runUIAction(action) {
  try {
    const result = typeof action === 'function' ? action() : action;
    if (result && typeof result.catch === 'function') {
      void result.catch(() => {});
    }
  }
  catch (error) {
    void error;
  }
}

function PageLoadingFallback() {
  return (
    <div className="empty-state" aria-live="polite">
      <h2>正在加载页面</h2>
      <p>请稍候</p>
    </div>
  );
}

function ChatPageRoute({ projectPath, rightPanelOpen, setRightPanelOpen }) {
  const store = useClientStore();
  return (
    <ChatPage
      store={store}
      projectPath={projectPath}
      rightPanelOpen={rightPanelOpen}
      setRightPanelOpen={setRightPanelOpen}
    />
  );
}

function PromptPageRoute({ projectPath, refreshKey }) {
  const resolveLaunchPreferences = useClientStore((state) => state.resolveLaunchPreferences);
  const store = useMemo(() => ({ resolveLaunchPreferences }), [resolveLaunchPreferences]);
  return <PromptPage projectPath={projectPath} store={store} refreshKey={refreshKey} />;
}

function WorkflowPageRoute({ projectPath, refreshKey }) {
  const store = useClientStore();
  return <WorkflowPage projectPath={projectPath} store={store} refreshKey={refreshKey} />;
}

function FilesPageRoute({ projectPath }) {
  const store = useClientStore();
  return <FilesPage projectPath={projectPath} store={store} />;
}

function ActivePageContent({ activePage, store, projectPath, memoryRevision, setMemoryPageSimilarCount, rightPanelOpen, setRightPanelOpen }) {
  if (activePage === 'chat') {
    return (
      <ChatPageRoute
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        setRightPanelOpen={setRightPanelOpen}
      />
    );
  }
  if (activePage === 'prompts') return <PromptPageRoute projectPath={projectPath} refreshKey={store.promptRevision} />;
  if (activePage === 'workflows') return <WorkflowPageRoute projectPath={projectPath} refreshKey={store.workflowRevision} />;
  if (activePage === 'skills') {
    return <SkillsPage projectPath={projectPath} refreshKey={store.skillRevision} resolveLaunchPreferences={store.resolveLaunchPreferences} />;
  }
  if (activePage === 'memory') {
    return (
      <MemoryPage
        projectPath={projectPath}
        refreshKey={memoryRevision}
        onSimilarCountChange={setMemoryPageSimilarCount}
        resolveLaunchPreferences={store.resolveLaunchPreferences}
      />
    );
  }
  if (activePage === 'files') return <FilesPageRoute projectPath={projectPath} />;
  if (activePage === 'observability') return <ObservabilityPage />;
  if (activePage === 'settings') return <SettingsPage projectPath={projectPath} />;
  return null;
}

function useAppBootstrap(bootstrap, skipBootstrap) {
  useEffect(() => {
    if (skipBootstrap) return undefined;
    let cancelled = false;
    bootstrap().catch((error) => {
      if (!cancelled) {
        console.error('[frontend-app] bootstrap failed', error);
      }
    });
    return () => {
      cancelled = true;
    };
  }, [bootstrap, skipBootstrap]);
}

function updateVersionFromResult(result) {
  return (result?.version || result?.artifact?.version || '').toString().trim();
}

function updateDismissedKey(version) {
  return `${UPDATE_BANNER_DISMISSED_PREFIX}${version}`;
}

function useAppUpdateBanner(skipBootstrap) {
  const [state, setState] = useState({ status: 'idle', update: null, message: '' });

  useEffect(() => {
    if (skipBootstrap) return undefined;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      setState((current) => (current.update ? current : { ...current, status: 'checking' }));
      checkAppUpdate()
        .then((result) => {
          if (cancelled || !result?.enabled || !result?.available) return;
          const version = updateVersionFromResult(result);
          if (version && window.localStorage.getItem(updateDismissedKey(version)) === '1') return;
          setState({ status: 'available', update: { ...result, version }, message: '' });
        })
        .catch((error) => {
          if (!cancelled) console.info('[frontend-app] background update check failed', error);
        })
        .finally(() => {
          if (!cancelled) {
            setState((current) => (current.status === 'checking' ? { ...current, status: 'idle' } : current));
          }
        });
    }, UPDATE_CHECK_DELAY_MS);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
    };
  }, [skipBootstrap]);

  const dismiss = useCallback(() => {
    setState((current) => {
      const version = updateVersionFromResult(current.update);
      if (version) window.localStorage.setItem(updateDismissedKey(version), '1');
      return { status: 'dismissed', update: null, message: '' };
    });
  }, []);

  const install = useCallback(async () => {
    setState((current) => ({ ...current, status: 'installing', message: '' }));
    try {
      const result = await installLatestAppUpdate();
      setState((current) => ({ ...current, status: 'installing', message: result?.started === false ? '安装没有启动，请稍后重试。' : '安装程序已启动，请按提示完成更新。' }));
    } catch (error) {
      setState((current) => ({ ...current, status: 'available', message: `更新失败：${errorMessage(error)}` }));
    }
  }, []);

  return {
    update: state.update,
    status: state.status,
    message: state.message,
    dismiss,
    install,
  };
}

function useAppShellState(store, skipBootstrap) {
  const routeBootstrapPending = !skipBootstrap && !['ready', 'failed'].includes(store.bootstrapStatus);
  useActivePageHistory(store.activePage, store.setActivePage, routeBootstrapPending);
  useAppBootstrap(store.bootstrap, skipBootstrap);
  const projectPath = store.activeProject && store.activeProject !== '.' ? store.activeProject : store.cwd || '未选择项目';
  const memoryBadge = useMemoryBadgeState(store, projectPath);
  const activeLabel = useMemo(() => (
    pageLabels[store.activePage] || pageLabels.chat
  ), [store.activePage]);
  const { theme, toggleTheme } = useColorTheme();
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const updateBanner = useAppUpdateBanner(skipBootstrap);
  return { activeLabel, memoryBadge, projectPath, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner };
}

function AppWindow({ activeLabel, memoryBadge, projectPath, store, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner }) {
  const [sidebarOpen, setSidebarOpen] = useState(false);
  const closeSidebar = useCallback(() => setSidebarOpen(false), []);
  const setActivePageFromSidebar = useCallback((page) => {
    store.setActivePage(page);
    setSidebarOpen(false);
  }, [store]);
  const SidebarToggleIcon = sidebarOpen ? X : Menu;
  return (
    <div className={`sa-window${sidebarOpen ? ' sidebar-open' : ''}`} data-theme={theme} data-testid="frontend-app">
      <button
        type="button"
        className="workbench-toggle"
        aria-label={sidebarOpen ? '关闭工作台' : '打开工作台'}
        aria-controls="app-sidebar"
        aria-expanded={sidebarOpen}
        onClick={() => setSidebarOpen((open) => !open)}
      >
        <SidebarToggleIcon size={22} aria-hidden="true" />
      </button>
      {sidebarOpen ? <button type="button" className="sidebar-scrim" aria-label="关闭工作台" onClick={closeSidebar} /> : null}
      <div className="sa-body">
        <WorkbenchSidebar
          activePage={store.activePage}
          isOpen={sidebarOpen}
          setActivePage={setActivePageFromSidebar}
          store={store}
          projectPath={projectPath}
          theme={theme}
          toggleTheme={toggleTheme}
          memorySimilarCount={memoryBadge.memorySimilarCount}
        />
        <main className="sa-main">
          <AppUpdateBanner updateBanner={updateBanner} />
          <Suspense fallback={<PageLoadingFallback />}>
            <ActivePageContent
              activePage={store.activePage}
              store={store}
              projectPath={projectPath}
              memoryRevision={memoryBadge.memoryRevision}
              setMemoryPageSimilarCount={memoryBadge.setMemoryPageSimilarCount}
              rightPanelOpen={rightPanelOpen}
              setRightPanelOpen={setRightPanelOpen}
            />
          </Suspense>
          <span className="sr-only">当前页面：{activeLabel}</span>
        </main>
      </div>
    </div>
  );
}

function AppUpdateBanner({ updateBanner }) {
  if (!updateBanner?.update) return null;
  const version = updateVersionFromResult(updateBanner.update);
  const installing = updateBanner.status === 'installing';
  return (
    <section className="app-update-banner" data-testid="app-update-banner" role="status">
      <div className="app-update-copy">
        <strong>发现新版本{version ? ` ${version}` : ''}</strong>
        <span>建议更新到最新版，以获得最新功能和修复。</span>
        {updateBanner.message ? <small>{updateBanner.message}</small> : null}
      </div>
      <div className="app-update-actions">
        <button type="button" className="app-update-primary" onClick={updateBanner.install} disabled={installing}>
          {installing ? '正在更新…' : '立即更新'}
        </button>
        <button type="button" className="app-update-secondary" onClick={updateBanner.dismiss} disabled={installing}>
          稍后
        </button>
      </div>
    </section>
  );
}

function selectAppShellStore(state) {
  return {
    actionNotice: state.actionNotice,
    activePage: state.activePage,
    activeProject: state.activeProject,
    activeThreadId: state.activeThreadId,
    activeTurnByThread: state.activeTurnByThread,
    activityStatsByThread: state.activityStatsByThread,
    addProjectFromPicker: state.addProjectFromPicker,
    addWarning: state.addWarning,
    attachDroppedFilesForComposer: state.attachDroppedFilesForComposer,
    attachPathsForComposer: state.attachPathsForComposer,
    attachments: state.attachments,
    bootstrap: state.bootstrap,
    bootstrapStatus: state.bootstrapStatus,
    copyActiveThreadInfo: state.copyActiveThreadInfo,
    cwd: state.cwd,
    diffTextByThread: state.diffTextByThread,
    draft: state.draft,
    error: state.error,
    forceCompleteActiveThread: state.forceCompleteActiveThread,
    hasActiveThreadActions: state.hasActiveThreadActions,
    hasInterruptibleThreadAction: state.hasInterruptibleThreadAction,
    interruptActiveThread: state.interruptActiveThread,
    loadOlderThreadMessages: state.loadOlderThreadMessages,
    memoryRevision: state.memoryRevision,
    newThread: state.newThread,
    openForkDraft: state.openForkDraft,
    openNewWindow: state.openNewWindow,
    projects: state.projects,
    promptRevision: state.promptRevision,
    recoverActiveThread: state.recoverActiveThread,
    removeProjectPath: state.removeProjectPath,
    removeAttachment: state.removeAttachment,
    respondApproval: state.respondApproval,
    resolveLaunchPreferences: state.resolveLaunchPreferences,
    rightPanelWidth: state.rightPanelWidth,
    runtimeResultEntries: state.runtimeResultEntries,
    selectFilesForComposer: state.selectFilesForComposer,
    sendDraft: state.sendDraft,
    sending: state.sending,
    setActivePage: state.setActivePage,
    setActiveProjectPath: state.setActiveProjectPath,
    setActiveThread: state.setActiveThread,
    setDraft: state.setDraft,
    setRightPanelWidth: state.setRightPanelWidth,
    skillRevision: state.skillRevision,
    statuses: state.statuses,
    syncThreadState: state.syncThreadState,
    threadDiffReadyByThread: state.threadDiffReadyByThread,
    threadMessagePaginationByThread: state.threadMessagePaginationByThread,
    threadStateLoadingByThread: state.threadStateLoadingByThread,
    threadTimelineReadyByThread: state.threadTimelineReadyByThread,
    threads: state.threads,
    timelinesByThread: state.timelinesByThread,
    toggleProviderMode: state.toggleProviderMode,
    tokenUsageByThread: state.tokenUsageByThread,
    warningEntries: state.warningEntries,
    workflowRevision: state.workflowRevision,
  };
}

function AppShell({ skipBootstrap = false }) {
  const store = useClientStore(useShallow(selectAppShellStore));
  const shell = useAppShellState(store, skipBootstrap);
  return <AppWindow {...shell} store={store} />;
}

function App(props) {
  const [queryClient] = useState(createDashboardQueryClient);
  return (
    <QueryClientProvider client={queryClient}>
      <AppShell {...props} />
    </QueryClientProvider>
  );
}

function projectNameFromPath(projectPath) {
  const value = textValue(projectPath);
  if (!value || value === '未选择项目') return 'Super-Dolphin';
  const normalized = value.replace(/\\/g, '/').replace(/\/+$/g, '');
  return normalized.split('/').filter(Boolean).pop() || 'Super-Dolphin';
}

function projectDirectoryItems(projectPath, projects = [], activeProject = '') {
  const seen = new Set();
  const items = [];
  const add = (value) => {
    const path = textValue(value);
    if (!path || path === '未选择项目' || seen.has(path)) return;
    seen.add(path);
    items.push({ path, name: projectNameFromPath(path) });
  };
  add(activeProject);
  add(projectPath);
  projects.forEach(add);
  return items.length ? items : [{ path: '', name: 'Super-Dolphin' }];
}

function projectTreeKey(value) {
  return textValue(value).replace(/\\/g, '/').replace(/\/+$/g, '').toLowerCase();
}

function projectThreadItems(threads = [], projectPath = '', activeProjectPath = '') {
  const targetProjectKey = projectTreeKey(projectPath);
  const activeProjectKey = projectTreeKey(activeProjectPath);
  if (!targetProjectKey) return [];
  return (threads || []).filter((thread) => {
    if (!thread || thread.archived || thread.archivedAt) return false;
    const threadProjectKey = projectTreeKey(thread.cwd);
    if (threadProjectKey) return threadProjectKey === targetProjectKey;
    return targetProjectKey === activeProjectKey;
  });
}

function projectThreadLabel(thread = {}) {
  const id = textValue(thread.id);
  const label = textValue(thread.name || thread.title);
  if (!label || (id && label === id)) return '新对话';
  return label;
}

function SidebarNavList({ items, activePage, setActivePage, memoryBadgeCount = 0, testId, className }) {
  return (
    <nav className={`app-sidebar-nav ${className || ''}`} data-testid={testId}>
      {items.map((item) => {
        const Icon = item.icon;
        const badgeCount = item.id === 'memory' ? memoryBadgeCount : 0;
        return (
          <button
            key={item.id}
            type="button"
            className={activePage === item.id ? 'active' : ''}
            onClick={() => setActivePage(item.id)}
            aria-label={item.label}
          >
            <Icon size={22} aria-hidden="true" />
            <span>{item.displayLabel || item.label}</span>
            {badgeCount > 0 ? <i aria-hidden="true" title={`${badgeCount} 条待整合相似记忆`} /> : null}
          </button>
        );
      })}
    </nav>
  );
}

function SidebarProjectTree({ projectPath, setActivePage, store }) {
  const projectItems = projectDirectoryItems(projectPath, store?.projects, store?.activeProject);
  const activeProjectPath = textValue(store?.activeProject || projectPath);
  const addProject = () => runUIAction(() => store?.addProjectFromPicker?.());
  const selectProject = (path) => {
    if (!path) return;
    runUIAction(() => store?.setActiveProjectPath?.(path));
  };
  const selectThread = (threadId) => {
    if (!threadId) return;
    setActivePage('chat');
    runUIAction(() => store?.setActiveThread?.(threadId));
  };
  return (
    <section className="sidebar-project-tree" aria-label="项目">
      <div className="sidebar-section-heading">
        <span className="sidebar-section-title">
          <ChevronDown size={15} aria-hidden="true" />
          <span>项目</span>
        </span>
        <button type="button" className="sidebar-icon-action" aria-label="添加项目目录" onClick={addProject}>
          <Plus size={16} aria-hidden="true" />
        </button>
      </div>
      <div className="sidebar-tree-root">
        {projectItems.map((item) => {
          const isActiveProject = item.path && item.path === activeProjectPath;
          const projectThreads = projectThreadItems(store?.threads, item.path, activeProjectPath);
          return (
            <div className="sidebar-tree-project" key={item.path || item.name}>
              <button
                type="button"
                className={`sidebar-tree-folder${isActiveProject ? ' active' : ''}`}
                onClick={() => selectProject(item.path)}
                aria-label={`选择项目 ${item.name}`}
              >
                <ChevronDown size={14} aria-hidden="true" />
                <Folder size={18} aria-hidden="true" />
                <span>{item.name}</span>
              </button>
              <ul className="sidebar-project-thread-list" aria-label={`${item.name} 聊天记录`}>
                {projectThreads.length > 0 ? projectThreads.map((thread) => {
                  const label = projectThreadLabel(thread);
                  const active = thread.id === store?.activeThreadId;
                  return (
                    <li key={thread.id || label}>
                      <button
                        type="button"
                        className={`sidebar-project-thread${active ? ' active' : ''}`}
                        onClick={() => selectThread(thread.id)}
                        aria-label="打开项目聊天"
                        title={label}
                      >
                        <MessageSquare size={14} aria-hidden="true" />
                        <span data-label={label} aria-hidden="true" />
                      </button>
                    </li>
                  );
                }) : (
                  <li className="sidebar-project-thread-empty">暂无聊天记录</li>
                )}
              </ul>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function WorkbenchSidebar({ activePage, isOpen = false, setActivePage, store, projectPath, theme, toggleTheme, memorySimilarCount = 0 }) {
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  const isDark = theme === COLOR_THEMES.dark;
  const ThemeIcon = isDark ? Sun : Moon;
  const themeLabel = isDark ? '白天模式' : '黑夜模式';
  const startNewChat = () => {
    setActivePage('chat');
    runUIAction(() => store?.newThread?.());
  };

  return (
    <aside
      id="app-sidebar"
      className={`app-sidebar${isOpen ? ' is-open' : ''}${activePage === 'chat' ? ' app-sidebar--chat' : ''}`}
      data-testid="app-sidebar"
      aria-label="Super-Dolphin 工作台"
      style={isOpen ? { marginLeft: 0 } : undefined}
    >
      <div className="sidebar-brand-row">
        <div className="sidebar-brand">
          <img src={superDolphinLogo} alt="" aria-hidden="true" />
          <strong>Super-Dolphin</strong>
        </div>
        <div className="sidebar-brand-actions" aria-label="工作台工具">
          <button type="button" aria-label="搜索" title="搜索" onClick={() => setActivePage('observability')}>
            <Search size={19} aria-hidden="true" />
          </button>
          <button type="button" aria-label="折叠侧栏" title="桌面侧栏当前固定展示" disabled>
            <PanelLeftClose size={19} aria-hidden="true" />
          </button>
        </div>
      </div>
      <button
        type="button"
        className={`sidebar-new-chat ${activePage === 'chat' ? 'active' : ''}`}
        aria-label="新对话"
        onClick={startNewChat}
      >
        <SquarePlus size={22} aria-hidden="true" />
        <span>新对话</span>
      </button>
      <SidebarNavList
        items={primaryNavItems}
        activePage={activePage}
        setActivePage={setActivePage}
        testId="sidebar-nav"
        className="sidebar-primary-nav"
      />
      <div className="sidebar-project-selector">
        <ProjectSelector store={store} projectPath={projectPath} />
      </div>
      <SidebarProjectTree projectPath={projectPath} setActivePage={setActivePage} store={store} />
      <SidebarNavList
        items={secondaryNavItems}
        activePage={activePage}
        setActivePage={setActivePage}
        memoryBadgeCount={memoryBadgeCount}
        testId="sidebar-secondary-nav"
        className="sidebar-secondary-nav"
      />
      <SidebarTaskSummary />
      <button
        type="button"
        className="sidebar-theme-toggle"
        onClick={toggleTheme}
        aria-label={`切换到${themeLabel}`}
      >
        <ThemeIcon size={16} aria-hidden="true" />
        <span>{themeLabel}</span>
      </button>
      <button
        type="button"
        className={`sidebar-settings ${activePage === 'settings' ? 'active' : ''}`}
        aria-label="设置"
        onClick={() => setActivePage('settings')}
      >
        <SettingsIcon size={25} aria-hidden="true" />
        <span>设置</span>
      </button>
    </aside>
  );
}

function SidebarTaskSummary() {
  return (
    <section className="sidebar-task-summary" aria-label="任务">
      <h2>任务</h2>
      <p>暂无任务</p>
    </section>
  );
}

export default App;
