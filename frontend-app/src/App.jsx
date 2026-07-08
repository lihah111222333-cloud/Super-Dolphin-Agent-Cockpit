import React, { useCallback, useEffect, useRef, useState } from 'react';
import { AppWindowFrame } from './AppWindowFrame.jsx';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { useShallow } from 'zustand/react/shallow';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { UITestMCPShell } from './devtools/UITestMCPShell.jsx';
import { checkAppUpdate, installLatestAppUpdate } from './shared/api/backendApi.js';
import {
  dashboardQueryKey,
  errorMessage,
  firstPresentText,
  optionalSettingsCwd,
  textValue,
  useDashboardFocusInvalidation,
} from './pages/shared/pageShared.js';
import { memoryPageService } from './pages/memory/services/memoryPageService.js';
import { APP_COPY, APP_LANGUAGE_STORAGE_KEY, initialAppLocale } from './shared/i18n/appI18n.js';
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



const DASHBOARD_QUERY_STALE_MS = 30_000;

const UPDATE_CHECK_DELAY_MS = 2_000;
const UPDATE_BANNER_DISMISSED_PREFIX = 'super-dolphin-update-dismissed:';

const DASHBOARD_QUERY_GC_MS = 10 * 60_000;

export const APP_PROFILER_ID = 'App';

const THEME_STORAGE_KEY = 'super-dolphin-theme';
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

function workflowSubpageLabel(workflowView, copy) {
  if (workflowView === 'templates') return copy.templatePageTitle;
  if (workflowView === 'freeDesign') return copy.freeDesignPageTitle;
  return '';
}

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
  const groups = response?.overview?.health?.similarGroups;
  return Array.isArray(groups) ? groups.length : 0;
}

function memoryBadgeQueryKey(memoryCwd) {
  return ['memory-badge', memoryCwd];
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

async function loadMemoryBadgeDashboard({ addWarning, memoryCwd }) {
  try {
    return await memoryPageService.loadBadgeDashboard(memoryCwd);
  } catch (error) {
    addWarning('warn', 'memory.badge.refresh.failed', { error: errorMessage(error) });
    throw error;
  }
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
    queryKey: memoryBadgeQueryKey(memoryCwd),
    queryFn: () => loadMemoryBadgeDashboard({ addWarning, memoryCwd }),
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

function updateDismissed(version) {
  return version && requiredAppStoragePort('update banner storage').get(updateDismissedKey(version)) === '1';
}

async function runAppUpdateCheck({ isCancelled, setState }) {
  setState((current) => (current.update ? current : { ...current, status: 'checking' }));
  try {
    const result = await checkAppUpdate();
    if (isCancelled() || !result?.enabled || !result?.available) return;
    const version = updateVersionFromResult(result);
    if (updateDismissed(version)) return;
    setState({ status: 'available', update: { ...result, version }, message: '' });
  } catch (error) {
    if (!isCancelled()) console.info('[frontend-app] background update check failed', error);
  } finally {
    if (!isCancelled()) {
      setState((current) => (current.status === 'checking' ? { ...current, status: 'idle' } : current));
    }
  }
}

function useAppUpdateBanner(skipBootstrap) {
  const [state, setState] = useState({ status: 'idle', update: null, message: '' });

  useEffect(() => {
    if (skipBootstrap) return undefined;
    let cancelled = false;
    const timer = window.setTimeout(() => {
      void runAppUpdateCheck({ isCancelled: () => cancelled, setState });
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
  const appStore = useClientStore();
  const routeBootstrapPending = !skipBootstrap && !['ready', 'failed'].includes(store.bootstrapStatus);
  useActivePageHistory(store.activePage, store.setActivePage, routeBootstrapPending);
  useAppBootstrap(store.bootstrap, skipBootstrap);
  const projectPath = store.activeProject && store.activeProject !== '.' ? store.activeProject : store.cwd || '未选择项目';
  const memoryBadge = useMemoryBadgeState(appStore, projectPath);
  const { theme, toggleTheme } = useColorTheme();
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const updateBanner = useAppUpdateBanner(skipBootstrap);
  return { memoryBadge, projectPath, theme, toggleTheme, rightPanelOpen, setRightPanelOpen, updateBanner };
}

function AppWindow({ shell, store }) {
  const routeStore = useClientStore();
  const {
    memoryBadge,
    projectPath,
    rightPanelOpen,
    setRightPanelOpen,
    theme,
    toggleTheme,
    updateBanner,
  } = shell;
  const { copy, locale, toggleLocale } = useAppLanguage();
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
  return (
    <AppWindowFrame
      frame={{
        closeSidebar,
        copy,
        currentPageLabelOverride,
        locale,
        memoryBadge,
        onSidebarResizeKeyDown: handleWorkbenchSidebarResizeKeyDown,
        onSidebarResizeStart: beginWorkbenchSidebarResize,
        onWorkflowViewChange: handleWorkflowViewChange,
        openSidebar: () => setSidebarOpen(true),
        projectPath,
        rightPanelOpen,
        setActivePage: setActivePageFromSidebar,
        setRightPanelOpen,
        sidebarOpen,
        store: routeStore,
        theme,
        toggleLocale,
        toggleSidebar: () => setSidebarOpen((open) => !open),
        toggleTheme,
        updateBanner,
        workbenchSidebarWidth,
      }}
    />
  );
}

function AppShell({ skipBootstrap = false, uiTestMCPMode = false }) {
  const store = useClientStore(useShallow(selectAppShellStore));
  const shell = useAppShellState(store, skipBootstrap || uiTestMCPMode);
  if (uiTestMCPMode) return <UITestMCPShell />;
  return <AppWindow shell={shell} store={store} />;
}

function App(props) {
  const [queryClient] = useState(createDashboardQueryClient);
  return (
    <QueryClientProvider client={queryClient}>
      <AppShell {...props} />
    </QueryClientProvider>
  );
}

export default App;
