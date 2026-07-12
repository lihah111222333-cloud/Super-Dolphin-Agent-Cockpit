import React, { Suspense, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { UNSAFE_PortalProvider } from 'react-aria';
import { useShallow } from 'zustand/react/shallow';
import {
  Bell,
  Brain,
  CircleUserRound,
  Clock3,
  Database,
  FolderOpen,
  Menu,
  MessageSquareText,
  Moon,
  PanelLeftClose,
  PanelLeftOpen,
  Puzzle,
  Sailboat,
  Settings as SettingsIcon,
  SlidersHorizontal,
  Plus,
  Sun,
  X,
} from 'lucide-react';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { UITestMCPShell } from './devtools/UITestMCPShell.jsx';
import { ActivePageContent, PageLoadingFallback } from './AppRoutes.jsx';
import { SidebarProjectTree as ChatSidebarProjectTree } from './WorkbenchSidebarProjectTree.jsx';
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
import { APP_BRAND_NAME, APP_COPY, APP_LANGUAGE_STORAGE_KEY, initialAppLocale } from './shared/i18n/appI18n.js';
import { runUIAction } from './shared/ui/runUIAction.js';
import { requiredOverlayRoot } from './shared/ui/OverlayPortal.jsx';
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
} from './app/appShellModel.js';
import { createShellLayoutStore } from './app/shell/model/useShellLayoutStore.js';
import { APP_COMMAND_IDS, defineAppCommandRegistry } from './app/commands/appCommandRegistry.js';
import { createAppCommandRuntime } from './app/commands/appCommandRuntime.js';
import { useAppCommandDispatcher } from './app/commands/useAppCommandDispatcher.js';



const DASHBOARD_QUERY_STALE_MS = 30_000;

const UPDATE_CHECK_DELAY_MS = 2_000;
const UPDATE_BANNER_DISMISSED_PREFIX = 'super-dolphin-update-dismissed:';

const DASHBOARD_QUERY_GC_MS = 10 * 60_000;

export const APP_PROFILER_ID = 'App';

const THEME_STORAGE_KEY = 'super-dolphin-theme';
const SUIYUAN_NAV_ITEMS = Object.freeze([
  { id: 'chat', label: 'Chat', labelKey: 'chat', icon: MessageSquareText },
  { id: 'skills', label: 'Plugins', labelKey: 'skills', icon: Puzzle },
  { id: 'workflows', label: 'Automation', labelKey: 'workflows', icon: SlidersHorizontal },
  { id: 'prompts', label: 'Roles', labelKey: 'prompts', icon: CircleUserRound },
  { id: 'files', label: 'Files', labelKey: 'files', icon: FolderOpen },
  { id: 'memory', label: 'Memory', labelKey: 'memory', icon: Brain },
  { id: 'observability', label: 'Logs', labelKey: 'observability', icon: Database },
]);
const APP_COMMAND_REGISTRY = defineAppCommandRegistry([
  {
    id: APP_COMMAND_IDS.PALETTE_OPEN,
    labelKey: 'commands.palette.open',
    section: 'application',
    defaultShortcut: { key: 'k', mod: true },
  },
  {
    id: APP_COMMAND_IDS.CHAT_NEW,
    labelKey: 'commands.chat.new',
    section: 'chat',
    defaultShortcut: { key: 'n', mod: true },
  },
  {
    id: APP_COMMAND_IDS.SETTINGS_OPEN,
    labelKey: 'commands.settings.open',
    section: 'navigation',
    defaultShortcut: { key: ',', mod: true },
  },
  {
    id: APP_COMMAND_IDS.SIDEBAR_TOGGLE,
    labelKey: 'commands.sidebar.toggle',
    section: 'navigation',
    defaultShortcut: { key: 'b', mod: true },
  },
  {
    id: APP_COMMAND_IDS.TURN_INTERRUPT,
    labelKey: 'commands.turn.interrupt',
    section: 'chat',
    defaultShortcut: { key: 'Escape' },
  },
]);
const EMPTY_SHORTCUT_OVERRIDES = Object.freeze({});

