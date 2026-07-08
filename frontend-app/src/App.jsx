import React, { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { Brain, Check, CircleUserRound, Folder, FolderOpen, Menu, Moon, PanelLeftClose, PanelLeftOpen, Plus, Puzzle, RefreshCw, Search, Settings as SettingsIcon, SquarePlus, Sun, Trash2, X } from 'lucide-react';
import { useShallow } from 'zustand/react/shallow';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { UITestMCPShell } from './devtools/UITestMCPShell.jsx';
import { checkAppUpdate, getSidebarState, installLatestAppUpdate } from './shared/api/backendApi.js';
import { currentTimestampMillis, dashboardQueryKey, errorMessage, firstPresentText, memoryHealth, normalizeMemorySnapshot, optionalSettingsCwd, optionalTimestampMillis, requireArrayValue, useDashboardFocusInvalidation, textValue } from './pages/shared/pageShared.js';
import { memoryPageService } from './pages/memory/services/memoryPageService.js';
import { ProjectSelector } from './pages/chat/components/ProjectSelector.jsx';
import { runUIAction } from './shared/ui/runUIAction.js';
import { APP_BRAND_NAME, APP_COPY, APP_LANGUAGE_STORAGE_KEY, initialAppLocale } from './shared/i18n/appI18n.js';
import suiyuanBrandIcon from './assets/suiyuan-brand-icon.png';
import './AppChrome.css';
import './AppShell.css';
import {
  COLOR_THEMES,
  appPageFromPathname,
  appRouteForPage,
  normalizeAppPathname,
  normalizeColorTheme,
  selectAppShellStore,
  threadStatusBusy,
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
  { id: 'skills', labelKey: 'skills', displayLabelKey: 'skillsShort', icon: Puzzle },
  { id: 'workflows', labelKey: 'workflows', icon: RefreshCw },
  { id: 'prompts', labelKey: 'prompts', displayLabelKey: 'promptsShort', icon: CircleUserRound },
  { id: 'files', labelKey: 'files', icon: FolderOpen },
];

const secondaryNavItems = [
  { id: 'memory', labelKey: 'memory', icon: Brain },
  { id: 'observability', labelKey: 'observability', icon: Search },
];

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
  const [theme, setTheme] = useState(() => normalizeColorTheme(requiredAppStoragePort('theme storage').get(THEME_STORAGE_KEY)));

  const toggleTheme = useCallback(() => {
    setTheme((current) => {
      const next = current === COLOR_THEMES.dark ? COLOR_THEMES.light : COLOR_THEMES.dark;
      requiredAppStoragePort('theme storage').set(THEME_STORAGE_KEY, next);
      return next;
    });
  }, []);

  return { theme, toggleTheme };
}

function useAppLanguage() {
  const [locale, setLocale] = useState(initialAppLocale);
  const copy = APP_COPY[locale] || APP_COPY.zh;
  const toggleLocale = useCallback(() => {
    setLocale((current) => {
      const next = current === 'zh' ? 'en' : 'zh';
      requiredAppStoragePort('language storage').set(APP_LANGUAGE_STORAGE_KEY, next);
      return next;
    });
  }, []);
  return { copy, locale, toggleLocale };
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

function requiredAppStoragePort(label = 'app storage') {
  if (typeof globalThis === 'undefined') throw new Error(`${label} global object is unavailable`);
  const storage = globalThis.window?.['localStorage'];
  if (!storage || typeof storage.getItem !== 'function' || typeof storage.setItem !== 'function') {
    throw new Error(`${label} is unavailable`);
  }
  return {
    get(key) {
      return storage.getItem(key);
    },
    set(key, value) {
      storage.setItem(key, value);
    },
  };
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
  const { data: memoryBadgeData } = useQuery({
    queryKey: dashboardQueryKey(memoryCwd, 'memory'),
    queryFn: async () => {
      try {
        return await memoryPageService.loadDashboard(memoryCwd);
      }
      catch (error) {
        addWarning('warn', 'memory.badge.refresh.failed', { error: errorMessage(error) });
        throw error;
      }
    },
    enabled: Boolean(memoryCwd),
    select: memorySimilarGroupCount,
  });
  const memorySimilarCount = Math.max(0, Number(memoryBadgeData) || 0);
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
      <h2>{APP_COPY.zh.pageLoadingTitle}</h2>
      <p>{APP_COPY.zh.pageLoadingDescription}</p>
    </div>
  );
}

function ChatPageRoute({ copy, projectPath, rightPanelOpen, setRightPanelOpen }) {
  const store = useClientStore();
  return (
    <ChatPage
      copy={copy.chat}
      store={store}
      projectPath={projectPath}
      rightPanelOpen={rightPanelOpen}
      setRightPanelOpen={setRightPanelOpen}
    />
  );
}

function PromptPageRoute({ copy, projectPath, refreshKey }) {
  const resolveLaunchPreferences = useClientStore((state) => state.resolveLaunchPreferences);
  const store = useMemo(() => ({ resolveLaunchPreferences }), [resolveLaunchPreferences]);
  return <PromptPage copy={copy.prompts} projectPath={projectPath} store={store} refreshKey={refreshKey} />;
}

function workflowSubpageLabel(workflowView, copy) {
  if (workflowView === 'templates') return copy.templatePageTitle;
  if (workflowView === 'freeDesign') return copy.freeDesignPageTitle;
  return '';
}

