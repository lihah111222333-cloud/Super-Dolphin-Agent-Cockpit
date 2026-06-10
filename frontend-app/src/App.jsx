import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, CheckCircle2, CircleStop, Copy, Eye, FileText, FolderOpen, MessageCircle, Moon, MoreHorizontal, PanelTopOpen, RefreshCw, Search, Sparkles, Sun, Workflow, X } from 'lucide-react';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { ChatPage, ProjectSelector } from './pages/chat/ChatPage.jsx';
import { FilesPage } from './pages/files/FilesPage.jsx';
import { MemoryPage } from './pages/memory/MemoryPage.jsx';
import { ObservabilityPage } from './pages/observability/ObservabilityPage.jsx';
import { PromptPage } from './pages/prompts/PromptPage.jsx';
import { SettingsPage } from './pages/settings/SettingsPage.jsx';
import { SkillsPage } from './pages/skills/SkillsPage.jsx';
import { WorkflowPage } from './pages/workflows/WorkflowPage.jsx';
import { checkAppUpdate, installLatestAppUpdate } from './shared/api/backendApi.js';
import { dashboardQueryKey, errorMessage, fetchMemoryDashboard, memoryHealth, normalizeMemorySnapshot, optionalSettingsCwd, useDashboardFocusInvalidation, textValue } from './pages/shared/pageShared.js';

const navItems = [
  { id: 'chat', label: 'Chat', icon: MessageCircle },
  { id: 'skills', label: '技能', icon: Sparkles },
  { id: 'prompts', label: '提示词', icon: FileText },
  { id: 'workflows', label: '自动化', icon: Workflow },
  { id: 'memory', label: '记忆中心', icon: Brain },
  { id: 'files', label: '共享文件', icon: FolderOpen },
  { id: 'observability', label: '链路追踪', icon: Search },
  { id: 'settings', label: '设置', icon: MoreHorizontal },
];

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

function ActivePageContent({ store, projectPath, memoryRevision, setMemoryPageSimilarCount, rightPanelOpen, setRightPanelOpen }) {
  if (store.activePage === 'chat') {
    return (
      <ChatPage
        store={store}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        setRightPanelOpen={setRightPanelOpen}
      />
    );
  }
  if (store.activePage === 'prompts') return <PromptPage projectPath={projectPath} store={store} refreshKey={store.promptRevision} />;
  if (store.activePage === 'workflows') return <WorkflowPage projectPath={projectPath} store={store} refreshKey={store.workflowRevision} />;
  if (store.activePage === 'skills') {
    return <SkillsPage projectPath={projectPath} refreshKey={store.skillRevision} resolveLaunchPreferences={store.resolveLaunchPreferences} />;
  }
  if (store.activePage === 'memory') {
    return (
      <MemoryPage
        projectPath={projectPath}
        refreshKey={memoryRevision}
        onSimilarCountChange={setMemoryPageSimilarCount}
        resolveLaunchPreferences={store.resolveLaunchPreferences}
      />
    );
  }
  if (store.activePage === 'files') return <FilesPage projectPath={projectPath} store={store} />;
  if (store.activePage === 'observability') return <ObservabilityPage />;
  if (store.activePage === 'settings') return <SettingsPage projectPath={projectPath} />;
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
    navItems.find((item) => item.id === store.activePage)?.label || 'Chat'
  ), [store.activePage]);
  const { theme, toggleTheme } = useColorTheme();
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const updateBanner = useAppUpdateBanner(skipBootstrap);
  return { activeLabel, memoryBadge, projectPath, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner };
}

