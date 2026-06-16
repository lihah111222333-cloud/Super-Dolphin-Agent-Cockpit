import React, { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { Archive, Brain, Check, CircleUserRound, Folder, FolderOpen, Menu, Moon, PanelLeftClose, PanelLeftOpen, Pencil, Plus, Puzzle, RefreshCw, Search, Settings as SettingsIcon, SquarePlus, Sun, Trash2, X } from 'lucide-react';
import { useShallow } from 'zustand/react/shallow';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { checkAppUpdate, installLatestAppUpdate } from './shared/api/backendApi.js';
import { dashboardQueryKey, errorMessage, fetchMemoryDashboard, memoryHealth, normalizeMemorySnapshot, optionalSettingsCwd, useDashboardFocusInvalidation, textValue } from './pages/shared/pageShared.js';
import { ProjectSelector } from './pages/chat/components/ProjectSelector.jsx';
import { runUIAction } from './shared/ui/runUIAction.js';
import superDolphinLogo from './assets/super-dolphin-logo.png';
import './AppChrome.css';
import './AppShell.css';
import {
  COLOR_THEMES,
  appPageFromPathname,
  appRouteForPage,
  normalizeAppPathname,
  normalizeColorTheme,
  selectAppShellStore,
} from './app/appShellModel.js';

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

const DASHBOARD_QUERY_STALE_MS = 30_000;

const UPDATE_CHECK_DELAY_MS = 2_000;
const UPDATE_BANNER_DISMISSED_PREFIX = 'super-dolphin-update-dismissed:';

const DASHBOARD_QUERY_GC_MS = 10 * 60_000;

export const APP_PROFILER_ID = 'App';

const THEME_STORAGE_KEY = 'super-dolphin-theme';

function appPageFromLocation() {
  if (typeof window === 'undefined') return 'chat';
  return appPageFromPathname(window.location?.pathname) || 'chat';
}

function hasExplicitAppPageRoute() {
  if (typeof window === 'undefined') return false;
  const path = normalizeAppPathname(window.location?.pathname);
  return path !== '/' && Boolean(appPageFromPathname(path));
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

function uiActionOptions(store) {
  return {
    onError: (error) => {
      store?.addWarning?.('error', 'ui.action.failed', { error: errorMessage(error) });
    },
  };
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

const WORKBENCH_SIDEBAR_MIN_WIDTH = 280;
const WORKBENCH_SIDEBAR_DEFAULT_WIDTH = 340;
const WORKBENCH_SIDEBAR_MAX_WIDTH = 460;
const WORKBENCH_SIDEBAR_KEY_STEP = 16;

function clampWorkbenchSidebarWidth(value) {
  const numeric = Number(value);
  const width = Number.isFinite(numeric) ? numeric : WORKBENCH_SIDEBAR_DEFAULT_WIDTH;
  return Math.max(WORKBENCH_SIDEBAR_MIN_WIDTH, Math.min(WORKBENCH_SIDEBAR_MAX_WIDTH, Math.round(width)));
}

function workbenchSidebarNextKeyboardWidth(event, currentWidth) {
  const nextWidthByKey = {
    ArrowLeft: currentWidth - WORKBENCH_SIDEBAR_KEY_STEP,
    PageDown: currentWidth - WORKBENCH_SIDEBAR_KEY_STEP,
    ArrowRight: currentWidth + WORKBENCH_SIDEBAR_KEY_STEP,
    PageUp: currentWidth + WORKBENCH_SIDEBAR_KEY_STEP,
    Home: WORKBENCH_SIDEBAR_MIN_WIDTH,
    End: WORKBENCH_SIDEBAR_MAX_WIDTH,
  };
  return nextWidthByKey[event.key] ?? null;
}

function AppWindow({ activeLabel, memoryBadge, projectPath, store, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner }) {
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    const isTest = typeof globalThis !== 'undefined' && globalThis.process?.env?.NODE_ENV === 'test';
    if (isTest) return false;
    if (typeof window !== 'undefined') {
      return window.innerWidth > 920;
    }
    return true;
  });
  const [workbenchSidebarWidth, setWorkbenchSidebarWidth] = useState(WORKBENCH_SIDEBAR_DEFAULT_WIDTH);
  const closeSidebar = useCallback(() => setSidebarOpen(false), []);
  const setActivePageFromSidebar = useCallback((page) => {
    store.setActivePage(page);
    const isTest = typeof globalThis !== 'undefined' && globalThis.process?.env?.NODE_ENV === 'test';
    if (isTest || (typeof window !== 'undefined' && window.innerWidth <= 920)) {
      setSidebarOpen(false);
    }
  }, [store]);
  const beginWorkbenchSidebarResize = useCallback((event) => {
    event.preventDefault();
    const startX = event.clientX;
    const startWidth = workbenchSidebarWidth;
    const bodyEl = event.currentTarget?.closest?.('.sa-body');
    let latestWidth = startWidth;
    const move = (moveEvent) => {
      if (moveEvent.buttons === 0) return;
      const nextWidth = clampWorkbenchSidebarWidth(startWidth + (moveEvent.clientX - startX));
      latestWidth = nextWidth;
      bodyEl?.style.setProperty('--workbench-sidebar-width', `${nextWidth}px`);
    };
    const stop = () => {
      window.removeEventListener('pointermove', move);
      window.removeEventListener('pointerup', stop);
      window.removeEventListener('pointercancel', stop);
      setWorkbenchSidebarWidth(latestWidth);
    };
    window.addEventListener('pointermove', move);
    window.addEventListener('pointerup', stop);
    window.addEventListener('pointercancel', stop);
    try {
      event.currentTarget?.setPointerCapture?.(event.pointerId);
    } catch {
      // Synthetic and older browser pointer events can fail capture; window listeners still drive resizing.
    }
  }, [workbenchSidebarWidth]);
  const handleWorkbenchSidebarResizeKeyDown = useCallback((event) => {
    if (event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    const nextWidth = workbenchSidebarNextKeyboardWidth(event, workbenchSidebarWidth);
    if (nextWidth === null) return;
    event.preventDefault();
    setWorkbenchSidebarWidth(clampWorkbenchSidebarWidth(nextWidth));
  }, [workbenchSidebarWidth]);
  const SidebarToggleIcon = sidebarOpen ? X : Menu;
  return (
    <div className={`sa-window${sidebarOpen ? ' sidebar-open' : ' sidebar-collapsed'}`} data-theme={theme} data-testid="frontend-app">
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
      <div className="sa-body" style={{ '--workbench-sidebar-width': `${workbenchSidebarWidth}px` }}>
        {!sidebarOpen && (
          <button
            type="button"
            className="sidebar-expand-trigger"
            aria-label="展开侧栏"
            title="展开侧栏"
            onClick={() => setSidebarOpen(true)}
          >
            <PanelLeftOpen size={20} aria-hidden="true" />
          </button>
        )}
        <WorkbenchSidebar
          activePage={store.activePage}
          isOpen={sidebarOpen}
          sidebarWidth={workbenchSidebarWidth}
          onSidebarResizeKeyDown={handleWorkbenchSidebarResizeKeyDown}
          onSidebarResizeStart={beginWorkbenchSidebarResize}
          setActivePage={setActivePageFromSidebar}
          store={store}
          projectPath={projectPath}
          theme={theme}
          toggleTheme={toggleTheme}
          memorySimilarCount={memoryBadge.memorySimilarCount}
          onCloseSidebar={() => setSidebarOpen(false)}
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

const AUTOMATION_THREAD_MARKERS = Object.freeze(['automation', 'workflow', 'dag', 'cron', 'task']);

function threadFieldValue(thread = {}, keys = []) {
  for (const key of keys) {
    const value = textValue(thread[key]);
    if (value) return value;
  }
  return '';
}

function isAutomationThread(thread = {}) {
  const metadata = [
    threadFieldValue(thread, ['agentKey', 'agent_key']),
    threadFieldValue(thread, ['dagKey', 'dag_key']),
    threadFieldValue(thread, ['workflowKey', 'workflow_key']),
    threadFieldValue(thread, ['runKey', 'run_key']),
    threadFieldValue(thread, ['taskId', 'task_id']),
    threadFieldValue(thread, ['source', 'origin']),
    threadFieldValue(thread, ['kind', 'type']),
  ].map((value) => value.toLowerCase()).filter(Boolean);
  if (metadata.some((value) => AUTOMATION_THREAD_MARKERS.some((marker) => value.includes(marker)))) return true;

  const label = textValue(thread.name || thread.title);
  return label === 'AI 设计流程' ||
    /^\[AI\s*流程设计师\]/.test(label) ||
    /^\[AI\s*Workflow Designer\]/i.test(label);
}

function projectThreadItems(threads = [], projectPath = '', activeProjectPath = '') {
  const targetProjectKey = projectTreeKey(projectPath);
  const activeProjectKey = projectTreeKey(activeProjectPath);
  if (!targetProjectKey) return [];
  return (threads || []).filter((thread) => {
    if (!thread || thread.archived || thread.archivedAt) return false;
    if (isAutomationThread(thread)) return false;
    const threadProjectKey = projectTreeKey(thread.cwd);
    if (threadProjectKey) return threadProjectKey === targetProjectKey;
    return targetProjectKey === activeProjectKey;
  });
}

function taskThreadItems(threads = []) {
  return (threads || []).filter((thread) => thread && !thread.archived && !thread.archivedAt && isAutomationThread(thread));
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


function formatRelativeTime(dateString) {
  if (!dateString) return '';
  const date = new Date(dateString);
  if (isNaN(date.getTime())) return '';
  const now = new Date();
  const diffMs = now.getTime() - date.getTime();
  if (diffMs < 0) return '刚刚';

  const diffSecs = Math.floor(diffMs / 1000);
  const diffMins = Math.floor(diffSecs / 60);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);
  const diffWeeks = Math.floor(diffDays / 7);
  const diffMonths = Math.floor(diffDays / 30);

  if (diffMins < 1) return '刚刚';
  if (diffMins < 60) return `${diffMins} 分`;
  if (diffHours < 24) return `${diffHours} 小时`;
  if (diffDays < 7) return `${diffDays} 天`;
  if (diffWeeks < 5) return `${diffWeeks} 周`;
  return `${diffMonths} 月`;
}

function useSidebarThreadActions(store) {
  const [editingThreadId, setEditingThreadId] = useState('');
  const [editingName, setEditingName] = useState('');
  const [renamingThreadId, setRenamingThreadId] = useState('');
  const [deletingThreadId, setDeletingThreadId] = useState('');

  const beginRename = useCallback((thread, event) => {
    event?.stopPropagation?.();
    if (!thread?.id) return;
    setDeletingThreadId('');
    setEditingThreadId(thread.id);
    setEditingName(projectThreadLabel(thread));
  }, []);

  const cancelRename = useCallback((event) => {
    event?.stopPropagation?.();
    if (renamingThreadId) return;
    setEditingThreadId('');
    setEditingName('');
  }, [renamingThreadId]);

  const submitRename = useCallback(async (thread, event) => {
    event?.preventDefault?.();
    event?.stopPropagation?.();
    const nextName = editingName.trim();
    if (!thread?.id || !nextName || renamingThreadId) return;
    if (nextName === projectThreadLabel(thread).trim()) {
      cancelRename(event);
      return;
    }
    setRenamingThreadId(thread.id);
    try {
      const saved = await store?.renameThread?.(thread.id, nextName);
      if (saved) {
        setEditingThreadId('');
        setEditingName('');
      }
    }
    finally {
      setRenamingThreadId('');
    }
  }, [cancelRename, editingName, renamingThreadId, store]);

  const beginDelete = useCallback((threadId, event) => {
    event?.stopPropagation?.();
    if (!threadId) return;
    setEditingThreadId('');
    setEditingName('');
    setDeletingThreadId(threadId);
  }, []);

  const cancelDelete = useCallback((event) => {
    event?.stopPropagation?.();
    setDeletingThreadId('');
  }, []);

  const confirmDelete = useCallback((threadId, event) => {
    event?.stopPropagation?.();
    if (!threadId) return;
    setDeletingThreadId('');
    runUIAction(() => store?.deleteStaleThreads?.([threadId]), uiActionOptions(store));
  }, [store]);

  return {
    beginDelete,
    beginRename,
    cancelDelete,
    cancelRename,
    confirmDelete,
    deletingThreadId,
    editingName,
    editingThreadId,
    renamingThreadId,
    setEditingName,
    submitRename,
  };
}

function SidebarThreadRow({
  active,
  archiveLabel,
  label,
  onArchive,
  onSelect,
  openLabel,
  thread,
  threadActions,
}) {
  const editing = threadActions.editingThreadId === thread.id;
  const deleting = threadActions.deletingThreadId === thread.id;
  const renaming = threadActions.renamingThreadId === thread.id;

  if (deleting) {
    return (
      <li className="sidebar-thread-row sidebar-thread-row--confirm">
        <span>删除此会话？</span>
        <div className="sidebar-thread-confirm-actions">
          <button type="button" onClick={(event) => threadActions.confirmDelete(thread.id, event)}>
            删除
          </button>
          <button type="button" onClick={threadActions.cancelDelete}>
            取消
          </button>
        </div>
      </li>
    );
  }

  if (editing) {
    return (
      <li className="sidebar-thread-row sidebar-thread-row--editing">
        <form className="sidebar-thread-rename" onSubmit={(event) => threadActions.submitRename(thread, event)}>
          <input
            aria-label="会话名称"
            autoFocus
            disabled={renaming}
            maxLength={64}
            value={threadActions.editingName}
            onChange={(event) => threadActions.setEditingName(event.target.value)}
            onClick={(event) => event.stopPropagation()}
            onFocus={(event) => event.currentTarget.select()}
            onKeyDown={(event) => {
              if (event.key === 'Escape') threadActions.cancelRename(event);
            }}
          />
          <button
            type="submit"
            aria-label="保存会话名称"
            disabled={renaming}
            onMouseDown={(event) => event.preventDefault()}
          >
            <Check size={13} aria-hidden="true" />
          </button>
          <button
            type="button"
            aria-label="取消重命名"
            disabled={renaming}
            onClick={threadActions.cancelRename}
            onMouseDown={(event) => event.preventDefault()}
          >
            <X size={13} aria-hidden="true" />
          </button>
        </form>
      </li>
    );
  }

  return (
    <li className="sidebar-thread-row">
      <button
        type="button"
        className={`sidebar-thread-item${active ? ' active' : ''}`}
        onClick={onSelect}
        aria-label={openLabel}
        title={label}
      >
        <span className="sidebar-thread-title">{label}</span>
        {thread.updatedAt && (
          <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt)}</span>
        )}
      </button>
      <div className="thread-inline-actions" aria-label="会话操作">
        <button
          type="button"
          className="thread-inline-action-btn"
          onClick={(event) => threadActions.beginRename(thread, event)}
          aria-label={`重命名会话：${label}`}
          title="重命名"
        >
          <Pencil size={13} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="thread-inline-action-btn"
          onClick={onArchive}
          aria-label={archiveLabel}
          title={archiveLabel}
        >
          <Archive size={13} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="thread-inline-action-btn danger"
          onClick={(event) => threadActions.beginDelete(thread.id, event)}
          aria-label={`删除会话：${label}`}
          title="删除"
        >
          <Trash2 size={13} aria-hidden="true" />
        </button>
      </div>
    </li>
  );
}

function SidebarProjectTree({ projectPath, setActivePage, store }) {
  const [expandedProjects, setExpandedProjects] = useState({});
  const threadActions = useSidebarThreadActions(store);
  const actionOptions = uiActionOptions(store);
  const toggleExpandProject = (path) => {
    setExpandedProjects((current) => ({
      ...current,
      [path]: !current[path],
    }));
  };
  const projectItems = projectDirectoryItems(projectPath, store?.projects, store?.activeProject);
  const activeProjectPath = textValue(store?.activeProject || projectPath);
  const addProject = () => runUIAction(async () => {
    const added = await store?.addProjectFromPicker?.();
    if (added) setActivePage('chat');
  }, actionOptions);
  const selectProject = (path) => {
    if (!path) return;
    setActivePage('chat');
    runUIAction(() => store?.setActiveProjectPath?.(path), actionOptions);
  };
  const selectThread = (threadId) => {
    if (!threadId) return;
    setActivePage('chat');
    runUIAction(() => store?.setActiveThread?.(threadId), actionOptions);
  };
  const archiveThread = (threadId, event) => {
    event.stopPropagation();
    if (!threadId) return;
    runUIAction(() => store?.archiveThread?.(threadId, true), actionOptions);
  };
  return (
    <section className="sidebar-project-tree" aria-label="项目">
      <div className="sidebar-section-heading">
        <span className="sidebar-section-title">
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
          const isExpanded = !!expandedProjects[item.path];
          const visibleThreads = isExpanded ? projectThreads : projectThreads.slice(0, 5);
          return (
            <div className="sidebar-tree-project" key={item.path || item.name}>
              <button
                type="button"
                className={`sidebar-tree-folder${isActiveProject ? ' active' : ''}`}
                onClick={() => selectProject(item.path)}
                aria-label={`选择项目 ${item.name}`}
              >
                <Folder size={18} aria-hidden="true" />
                <span>{item.name}</span>
              </button>
              <ul className="sidebar-project-thread-list" aria-label={`${item.name} 聊天记录`}>
                {visibleThreads.length > 0 ? visibleThreads.map((thread) => {
                  const label = projectThreadLabel(thread);
                  const active = thread.id === store?.activeThreadId;
                  if (thread.id) {
                    return (
                      <SidebarThreadRow
                        key={thread.id}
                        active={active}
                        archiveLabel="归档此项目会话"
                        label={label}
                        onArchive={(event) => archiveThread(thread.id, event)}
                        onSelect={() => selectThread(thread.id)}
                        openLabel={`打开项目聊天：${label}`}
                        thread={thread}
                        threadActions={threadActions}
                      />
                    );
                  }
                  return (
                    <li key={thread.id || label}>
                      <button
                        type="button"
                        className={`sidebar-project-thread${active ? ' active' : ''}`}
                        onClick={() => selectThread(thread.id)}
                        aria-label={`打开项目聊天：${label}`}
                        title={label}
                      >
                        <span className="sidebar-thread-title">{label}</span>
                        {thread.updatedAt && (
                          <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt)}</span>
                        )}
                      </button>
                      <button
                        type="button"
                        className="thread-archive-btn"
                        onClick={(e) => archiveThread(thread.id, e)}
                        aria-label="归档此项目会话"
                        title="归档此项目会话"
                      >
                        <Archive size={14} aria-hidden="true" />
                      </button>
                    </li>
                  );
                }) : (
                  <li className="sidebar-project-thread-empty">暂无聊天记录</li>
                )}
                {projectThreads.length > 5 && (
                  <li className="thread-expand-item">
                    <button
                      type="button"
                      className="thread-expand-btn"
                      onClick={() => toggleExpandProject(item.path)}
                    >
                      {isExpanded ? '收起' : '展开显示'}
                    </button>
                  </li>
                )}
              </ul>
            </div>
          );
        })}
      </div>
    </section>
  );
}