function WorkflowPageRoute({ copy, onWorkflowViewChange, projectPath, refreshKey }) {
  const store = useClientStore();
  return <WorkflowPage copy={copy.workflow} onWorkflowViewChange={onWorkflowViewChange} projectPath={projectPath} store={store} refreshKey={refreshKey} />;
}

function FilesPageRoute({ copy, projectPath }) {
  const store = useClientStore();
  return <FilesPage copy={copy.files} projectPath={projectPath} store={store} />;
}

function ActivePageContent({ activePage, copy, store, projectPath, memoryRevision, setMemoryPageSimilarCount, onWorkflowViewChange, rightPanelOpen, setRightPanelOpen }) {
  if (activePage === 'chat') {
    return (
      <ChatPageRoute
        copy={copy}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        setRightPanelOpen={setRightPanelOpen}
      />
    );
  }
  if (activePage === 'prompts') return <PromptPageRoute copy={copy} projectPath={projectPath} refreshKey={store.promptRevision} />;
  if (activePage === 'workflows') return <WorkflowPageRoute copy={copy} onWorkflowViewChange={onWorkflowViewChange} projectPath={projectPath} refreshKey={store.workflowRevision} />;
  if (activePage === 'skills') {
    return <SkillsPage copy={copy.skills} projectPath={projectPath} refreshKey={store.skillRevision} resolveLaunchPreferences={store.resolveLaunchPreferences} />;
  }
  if (activePage === 'memory') {
    return (
      <MemoryPage
        projectPath={projectPath}
        refreshKey={memoryRevision}
        onSimilarCountChange={setMemoryPageSimilarCount}
        copy={copy.memory}
        resolveLaunchPreferences={store.resolveLaunchPreferences}
      />
    );
  }
  if (activePage === 'files') return <FilesPageRoute copy={copy} projectPath={projectPath} />;
  if (activePage === 'observability') return <ObservabilityPage copy={copy.observability} />;
  if (activePage === 'settings') return <SettingsPage copy={copy.settings} projectPath={projectPath} />;
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
  return firstPresentText(result?.version, result?.artifact?.version);
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
          if (version && requiredAppStoragePort('update banner storage').get(updateDismissedKey(version)) === '1') return;
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
      if (version) requiredAppStoragePort('update banner storage').set(updateDismissedKey(version), '1');
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
  const { theme, toggleTheme } = useColorTheme();
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const updateBanner = useAppUpdateBanner(skipBootstrap);
  return { memoryBadge, projectPath, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner };
}

const WORKBENCH_SIDEBAR_MIN_WIDTH = 280;
const WORKBENCH_SIDEBAR_DEFAULT_WIDTH = 340;
const WORKBENCH_SIDEBAR_MAX_WIDTH = 460;
const WORKBENCH_SIDEBAR_KEY_STEP = 16;
const PROJECT_THREAD_CACHE_TTL_MS = 45_000;

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

function AppWindow({ memoryBadge, projectPath, store, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner }) {
  const { copy, locale, toggleLocale } = useAppLanguage();
  const activeLabel = copy.nav[store.activePage] || copy.nav.chat;
  const [currentPageState, setCurrentPageState] = useState({ activePage: store.activePage, workflowView: 'automation' });
  if (currentPageState.activePage !== store.activePage) {
    setCurrentPageState({ activePage: store.activePage, workflowView: 'automation' });
  }
  const currentWorkflowView = currentPageState.activePage === store.activePage ? currentPageState.workflowView : 'automation';
  const currentPageLabelOverride = store.activePage === 'workflows' ? workflowSubpageLabel(currentWorkflowView, copy.workflow) : '';
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
    setCurrentPageState({ activePage: page, workflowView: 'automation' });
    const isTest = typeof globalThis !== 'undefined' && globalThis.process?.env?.NODE_ENV === 'test';
    if (isTest || (typeof window !== 'undefined' && window.innerWidth <= 920)) {
      setSidebarOpen(false);
    }
  }, [store]);
  const handleWorkflowViewChange = useCallback((workflowView) => {
    setCurrentPageState({ activePage: 'workflows', workflowView: textValue(workflowView) || 'automation' });
  }, []);
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
        aria-label={sidebarOpen ? copy.workbench.close : copy.workbench.open}
        aria-controls="app-sidebar"
        aria-expanded={sidebarOpen}
        onClick={() => setSidebarOpen((open) => !open)}
      >
        <SidebarToggleIcon size={22} aria-hidden="true" />
      </button>
      {sidebarOpen ? <button type="button" className="sidebar-scrim" aria-label={copy.workbench.close} onClick={closeSidebar} /> : null}
      <div className="sa-body" style={{ '--workbench-sidebar-width': `${workbenchSidebarWidth}px` }}>
        {!sidebarOpen && (
          <button
            type="button"
            className="sidebar-expand-trigger"
            aria-label={copy.workbench.expand}
            title={copy.workbench.expand}
            onClick={() => setSidebarOpen(true)}
          >
            <PanelLeftOpen size={20} aria-hidden="true" />
          </button>
        )}
        <WorkbenchSidebar
          activePage={store.activePage}
          copy={copy}
          isOpen={sidebarOpen}
          locale={locale}
          sidebarWidth={workbenchSidebarWidth}
          onSidebarResizeKeyDown={handleWorkbenchSidebarResizeKeyDown}
          onSidebarResizeStart={beginWorkbenchSidebarResize}
          setActivePage={setActivePageFromSidebar}
          store={store}
          projectPath={projectPath}
          theme={theme}
          toggleLocale={toggleLocale}
          toggleTheme={toggleTheme}
          memorySimilarCount={memoryBadge.memorySimilarCount}
          onCloseSidebar={() => setSidebarOpen(false)}
        />
        <main className="sa-main">
          <AppUpdateBanner copy={copy.update} updateBanner={updateBanner} />
          <Suspense fallback={<PageLoadingFallback />}>
            <ActivePageContent
              activePage={store.activePage}
              copy={copy}
              store={store}
              projectPath={projectPath}
              memoryRevision={memoryBadge.memoryRevision}
              setMemoryPageSimilarCount={memoryBadge.setMemoryPageSimilarCount}
              onWorkflowViewChange={handleWorkflowViewChange}
              rightPanelOpen={rightPanelOpen}
              setRightPanelOpen={setRightPanelOpen}
            />
          </Suspense>
          <span className="sr-only">{copy.currentPagePrefix}: {currentPageLabelOverride || activeLabel}</span>
        </main>
      </div>
    </div>
  );
}