function appShortcutPlatform() {
  if (typeof navigator === 'undefined') throw new Error('browser shortcut platform is unavailable');
  const browserPlatform = `${String(navigator.platform)} ${String(navigator.userAgent)}`.toLowerCase();
  if (browserPlatform.includes('mac')) return 'darwin';
  if (browserPlatform.includes('win')) return 'win32';
  if (browserPlatform.includes('linux')) return 'linux';
  const runtimePlatform = globalThis.process?.platform;
  if (['darwin', 'linux', 'win32'].includes(runtimePlatform)) return runtimePlatform;
  throw new Error(`unsupported browser shortcut platform: ${browserPlatform || 'unknown'}`);
}

function hasOpenLocalEscapeSurface() {
  return Boolean(document.querySelector('dialog[open], [role="dialog"], [role="menu"], [role="listbox"], [data-escape-scope="local"]'));
}

function workflowSubpageLabel(workflowView, copy) {
  if (workflowView === 'templates') return copy.templatePageTitle;
  if (workflowView === 'freeDesign') return copy.freeDesignPageTitle;
  return '';
}

function appPageTitleLabel(page, copy) {
  if (page === 'workflows') return copy.workflow.title || copy.nav.workflows;
  if (page === 'prompts') return copy.prompts.title || copy.nav.prompts;
  if (page === 'files') return copy.files.title || copy.nav.files;
  if (page === 'memory') return copy.memory.title || copy.nav.memory;
  if (page === 'observability') return copy.observability.title || copy.nav.observability;
  if (page === 'settings') return copy.settings.title || copy.nav.settings;
  if (page === 'skills') return copy.skills.title || copy.nav.skills;
  return copy.nav[page] || copy.nav.chat;
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
  if (
    !storage
    || typeof storage.getItem !== 'function'
    || typeof storage.setItem !== 'function'
    || typeof storage.removeItem !== 'function'
  ) {
    throw new Error(`${label} is unavailable`);
  }
  return {
    get(key) {
      return storage.getItem(key);
    },
    set(key, value) {
      storage.setItem(key, value);
    },
    remove(key) {
      storage.removeItem(key);
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

function uiActionOptions(store) {
  return {
    onError: (error) => {
      store?.addWarning?.('error', 'ui.action.failed', { error: errorMessage(error) });
    },
  };
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

function SuiyuanNavButton({ activePage, copy, item, memoryBadgeCount, setActivePage }) {
  const Icon = item.icon;
  const active = activePage === item.id;
  const label = copy.nav[item.labelKey] || item.label;
  const badgeCount = item.id === 'memory' ? memoryBadgeCount : 0;
  return (
    <button
      type="button"
      className={`suiyuan-nav-item${active ? ' active' : ''}`}
      onClick={() => setActivePage(item.id)}
      aria-label={label}
      aria-current={active ? 'page' : undefined}
    >
      <Icon size={16} aria-hidden="true" />
      <span>{label}</span>
      {badgeCount > 0 ? <i aria-hidden="true" title={`${badgeCount} ${copy.workbench.memoryBadgeTitle}`} /> : null}
    </button>
  );
}

function SuiyuanChatNavGroup({ copy, item, projectPath, sidebar, store }) {
  const { activePage, setActivePage } = sidebar;
  return (
    <div className="suiyuan-chat-nav-group">
      <SuiyuanNavButton
        activePage={activePage}
        copy={copy}
        item={item}
        memoryBadgeCount={0}
        setActivePage={setActivePage}
      />
      {activePage === 'chat' ? (
        <div className="suiyuan-chat-project-tree">
          <ChatSidebarProjectTree copy={copy.workbench} projectPath={projectPath} setActivePage={setActivePage} store={store} />
        </div>
      ) : null}
    </div>
  );
}

function SuiyuanSidebar({ copy, projectPath, sidebar, store }) {
  const { activePage, closeSidebar, isOpen, memorySimilarCount, setActivePage, startNewChat } = sidebar;
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);
  return (
    <aside
      id="app-sidebar"
      className={`app-sidebar suiyuan-sidebar${isOpen ? ' is-open' : ''}`}
      data-testid="app-sidebar"
      aria-label={copy.workbench.ariaLabel}
    >
      <div className="suiyuan-brand-block">
        <span className="suiyuan-brand-light-mark" data-testid="suiyuan-brand-light-logo" aria-hidden="true">
          <Sailboat size={14} strokeWidth={2} />
        </span>
        <img className="suiyuan-brand-dark-mark" data-testid="suiyuan-brand-dark-logo" src={suiyuanBrandIcon} alt="" aria-hidden="true" />
        <div className="suiyuan-brand-meta">
          <strong>{APP_BRAND_NAME}</strong>
          <span>AI Canvas</span>
        </div>
        <button
          type="button"
          className="suiyuan-sidebar-collapse"
          aria-label={copy.workbench.collapse}
          title={copy.workbench.collapse}
          aria-controls="app-sidebar"
          onClick={closeSidebar}
        >
          <PanelLeftClose size={17} aria-hidden="true" />
        </button>
      </div>
      <button type="button" className="suiyuan-new-chat" aria-label={copy.workbench.newChat} onClick={startNewChat}>
        <Plus size={18} aria-hidden="true" />
        <span>{copy.workbench.newChat}</span>
      </button>
      <nav className="suiyuan-nav" data-testid="sidebar-nav" aria-label="Suiyuan navigation">
        <SuiyuanChatNavGroup
          copy={copy}
          item={SUIYUAN_NAV_ITEMS[0]}
          projectPath={projectPath}
          sidebar={sidebar}
          store={store}
        />
        {SUIYUAN_NAV_ITEMS.slice(1).map((item) => (
          <SuiyuanNavButton
            key={item.id}
            activePage={activePage}
            copy={copy}
            item={item}
            memoryBadgeCount={memoryBadgeCount}
            setActivePage={setActivePage}
          />
        ))}
      </nav>
      <div className="suiyuan-sidebar-footer">
        <button
          type="button"
          className={`suiyuan-footer-item${activePage === 'settings' ? ' active' : ''}`}
          aria-label={copy.workbench.settings}
          onClick={() => setActivePage('settings')}
        >
          <SettingsIcon size={15} aria-hidden="true" />
          <span>{copy.workbench.settings}</span>
        </button>
      </div>
    </aside>
  );
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

function useBoundAppCommandRuntime({ setActivePage, setPaletteOpen, setSidebarOpen, startNewChat, store }) {
  return useMemo(() => createAppCommandRuntime({
    registry: APP_COMMAND_REGISTRY,
    bindings: {
      [APP_COMMAND_IDS.PALETTE_OPEN]: {
        run: () => setPaletteOpen(true),
      },
      [APP_COMMAND_IDS.CHAT_NEW]: {
        run: startNewChat,
      },
      [APP_COMMAND_IDS.SETTINGS_OPEN]: {
        run: () => setActivePage('settings'),
      },
      [APP_COMMAND_IDS.SIDEBAR_TOGGLE]: {
        run: () => setSidebarOpen((open) => !open),
      },
      [APP_COMMAND_IDS.TURN_INTERRUPT]: {
        run: () => runUIAction(() => store.interruptActiveThread(), uiActionOptions(store)),
        canExecute: () => store.hasActiveThreadActions() && !hasOpenLocalEscapeSurface(),
        disabledReason: '当前没有可中断任务',
      },
    },
    overrides: EMPTY_SHORTCUT_OVERRIDES,
    platform: appShortcutPlatform(),
  }), [setActivePage, setPaletteOpen, setSidebarOpen, startNewChat, store]);
}

function AppWindow({ shell, shellLayoutStore, store }) {
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
  const [paletteOpen, setPaletteOpen] = useState(false);
  const SidebarToggleIcon = sidebarOpen ? X : Menu;
  const activeLabel = appPageTitleLabel(store.activePage, copy);
  const currentPageLabel = currentPageLabelOverride || activeLabel;
  const isDark = theme === COLOR_THEMES.dark;
  const ThemeIcon = isDark ? Sun : Moon;
  const themeLabel = isDark ? copy.workbench.dayMode : copy.workbench.nightMode;
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
  const startNewChat = useCallback(() => {
    setActivePageFromSidebar('chat');
    runUIAction(() => store?.newThread?.(), uiActionOptions(store));
  }, [setActivePageFromSidebar, store]);
  const commandRuntime = useBoundAppCommandRuntime({
    setActivePage: setActivePageFromSidebar,
    setPaletteOpen,
    setSidebarOpen,
    startNewChat,
    store,
  });
  useAppCommandDispatcher({ runtime: commandRuntime });
  return (
    <div className={`sa-window suiyuan-shell${sidebarOpen ? ' sidebar-open' : ' sidebar-collapsed'}`} data-command-palette-open={paletteOpen} data-theme={theme} data-testid="frontend-app">
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
      <div className="sa-body suiyuan-shell-body">
        {!sidebarOpen ? (
          <button
            type="button"
            className="sidebar-expand-trigger"
            aria-label={copy.workbench.expand}
            title={copy.workbench.expand}
            onClick={() => setSidebarOpen(true)}
          >
            <PanelLeftOpen size={20} aria-hidden="true" />
          </button>
        ) : null}
        <SuiyuanSidebar
          copy={copy}
          projectPath={projectPath}
          sidebar={{
            activePage: store.activePage,
            closeSidebar,
            isOpen: sidebarOpen,
            memorySimilarCount: memoryBadge.memorySimilarCount,
            setActivePage: setActivePageFromSidebar,
            startNewChat,
          }}
          store={store}
        />
        <main className="sa-main suiyuan-main">
          <header className="suiyuan-top-appbar" aria-label="Suiyuan app bar">
            <div className="suiyuan-appbar-title">
              <span>{copy.currentPagePrefix}</span>
              <h1>{currentPageLabel}</h1>
            </div>
            <div className="suiyuan-appbar-actions" aria-label="Workspace actions">
              <button type="button" className="suiyuan-icon-action" aria-label={copy.workbench.notifications} title={copy.workbench.notifications} onClick={() => setActivePageFromSidebar('observability')}>
                <Bell size={15} aria-hidden="true" />
              </button>
              <button type="button" className="suiyuan-icon-action" aria-label={copy.workbench.history} title={copy.workbench.history} onClick={() => setActivePageFromSidebar('chat')}>
                <Clock3 size={15} aria-hidden="true" />
              </button>
              <button type="button" className="suiyuan-icon-action" aria-label={`${copy.workbench.switchThemePrefix}${themeLabel}`} title={themeLabel} onClick={toggleTheme}>
                <ThemeIcon size={15} aria-hidden="true" />
              </button>
              <button type="button" className="suiyuan-locale-action" aria-label={copy.switchLanguage} title={copy.switchLanguage} onClick={toggleLocale}>
                {locale.toUpperCase()}
              </button>
              <button type="button" className="suiyuan-profile-action" aria-label={copy.workbench.settings} title={copy.workbench.settings} onClick={() => setActivePageFromSidebar('settings')}>
                <CircleUserRound size={16} aria-hidden="true" />
              </button>
            </div>
          </header>
          <AppUpdateBanner copy={copy.update} updateBanner={updateBanner} />
          <div className="suiyuan-main-canvas">
            <Suspense fallback={<PageLoadingFallback />}>
              <ActivePageContent
                activePage={store.activePage}
                copy={copy}
                store={routeStore}
                projectPath={projectPath}
                memoryRevision={memoryBadge.memoryRevision}
                setMemoryPageSimilarCount={memoryBadge.setMemoryPageSimilarCount}
                onWorkflowViewChange={handleWorkflowViewChange}
                rightPanelOpen={rightPanelOpen}
                shellLayoutStore={shellLayoutStore}
                setRightPanelOpen={setRightPanelOpen}
              />
            </Suspense>
          </div>
        </main>
      </div>
    </div>
  );
}

function AppShell({ shellLayoutStorage, skipBootstrap = false, uiTestMCPMode = false }) {
  const store = useClientStore(useShallow(selectAppShellStore));
  const shell = useAppShellState(store, skipBootstrap || uiTestMCPMode);
  const [shellLayoutStore] = useState(() => createShellLayoutStore({
    storage: shellLayoutStorage === undefined
      ? requiredAppStoragePort('shell layout storage')
      : shellLayoutStorage,
  }));
  const overlayRoot = requiredOverlayRoot();
  useLayoutEffect(() => {
    overlayRoot.setAttribute('data-theme', shell.theme);
    return () => {
      if (overlayRoot.getAttribute('data-theme') === shell.theme) {
        overlayRoot.removeAttribute('data-theme');
      }
    };
  }, [overlayRoot, shell.theme]);
  return (
    <UNSAFE_PortalProvider getContainer={() => overlayRoot}>
      {uiTestMCPMode
        ? <UITestMCPShell />
        : <AppWindow shell={shell} shellLayoutStore={shellLayoutStore} store={store} />}
    </UNSAFE_PortalProvider>
  );
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
