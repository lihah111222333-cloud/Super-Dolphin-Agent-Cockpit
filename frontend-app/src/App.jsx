import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, FileText, FolderOpen, MessageCircle, Moon, MoreHorizontal, Search, Sparkles, Sun, Workflow } from 'lucide-react';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { ChatPage, FilesPage, MemoryPage, ObservabilityPage, PromptPage, SettingsPage, SkillsPage, WorkflowPage } from './pages/index.js';
import { dashboardQueryKey, errorMessage, fetchMemoryDashboard, memoryHealth, normalizeMemorySnapshot, optionalSettingsCwd, useDashboardFocusInvalidation } from './pages/shared/pageShared.js';

const navItems = [
  { id: 'chat', label: 'Chat', icon: MessageCircle },
  { id: 'prompts', label: '提示词', icon: FileText },
  { id: 'workflows', label: '自动化', icon: Workflow },
  { id: 'skills', label: '技能', icon: Sparkles },
  { id: 'memory', label: '记忆中心', icon: Brain },
  { id: 'observability', label: '链路追踪', icon: Search },
  { id: 'files', label: '共享文件', icon: FolderOpen },
  { id: 'settings', label: '设置', icon: MoreHorizontal },
];

const PAGE_ROUTE_BY_ID = Object.freeze({
  chat: '/',
  prompts: '/prompts',
  workflows: '/dags',
  skills: '/skills',
  memory: '/memory',
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
  '/files': 'files',
  '/shared-files': 'files',
  '/settings': 'settings',
});

const DASHBOARD_QUERY_STALE_MS = 30_000;

const DASHBOARD_QUERY_GC_MS = 10 * 60_000;

export const APP_PROFILER_ID = 'App';

const THEME_STORAGE_KEY = 'super-dolphin-theme';

const COLOR_THEMES = Object.freeze({
  dark: 'dark',
  light: 'light',
});

function normalizeColorTheme(value) {
  return value === COLOR_THEMES.light || value === COLOR_THEMES.dark ? value : COLOR_THEMES.dark;
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
  const explicitRouteRef = useRef(hasExplicitAppPageRoute());
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
  const [memoryPageSimilarCount, setMemoryPageSimilarCount] = useState(null);
  const memoryCwd = optionalSettingsCwd(projectPath);
  useDashboardFocusInvalidation(memoryCwd, 'memory');
  const memoryBadgeQuery = useQuery({
    queryKey: dashboardQueryKey(memoryCwd, 'memory'),
    queryFn: () => fetchMemoryDashboard(memoryCwd),
    enabled: Boolean(memoryCwd),
    select: memorySimilarGroupCount,
  });
  const memorySimilarCount = Math.max(0, Number(memoryBadgeQuery.data) || 0);

  useEffect(() => {
    if (store.activePage !== 'memory') {
      setMemoryPageSimilarCount(null);
    }
  }, [store.activePage, memoryCwd]);

  useEffect(() => {
    if (!memoryBadgeQuery.error) return;
    addWarning('warn', 'memory.badge.refresh.failed', { error: errorMessage(memoryBadgeQuery.error) });
  }, [addWarning, memoryBadgeQuery.error]);

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

function ActivePageContent({ store, projectPath, memoryRevision, setMemoryPageSimilarCount }) {
  if (store.activePage === 'chat') return <ChatPage store={store} projectPath={projectPath} />;
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
  return { activeLabel, memoryBadge, projectPath, theme, toggleTheme };
}

function AppWindow({ activeLabel, memoryBadge, projectPath, store, theme, toggleTheme }) {
  return (
    <div className="sa-window" data-theme={theme} data-testid="frontend-app">
      <Titlebar theme={theme} onToggleTheme={toggleTheme} />
      <div className="sa-body">
        <NavRail
          activePage={store.activePage}
          setActivePage={store.setActivePage}
          memorySimilarCount={memoryBadge.memorySimilarCount}
        />
        <main className="sa-main">
          <ActivePageContent
            store={store}
            projectPath={projectPath}
            memoryRevision={memoryBadge.memoryRevision}
            setMemoryPageSimilarCount={memoryBadge.setMemoryPageSimilarCount}
          />
          <span className="sr-only">当前页面：{activeLabel}</span>
        </main>
      </div>
    </div>
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

function Titlebar({ theme, onToggleTheme }) {
  const isDark = theme === COLOR_THEMES.dark;
  const ThemeIcon = isDark ? Sun : Moon;
  const label = isDark ? '白天模式' : '黑夜模式';

  return (
    <header className="titlebar">
      <div className="titlebar-brand">
        <span className="brand-orb" aria-hidden="true" />
        <strong>Super Agent</strong>
      </div>
      <button
        type="button"
        className="theme-toggle"
        onClick={onToggleTheme}
        aria-label={`切换到${label}`}
      >
        <ThemeIcon size={16} aria-hidden="true" />
        <span>{label}</span>
      </button>
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