function AppUpdateBanner({ copy = APP_COPY.zh.update, updateBanner }) {
  if (!updateBanner?.update) return null;
  const version = updateVersionFromResult(updateBanner.update);
  const installing = updateBanner.status === 'installing';
  return (
    <section className="app-update-banner" data-testid="app-update-banner" role="status">
      <div className="app-update-copy">
        <strong>{copy.available}{version ? ` ${version}` : ''}</strong>
        <span>{copy.description}</span>
        {updateBanner.message ? <small>{updateBanner.message}</small> : null}
      </div>
      <div className="app-update-actions">
        <button type="button" className="app-update-primary" onClick={updateBanner.install} disabled={installing}>
          {installing ? copy.installing : copy.install}
        </button>
        <button type="button" className="app-update-secondary" onClick={updateBanner.dismiss} disabled={installing}>
          {copy.dismiss}
        </button>
      </div>
    </section>
  );
}

function AppShell({ skipBootstrap = false, uiTestMCPMode = false }) {
  const store = useClientStore(useShallow(selectAppShellStore)); const shell = useAppShellState(store, skipBootstrap || uiTestMCPMode);
  return uiTestMCPMode ? <UITestMCPShell /> : <AppWindow {...shell} store={store} />;
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
  if (!value || value === '未选择项目') return APP_BRAND_NAME;
  const normalized = value.replace(/\\/g, '/').replace(/\/+$/g, '');
  return normalized.split('/').filter(Boolean).pop() || APP_BRAND_NAME;
}

function projectDirectoryItems(projectPath, projects = [], activeProject = '') {
  const seen = new Set();
  const items = [];
  const add = (value) => {
    const path = textValue(value);
    if (!path || path === '.' || path === '未选择项目' || seen.has(path)) return;
    seen.add(path);
    items.push({ path, name: projectNameFromPath(path) });
  };
  projects.forEach(add);
  add(activeProject);
  add(projectPath);
  return items.length ? items : [{ path: '', name: APP_BRAND_NAME }];
}

function projectTreeKey(value) {
  return textValue(value).replace(/\\/g, '/').replace(/\/+$/g, '').toLowerCase();
}

function sidebarActiveProjectPath(activeProject, projectPath) {
  const active = textValue(activeProject);
  return active && active !== '.' ? active : textValue(projectPath);
}

function projectThreadSourceId(thread) {
  return firstPresentText(thread?.id, thread?.threadId, thread?.thread_id, thread?.agentId, thread?.agent_id);
}

function mergeProjectThreadSources(...sources) {
  const canonical = Array.isArray(sources[0]) ? sources[0] : [];
  const canonicalIds = new Set();
  const mergedById = new Map();
  const ordered = [];

  for (const thread of canonical) {
    const id = projectThreadSourceId(thread);
    if (!id || canonicalIds.has(id)) continue;
    canonicalIds.add(id);
    mergedById.set(id, thread);
    ordered.push(id);
  }

  const newThreadIds = [];
  for (const source of sources.slice(1)) {
    for (const thread of Array.isArray(source) ? source : []) {
      const id = projectThreadSourceId(thread);
      if (!id) continue;
      if (mergedById.has(id)) {
        mergedById.set(id, { ...mergedById.get(id), ...thread });
        continue;
      }
      mergedById.set(id, thread);
      newThreadIds.push(id);
    }
  }

  return [...newThreadIds, ...ordered].map((id) => mergedById.get(id));
}

const AUTOMATION_THREAD_MARKERS = Object.freeze(['automation', 'workflow', 'dag', 'cron', 'task']);
const ARCHIVED_THREAD_STATUS = 'archived';

function threadFieldValue(thread = {}, keys = []) {
  for (const key of keys) {
    const value = textValue(thread[key]);
    if (value) return value;
  }
  return '';
}

function projectThreadArchiveMap(snapshot = {}) {
  for (const candidate of [
    snapshot?.['threadArchives.chat'],
    snapshot?.threadArchivesChat,
    snapshot?.archivedThreadAtById,
    snapshot?.threadArchives?.chat,
    snapshot?.thread_archives?.chat,
  ]) {
    if (candidate && typeof candidate === 'object' && !Array.isArray(candidate)) return candidate;
  }
  return {};
}