function AppWindow({ activeLabel, memoryBadge, projectPath, store, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner }) {
  return (
    <div className="sa-window" data-theme={theme} data-testid="frontend-app">
      <Titlebar
        theme={theme}
        onToggleTheme={toggleTheme}
        store={store}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        setRightPanelOpen={setRightPanelOpen}
      />
      <div className="sa-body">
        <NavRail
          activePage={store.activePage}
          setActivePage={store.setActivePage}
          memorySimilarCount={memoryBadge.memorySimilarCount}
        />
        <main className="sa-main">
          <AppUpdateBanner updateBanner={updateBanner} />
          <ActivePageContent
            store={store}
            projectPath={projectPath}
            memoryRevision={memoryBadge.memoryRevision}
            setMemoryPageSimilarCount={memoryBadge.setMemoryPageSimilarCount}
            rightPanelOpen={rightPanelOpen}
            setRightPanelOpen={setRightPanelOpen}
          />
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
  const store = useClientStore();
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

function Titlebar({ theme, onToggleTheme, store, projectPath, rightPanelOpen, setRightPanelOpen }) {
  const isDark = theme === COLOR_THEMES.dark;
  const ThemeIcon = isDark ? Sun : Moon;
  const label = isDark ? '白天模式' : '黑夜模式';
  const isChatPage = store?.activePage === 'chat';

  const canUseThreadActions = Boolean(store?.hasActiveThreadActions?.());
  const canInterruptThread = Boolean(store?.hasInterruptibleThreadAction?.());
  const bootstrapFailureMessage = store?.bootstrapStatus === 'failed' && textValue(store?.error)
    ? `连接后端失败：${textValue(store?.error)}`
    : '';
  const feedback = store?.actionNotice?.message
    ? store?.actionNotice
    : (bootstrapFailureMessage ? { message: bootstrapFailureMessage, tone: 'error' } : null);

  const toggleRightPanel = () => {
    setRightPanelOpen?.((prev) => !prev);
  };

  return (
    <header className="titlebar" data-testid="chat-toolbar">
      <div className="titlebar-brand">
        <span className="brand-orb" aria-hidden="true" />
        <strong>Super Dolphin</strong>
      </div>
      <div className="titlebar-center">
        {isChatPage && store && (
          <div className="titlebar-actions">
            <ProjectSelector store={store} projectPath={projectPath} />
            <button
              type="button"
              className="icon-btn"
              aria-label="新窗口（独立进程）"
              title="新窗口（独立进程）"
              onClick={() => runUIAction(() => store.openNewWindow?.())}
            >
              <PanelTopOpen size={14} />
            </button>
            <button
              type="button"
              className="icon-btn"
              aria-label={canUseThreadActions ? "复制当前线程" : "复制当前线程（不可用）"}
              title={canUseThreadActions ? "复制当前线程" : "请先选择会话"}
              disabled={!canUseThreadActions}
              onClick={() => runUIAction(() => store.copyActiveThreadInfo())}
            >
              <Copy size={14} />
            </button>
            <button
              type="button"
              className="icon-btn"
              aria-label={canInterruptThread ? "停止" : "停止（不可用）"}
              title={canInterruptThread ? "中断当前执行" : "无运行中任务"}
              disabled={!canInterruptThread}
              onClick={() => runUIAction(() => store.interruptActiveThread())}
            >
              <CircleStop size={14} />
            </button>
            <button
              type="button"
              className="icon-btn"
              aria-label={canUseThreadActions ? "强制完成" : "强制完成（不可用）"}
              title={canUseThreadActions ? "强制完成当前执行" : "请先选择会话"}
              disabled={!canUseThreadActions}
              onClick={() => runUIAction(() => store.forceCompleteActiveThread())}
            >
              <CheckCircle2 size={14} />
            </button>
            <button
              type="button"
              className="icon-btn"
              aria-label={canUseThreadActions ? "进程恢复" : "请先选择会话"}
              title={canUseThreadActions ? "手动杀进程并恢复连接" : "请先选择会话"}
              disabled={!canUseThreadActions}
              onClick={() => runUIAction(() => store.recoverActiveThread())}
            >
              <RefreshCw size={14} />
            </button>
            {feedback?.message ? (
              <output
                className={`action-feedback ${feedback.tone || "info"}`}
                data-testid="chat-action-feedback"
              >
                {feedback.message}
              </output>
            ) : null}
          </div>
        )}
      </div>

      <div className="titlebar-right">
        <button
          type="button"
          className="theme-toggle"
          onClick={onToggleTheme}
          aria-label={`切换到${label}`}
        >
          <ThemeIcon size={14} aria-hidden="true" />
          <span>{label}</span>
        </button>

        {isChatPage && (
          <button
            type="button"
            className={`icon-btn sidebar-toggle ${rightPanelOpen ? 'active' : ''}`}
            aria-label={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
            title={rightPanelOpen ? '隐藏侧边栏' : '显示侧边栏'}
            aria-pressed={rightPanelOpen}
            onClick={toggleRightPanel}
          >
            {rightPanelOpen ? <X size={14} /> : <Eye size={14} />}
          </button>
        )}
      </div>
    </header>
  );
}

function NavRail({ activePage, setActivePage, memorySimilarCount = 0 }) {
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  return (
    <aside className="nav-rail" data-testid="sidebar-nav">
      <nav>
        {navItems.map((item) => {
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
              <span>{item.label}</span>
              {badgeCount > 0 ? <i aria-hidden="true" title={`${badgeCount} 条待整合相似记忆`} /> : null}
            </button>
          );
        })}
      </nav>
    </aside>
  );
}

export default App;