function WorkbenchSidebar({
  activePage,
  isOpen = false,
  sidebarWidth = WORKBENCH_SIDEBAR_DEFAULT_WIDTH,
  onSidebarResizeKeyDown,
  onSidebarResizeStart,
  setActivePage,
  store,
  projectPath,
  theme,
  toggleTheme,
  memorySimilarCount = 0,
  onCloseSidebar,
}) {
  const actionOptions = uiActionOptions(store);
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  const isDark = theme === COLOR_THEMES.dark;
  const ThemeIcon = isDark ? Sun : Moon;
  const themeLabel = isDark ? '白天模式' : '黑夜模式';
  const startNewChat = () => {
    setActivePage('chat');
    runUIAction(() => store?.newThread?.(), actionOptions);
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
          <button
            type="button"
            aria-label="折叠侧栏"
            title="折叠侧栏"
            onClick={onCloseSidebar}
          >
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
        memoryBadgeCount={0}
        testId="sidebar-nav"
        className="sidebar-primary-nav"
      />
      <SidebarNavList
        items={secondaryNavItems}
        activePage={activePage}
        setActivePage={setActivePage}
        memoryBadgeCount={memoryBadgeCount}
        testId="sidebar-secondary-nav"
        className="sidebar-secondary-nav"
      />
      <div className="sidebar-project-selector">
        <ProjectSelector store={store} projectPath={projectPath} />
      </div>
      <div className="sidebar-scrollable-content">
        <SidebarProjectTree projectPath={projectPath} setActivePage={setActivePage} store={store} />
        <SidebarTaskSummary store={store} setActivePage={setActivePage} />
      </div>
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
      <button
        type="button"
        className="workbench-sidebar-resizer"
        data-testid="workbench-sidebar-resizer"
        role="separator"
        aria-label="调整工作台侧栏宽度"
        aria-orientation="vertical"
        aria-valuemin={WORKBENCH_SIDEBAR_MIN_WIDTH}
        aria-valuemax={WORKBENCH_SIDEBAR_MAX_WIDTH}
        aria-valuenow={sidebarWidth}
        onKeyDown={onSidebarResizeKeyDown}
        onPointerDown={onSidebarResizeStart}
      />
    </aside>
  );
}