function archiveTimestamp(value) {
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0;
}

function projectThreadArchiveTimestamp(thread = {}, archiveMap = {}) {
  for (const key of ['id', 'threadId', 'thread_id', 'agentId', 'agent_id']) {
    const id = textValue(thread[key]);
    const timestamp = id ? archiveTimestamp(archiveMap[id]) : 0;
    if (timestamp > 0) return timestamp;
  }
  return 0;
}

function projectThreadStatusArchived(thread = {}) {
  return ['status', 'state', 'lifecycleStatus', 'lifecycle_status', 'threadStatus', 'thread_status']
    .some((key) => textValue(thread[key]).toLowerCase() === ARCHIVED_THREAD_STATUS);
}

function isProjectThreadArchived(thread = {}) {
  return Boolean(thread?.archived) || Boolean(thread?.isArchived) || Boolean(thread?.archivedAt) || projectThreadStatusArchived(thread);
}

function projectThreadRuntimeMap(snapshot = {}) {
  for (const runtime of [snapshot?.agentRuntimeById, snapshot?.agent_runtime_by_id]) {
    if (runtime && typeof runtime === 'object' && !Array.isArray(runtime)) return runtime;
  }
  return {};
}

function projectThreadRuntimeCwd(thread = {}, runtimeById = {}) {
  for (const key of ['id', 'threadId', 'thread_id', 'agentId', 'agent_id', 'providerThreadId', 'provider_thread_id', 'sessionId', 'session_id', 'sessionUuid', 'session_uuid']) {
    const id = textValue(thread[key]);
    const runtime = id ? runtimeById[id] : null;
    if (!runtime || typeof runtime !== 'object' || Array.isArray(runtime)) continue;
    const cwd = firstPresentText(runtime.cwd, runtime.CWD, runtime.workdir, runtime.workDir, runtime.work_dir);
    if (cwd) return cwd;
  }
  return '';
}

function threadProjectPath(thread = {}) {
  const direct = threadFieldValue(thread, [
    'cwd',
    'projectPath',
    'project_path',
    'workspacePath',
    'workspace_path',
    'rootPath',
    'root_path',
  ]);
  if (direct) return direct;

  for (const key of ['project', 'workspace', 'metadata', 'meta']) {
    const value = thread[key];
    if (!value || typeof value !== 'object') continue;
    const nested = threadFieldValue(value, ['path', 'cwd', 'root', 'projectPath', 'project_path']);
    if (nested) return nested;
  }
  return '';
}

function sidebarSnapshotThreads(snapshot) {
  const threads = Array.isArray(snapshot?.threads) ? snapshot.threads : [];
  const archiveMap = projectThreadArchiveMap(snapshot);
  const runtimeById = projectThreadRuntimeMap(snapshot);
  return threads.map((thread) => {
    const archivedAt = projectThreadArchiveTimestamp(thread, archiveMap);
    const runtimeCwd = projectThreadRuntimeCwd(thread, runtimeById);
    if (!archivedAt && !runtimeCwd && !isProjectThreadArchived(thread)) return thread;
    return {
      ...thread,
      ...(runtimeCwd && !textValue(thread?.cwd) ? { cwd: runtimeCwd } : {}),
      ...(archivedAt || isProjectThreadArchived(thread) ? {
        archived: true,
        archivedAt: thread?.archivedAt || archivedAt || 1,
      } : {}),
    };
  });
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

  const label = firstPresentText(thread.name, thread.title);
  return label === 'AI 设计流程' ||
    /^\[AI\s*流程设计师\]/.test(label) ||
    /^\[AI\s*Workflow Designer\]/i.test(label);
}

function projectThreadItems(threads = [], projectPath = '', activeProjectPath = '', options = {}) {
  const sourceThreads = requireArrayValue(threads, 'project thread list');
  const targetProjectKey = projectTreeKey(projectPath);
  const activeProjectKey = projectTreeKey(activeProjectPath);
  const allowMissingCwdFallback = options.allowMissingCwdFallback !== false;
  if (!targetProjectKey) return [];
  return sourceThreads.filter((thread) => {
    if (!thread || isProjectThreadArchived(thread)) return false;
    if (isAutomationThread(thread)) return false;
    const threadProjectKey = projectTreeKey(threadProjectPath(thread));
    if (threadProjectKey) return threadProjectKey === targetProjectKey;
    if (!allowMissingCwdFallback) return false;
    return targetProjectKey === activeProjectKey;
  });
}

function taskThreadItems(threads = []) {
  const sourceThreads = requireArrayValue(threads, 'task thread list');
  return sourceThreads.filter((thread) => thread && !thread.archived && !thread.archivedAt && isAutomationThread(thread));
}

function projectThreadLabel(thread = {}) {
  const id = textValue(thread.id);
  const label = firstPresentText(thread.name, thread.title);
  if (!label || (id && label === id)) return '新对话';
  return label;
}

function SidebarNavList({ copy, items, activePage, setActivePage, memoryBadgeCount = 0, testId, className }) {
  return (
    <nav className={`app-sidebar-nav ${textValue(className)}`} data-testid={testId}>
      {items.map((item) => {
        const Icon = item.icon;
        const label = copy.nav[item.labelKey];
        const displayLabel = item.displayLabelKey ? copy.nav[item.displayLabelKey] : label;
        const badgeCount = item.id === 'memory' ? memoryBadgeCount : 0;
        return (
          <button
            key={item.id}
            type="button"
            className={activePage === item.id ? 'active' : ''}
            onClick={() => setActivePage(item.id)}
            aria-label={label}
          >
            <Icon size={22} aria-hidden="true" />
            <span>{displayLabel}</span>
            {badgeCount > 0 ? <i aria-hidden="true" title={`${badgeCount} ${copy.workbench.memoryBadgeTitle}`} /> : null}
          </button>
        );
      })}
    </nav>
  );
}


function formatRelativeTime(dateString, copy = APP_COPY.zh.workbench.relativeTime) {
  if (!dateString) return '';
  const dateTime = optionalTimestampMillis(dateString, 'relative time timestamp');
  if (!dateTime) return '';
  const diffMs = currentTimestampMillis('relative time clock') - dateTime;
  if (diffMs < 0) return copy.now;

  const diffSecs = Math.floor(diffMs / 1000);
  const diffMins = Math.floor(diffSecs / 60);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);
  const diffWeeks = Math.floor(diffDays / 7);
  const diffMonths = Math.floor(diffDays / 30);

  if (diffMins < 1) return copy.now;
  if (diffMins < 60) return copy.minute.replace('{count}', diffMins);
  if (diffHours < 24) return copy.hour.replace('{count}', diffHours);
  if (diffDays < 7) return copy.day.replace('{count}', diffDays);
  if (diffWeeks < 5) return copy.week.replace('{count}', diffWeeks);
  return copy.month.replace('{count}', diffMonths);
}

function useSidebarThreadActions(store, options = {}) {
  const { onDeleteThreads, onRenameThread } = options;
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

  const handleRenameBlur = useCallback((event) => {
    if (renamingThreadId) return;
    const nextTarget = event.relatedTarget;
    if (nextTarget && event.currentTarget.contains(nextTarget)) return;
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
        onRenameThread?.(thread.id, nextName);
        setEditingThreadId('');
        setEditingName('');
      }
    }
    finally {
      setRenamingThreadId('');
    }
  }, [cancelRename, editingName, onRenameThread, renamingThreadId, store]);

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
    runUIAction(async () => {
      const result = await store?.deleteStaleThreads?.([threadId]);
      if (!result || result.deleted > 0) onDeleteThreads?.([threadId]);
    }, uiActionOptions(store));
  }, [onDeleteThreads, store]);

  return {
    beginDelete,
    beginRename,
    cancelDelete,
    cancelRename,
    confirmDelete,
    deletingThreadId,
    editingName,
    editingThreadId,
    handleRenameBlur,
    renamingThreadId,
    setEditingName,
    submitRename,
  };
}

function SidebarThreadRow({
  active,
  copy = APP_COPY.zh.workbench,
  label,
  onSelect,
  openLabel,
  thread,
  threadActions,
}) {
  const editing = threadActions.editingThreadId === thread.id;
  const deleting = threadActions.deletingThreadId === thread.id;
  const renaming = threadActions.renamingThreadId === thread.id;
  const running = threadStatusBusy(thread.status);
  const runningLabel = copy.threadRunning || '会话运行中';

  if (deleting) {
    return (
      <li className="sidebar-thread-row sidebar-thread-row--confirm">
        <span>{copy.deleteQuestion}</span>
        <div className="sidebar-thread-confirm-actions">
          <button type="button" onClick={(event) => threadActions.confirmDelete(thread.id, event)}>
            {copy.delete}
          </button>
          <button type="button" onClick={threadActions.cancelDelete}>
            {copy.cancel}
          </button>
        </div>
      </li>
    );
  }

  if (editing) {
    return (
      <li className="sidebar-thread-row sidebar-thread-row--editing">
        <form className="sidebar-thread-rename" onBlur={threadActions.handleRenameBlur} onSubmit={(event) => threadActions.submitRename(thread, event)}>
          <input
            aria-label={copy.conversationName}
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
            aria-label={copy.saveConversationName}
            disabled={renaming}
            onMouseDown={(event) => event.preventDefault()}
          >
            <Check size={13} aria-hidden="true" />
          </button>
          <button
            type="button"
            aria-label={copy.cancelRename}
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
        onDoubleClick={(event) => threadActions.beginRename(thread, event)}
        aria-label={openLabel}
        title={label}
      >
        <span className="sidebar-thread-title">{label}</span>
        {thread.updatedAt && (
          <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt, copy.relativeTime)}</span>
        )}
      </button>
      <div className={`thread-inline-actions${running ? ' is-running' : ''}`} aria-label={copy.conversationActions}>
        {running ? (
          <span className="thread-inline-spinner" aria-label={runningLabel} title={runningLabel}>
            <RefreshCw size={13} aria-hidden="true" />
          </span>
        ) : null}
        <button
          type="button"
          className="thread-inline-action-btn danger"
          onClick={(event) => threadActions.beginDelete(thread.id, event)}
          aria-label={`${copy.delete}：${label}`}
          title={copy.delete}
        >
          <Trash2 size={13} aria-hidden="true" />
        </button>
      </div>
    </li>
  );
}