function SidebarTaskSummary({ store, setActivePage }) {
  const tasks = taskThreadItems(store?.threads);
  const threadActions = useSidebarThreadActions(store);
  const actionOptions = uiActionOptions(store);
  const selectThread = (threadId) => {
    if (!threadId) return;
    setActivePage('chat');
    runUIAction(() => store?.setActiveThread?.(threadId), actionOptions);
  };
  const archiveThread = (threadId, event) => {
    event.stopPropagation();
    if (!threadId) return;
    runUIAction(() => store?.archiveThread?.(threadId, true), actionOptions);
  };
  return (
    <section className="sidebar-task-summary" aria-label="任务">
      <h2>任务</h2>
      {tasks.length > 0 ? (
        <ul className="sidebar-task-list" aria-label="任务对话">
          {tasks.map((thread) => {
            const label = projectThreadLabel(thread);
            const active = thread.id === store?.activeThreadId;
            if (thread.id) {
              return (
                <SidebarThreadRow
                  key={thread.id}
                  active={active}
                  archiveLabel="归档此任务会话"
                  label={label}
                  onArchive={(event) => archiveThread(thread.id, event)}
                  onSelect={() => selectThread(thread.id)}
                  openLabel={`打开任务对话：${label}`}
                  thread={thread}
                  threadActions={threadActions}
                />
              );
            }
            return (
              <li key={thread.id || label}>
                <button
                  type="button"
                  className={`sidebar-task-thread${active ? ' active' : ''}`}
                  onClick={() => selectThread(thread.id)}
                  aria-label={`打开任务对话：${label}`}
                  title={label}
                >
                  <span className="sidebar-thread-title">{label}</span>
                  {thread.updatedAt && (
                    <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt)}</span>
                  )}
                </button>
                <button
                  type="button"
                  className="thread-archive-btn"
                  onClick={(e) => archiveThread(thread.id, e)}
                  aria-label="归档此任务会话"
                  title="归档此任务会话"
                >
                  <Archive size={14} aria-hidden="true" />
                </button>
              </li>
            );
          })}
        </ul>
      ) : <p>暂无任务</p>}
    </section>
  );
}

export default App;