function SidebarProjectTree({ copy = APP_COPY.zh.workbench, projectPath, setActivePage, store }) {
  const [expandedProjects, setExpandedProjects] = useState({});
  const [projectThreadCache, setProjectThreadCache] = useState({});
  const projectThreadCacheRef = useRef({});
  const actionOptions = uiActionOptions(store);
  const projectItems = projectDirectoryItems(projectPath, store?.projects, store?.activeProject);
  const activeProjectPath = sidebarActiveProjectPath(store?.activeProject, projectPath);
  useEffect(() => {
    projectThreadCacheRef.current = projectThreadCache;
  }, [projectThreadCache]);
  const updateProjectThreadCache = useCallback((path, update) => {
    const key = projectTreeKey(path);
    if (!key) return;
    setProjectThreadCache((current) => {
      const previous = current[key] || { path, threads: [], loadedAt: 0, loading: false, error: '' };
      const patch = typeof update === 'function' ? update(previous) : update;
      return {
        ...current,
        [key]: {
          ...previous,
          path,
          ...patch,
        },
      };
    });
  }, []);
  const renameCachedProjectThread = useCallback((threadId, name) => {
    const id = textValue(threadId);
    if (!id) return;
    setProjectThreadCache((current) => {
      let changed = false;
      const next = Object.fromEntries(Object.entries(current).map(([key, entry]) => {
        const threads = Array.isArray(entry?.threads) ? entry.threads : [];
        const nextThreads = threads.map((thread) => {
          if (thread?.id !== id) return thread;
          changed = true;
          return { ...thread, name, title: name };
        });
        return [key, { ...entry, threads: nextThreads }];
      }));
      return changed ? next : current;
    });
  }, []);
  const removeCachedProjectThreads = useCallback((threadIds = []) => {
    const ids = new Set((Array.isArray(threadIds) ? threadIds : [threadIds]).map(textValue).filter(Boolean));
    if (ids.size === 0) return;
    setProjectThreadCache((current) => {
      let changed = false;
      const next = Object.fromEntries(Object.entries(current).map(([key, entry]) => {
        const threads = Array.isArray(entry?.threads) ? entry.threads : [];
        const nextThreads = threads.filter((thread) => !ids.has(textValue(thread?.id)));
        if (nextThreads.length !== threads.length) changed = true;
        return [key, { ...entry, threads: nextThreads }];
      }));
      return changed ? next : current;
    });
  }, []);
  const threadActions = useSidebarThreadActions(store, {
    onDeleteThreads: removeCachedProjectThreads,
    onRenameThread: renameCachedProjectThread,
  });
  const refreshProjectThreadCache = useCallback((path, options = {}) => {
    const key = projectTreeKey(path);
    if (!key) return;
    const current = projectThreadCacheRef.current[key];
    if (!options.force && current && !current.error && currentTimestampMillis('project thread cache clock') - current.loadedAt < PROJECT_THREAD_CACHE_TTL_MS) return;
    updateProjectThreadCache(path, (previous) => ({
      threads: Array.isArray(previous.threads) ? previous.threads : [],
      loading: true,
      error: '',
    }));
    getSidebarState({ cwd: path })
      .then((snapshot) => {
        updateProjectThreadCache(path, {
          threads: sidebarSnapshotThreads(snapshot),
          loadedAt: currentTimestampMillis('project thread cache loadedAt'),
          loading: false,
          error: '',
        });
      })
      .catch((error) => {
        updateProjectThreadCache(path, (previous) => ({
          threads: Array.isArray(previous.threads) ? previous.threads : [],
          loading: false,
          error: error?.message || String(error),
        }));
      });
  }, [updateProjectThreadCache]);
  useEffect(() => {
    const activeKey = projectTreeKey(activeProjectPath);
    if (!activeKey) return;
    if (store?.bootstrapStatus !== 'ready') return;
    const loadingKey = projectTreeKey(store?.chatSurfaceLoadingCwd);
    if (loadingKey && loadingKey === activeKey) return;
    // 只在全局 bootstrap 完成后同步可信来源，避免刷新初期把临时 activeProject 的空列表写成新鲜缓存。
    const sidebarThreadCache = store?.sidebarThreadsByProject && typeof store.sidebarThreadsByProject === 'object' ? store.sidebarThreadsByProject : {};
    const hasSidebarThreads = Object.prototype.hasOwnProperty.call(sidebarThreadCache, activeKey);
    const sidebarThreads = hasSidebarThreads ? sidebarThreadCache[activeKey] : null;
    const hasStoreThreads = Array.isArray(store?.threads) && store.threads.length > 0;
    if (!hasSidebarThreads && !hasStoreThreads) return;
    const sourceThreads = hasSidebarThreads ? sidebarThreads : store?.threads;
    updateProjectThreadCache(activeProjectPath, {
      threads: Array.isArray(sourceThreads) ? sourceThreads : [],
      loadedAt: currentTimestampMillis('project thread cache loadedAt'),
      loading: false,
      error: '',
    });
  }, [activeProjectPath, store?.bootstrapStatus, store?.chatSurfaceLoadingCwd, store?.sidebarThreadsByProject, store?.threads, updateProjectThreadCache]);
  const addProject = () => runUIAction(async () => {
    const added = await store?.addProjectFromPicker?.();
    if (added) setActivePage('chat');
  }, actionOptions);
  const selectProject = (path) => {
    if (!path) return;
    const hasExplicitState = Object.prototype.hasOwnProperty.call(expandedProjects, path);
    const currentExpanded = hasExplicitState ? !!expandedProjects[path] : projectTreeKey(path) === projectTreeKey(activeProjectPath);
    const nextExpanded = !currentExpanded;
    setExpandedProjects((current) => {
      return {
        ...current,
        [path]: nextExpanded,
      };
    });
    if (nextExpanded) refreshProjectThreadCache(path);
  };
  const startProjectThread = (path, event) => {
    event?.stopPropagation?.();
    if (!path) return;
    runUIAction(async () => {
      if (projectTreeKey(path) !== projectTreeKey(activeProjectPath)) {
        const switched = await store?.setActiveProjectPath?.(path);
        if (switched === false) return;
      }
      store?.newThread?.();
      setActivePage('chat');
    }, actionOptions);
  };
  const selectThread = (thread, path) => {
    const threadId = typeof thread === 'object' ? thread?.id : thread;
    if (!threadId) return;
    const openingStarted = store?.beginOpeningThread?.(thread);
    setActivePage('chat');
    runUIAction(async () => {
      if (path && projectTreeKey(path) !== projectTreeKey(activeProjectPath)) {
        const switched = await store?.setActiveProjectPath?.(path, {
          preserveActiveThreadId: true,
        });
        if (switched === false) {
          if (openingStarted) void store?.setActiveThread?.('');
          return;
        }
      }
      return store?.setActiveThread?.(threadId);
    }, actionOptions);
  };
  return (
    <section className="sidebar-project-tree" aria-label={copy.projects}>
      <div className="sidebar-section-heading">
        <span className="sidebar-section-title">
          <span>{copy.projects}</span>
        </span>
        <button type="button" className="sidebar-icon-action" aria-label={copy.addProject} onClick={addProject}>
          <Plus size={16} aria-hidden="true" />
        </button>
      </div>
      <div className="sidebar-tree-root">
        {projectItems.map((item) => {
          const projectKey = projectTreeKey(item.path);
          const isActiveProject = Boolean(item.path && projectKey === projectTreeKey(activeProjectPath));
          const cacheEntry = projectThreadCache[projectKey];
          const sidebarThreads = store?.sidebarThreadsByProject?.[projectKey];
          const sourceThreads = cacheEntry
            ? (isActiveProject ? mergeProjectThreadSources(cacheEntry.threads, store?.threads) : cacheEntry.threads)
            : (isActiveProject ? mergeProjectThreadSources(sidebarThreads, store?.threads) : (Array.isArray(sidebarThreads) ? sidebarThreads : []));
          const projectThreads = projectThreadItems(sourceThreads, item.path, cacheEntry ? item.path : activeProjectPath, {
            allowMissingCwdFallback: !cacheEntry,
          });
          const hasExplicitState = Object.prototype.hasOwnProperty.call(expandedProjects, item.path);
          const isExpanded = hasExplicitState ? !!expandedProjects[item.path] : isActiveProject;
          const visibleThreads = isExpanded ? projectThreads : [];
          const showLoading = isExpanded && cacheEntry?.loading && visibleThreads.length === 0;
          const newProjectThreadLabel = `${copy.newChat} ${item.name}`;
          return (
            <div className="sidebar-tree-project" key={item.path || item.name}>
              <div className="sidebar-project-header">
                <button
                  type="button"
                  className={`sidebar-tree-folder${isActiveProject ? ' active' : ''}`}
                  onClick={() => selectProject(item.path)}
                  aria-expanded={isExpanded}
                  aria-label={`${copy.selectProject} ${item.name}`}
                >
                  <Folder size={18} aria-hidden="true" />
                  <span>{item.name}</span>
                </button>
                <button
                  type="button"
                  className="sidebar-icon-action sidebar-project-new-thread"
                  onClick={(event) => startProjectThread(item.path, event)}
                  aria-label={newProjectThreadLabel}
                  title={newProjectThreadLabel}
                >
                  <SquarePlus size={16} aria-hidden="true" />
                </button>
              </div>
              <ul className="sidebar-project-thread-list" aria-label={`${item.name} ${copy.projectChatsSuffix}`}>
                {visibleThreads.length > 0 ? visibleThreads.map((thread) => {
                  const label = projectThreadLabel(thread);
                  const active = thread.id === store?.activeThreadId;
                  if (thread.id) {
                    return (
                      <SidebarThreadRow
                        key={thread.id}
                        active={active}
                        copy={copy}
                        label={label}
                        onSelect={() => selectThread(thread, item.path)}
                        openLabel={`${copy.openProjectThread}：${label}`}
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
                        onClick={() => selectThread(thread, item.path)}
                        aria-label={`${copy.openProjectThread}：${label}`}
                        title={label}
                      >
                        <span className="sidebar-thread-title">{label}</span>
                        {thread.updatedAt && (
                          <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt, copy.workbench.relativeTime)}</span>
                        )}
                      </button>
                    </li>
                  );
                }) : showLoading ? (
                  <li className="sidebar-project-thread-empty">{copy.loadingThreads}</li>
                ) : (
                  isExpanded ? <li className="sidebar-project-thread-empty">{copy.emptyThreads}</li> : null
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
  copy = APP_COPY.zh,
  isOpen = false,
  locale = 'zh',
  sidebarWidth = WORKBENCH_SIDEBAR_DEFAULT_WIDTH,
  onSidebarResizeKeyDown,
  onSidebarResizeStart,
  setActivePage,
  store,
  projectPath,
  theme,
  toggleLocale,
  toggleTheme,
  memorySimilarCount = 0,
  onCloseSidebar,
}) {
  const actionOptions = uiActionOptions(store);
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  const isDark = theme === COLOR_THEMES.dark;
  const ThemeIcon = isDark ? Sun : Moon;
  const themeLabel = isDark ? copy.workbench.dayMode : copy.workbench.nightMode;
  const startNewChat = () => {
    setActivePage('chat');
    runUIAction(() => store?.newThread?.(), actionOptions);
  };

  return (
    <aside
      id="app-sidebar"
      className={`app-sidebar${isOpen ? ' is-open' : ''}`}
      data-testid="app-sidebar"
      aria-label={copy.workbench.ariaLabel}
      style={isOpen ? { marginLeft: 0 } : undefined}
    >
      <div className="sidebar-brand-row">
        <div className="sidebar-brand">
          <img src={suiyuanBrandIcon} alt="" aria-hidden="true" />
          <strong>{APP_BRAND_NAME}</strong>
        </div>
        <div className="sidebar-brand-actions" aria-label={copy.workbench.tools}>
          <button type="button" aria-label={copy.workbench.search} title={copy.workbench.search} onClick={() => setActivePage('observability')}>
            <Search size={19} aria-hidden="true" />
          </button>
          <button
            type="button"
            aria-label={copy.switchLanguage}
            title={copy.switchLanguage}
            onClick={toggleLocale}
          >
            <span aria-hidden="true">{locale.toUpperCase()}</span>
          </button>
          <button
            type="button"
            aria-label={copy.workbench.collapse}
            title={copy.workbench.collapse}
            onClick={onCloseSidebar}
          >
            <PanelLeftClose size={19} aria-hidden="true" />
          </button>
        </div>
      </div>
      <button
        type="button"
        className={`sidebar-new-chat ${activePage === 'chat' ? 'active' : ''}`}
        aria-label={copy.workbench.newChat}
        onClick={startNewChat}
      >
        <SquarePlus size={22} aria-hidden="true" />
        <span>{copy.workbench.newChat}</span>
      </button>
      <SidebarNavList
        copy={copy}
        items={primaryNavItems}
        activePage={activePage}
        setActivePage={setActivePage}
        memoryBadgeCount={0}
        testId="sidebar-nav"
        className="sidebar-primary-nav"
      />
      <SidebarNavList
        copy={copy}
        items={secondaryNavItems}
        activePage={activePage}
        setActivePage={setActivePage}
        memoryBadgeCount={memoryBadgeCount}
        testId="sidebar-secondary-nav"
        className="sidebar-secondary-nav"
      />
      <div className="sidebar-project-selector">
        <ProjectSelector copy={copy.workbench} store={store} projectPath={projectPath} />
      </div>
      <div className="sidebar-scrollable-content">
        <SidebarProjectTree copy={copy.workbench} projectPath={projectPath} setActivePage={setActivePage} store={store} />
        <SidebarTaskSummary copy={copy.workbench} store={store} setActivePage={setActivePage} />
      </div>
      <button
        type="button"
        className="sidebar-theme-toggle"
        onClick={toggleTheme}
        aria-label={`${copy.workbench.switchThemePrefix}${themeLabel}`}
      >
        <ThemeIcon size={16} aria-hidden="true" />
        <span>{themeLabel}</span>
      </button>
      <button
        type="button"
        className={`sidebar-settings ${activePage === 'settings' ? 'active' : ''}`}
        aria-label={copy.workbench.settings}
        onClick={() => setActivePage('settings')}
      >
        <SettingsIcon size={25} aria-hidden="true" />
        <span>{copy.workbench.settings}</span>
      </button>
      <button
        type="button"
        className="workbench-sidebar-resizer"
        data-testid="workbench-sidebar-resizer"
        role="separator"
        aria-label={copy.workbench.resize}
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

function SidebarTaskSummary({ copy = APP_COPY.zh.workbench, store, setActivePage }) {
  const tasks = taskThreadItems(store?.threads);
  const threadActions = useSidebarThreadActions(store);
  const actionOptions = uiActionOptions(store);
  const selectThread = (threadId) => {
    if (!threadId) return;
    setActivePage('chat');
    runUIAction(() => store?.setActiveThread?.(threadId), actionOptions);
  };
  return (
    <section className="sidebar-task-summary" aria-label={copy.task}>
      <h2>{copy.task}</h2>
      {tasks.length > 0 ? (
        <ul className="sidebar-task-list" aria-label={copy.taskDialogs}>
          {tasks.map((thread) => {
            const label = projectThreadLabel(thread);
            const active = thread.id === store?.activeThreadId;
            if (thread.id) {
              return (
                <SidebarThreadRow
                  key={thread.id}
                  active={active}
                  copy={copy}
                  label={label}
                  onSelect={() => selectThread(thread.id)}
                  openLabel={`${copy.openTask}：${label}`}
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
                  aria-label={`${copy.openTask}：${label}`}
                  title={label}
                >
                  <span className="sidebar-thread-title">{label}</span>
                  {thread.updatedAt && (
                    <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt, copy.workbench.relativeTime)}</span>
                  )}
                </button>
              </li>
            );
          })}
        </ul>
      ) : <p>{copy.emptyTasks}</p>}
    </section>
  );
}

export default App;
