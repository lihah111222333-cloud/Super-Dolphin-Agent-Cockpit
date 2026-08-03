import React, { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { QueryClient, QueryClientProvider, useQuery, useQueryClient } from '@tanstack/react-query';
import { UNSAFE_PortalProvider } from 'react-aria';
import { ConfigProvider as AntdConfigProvider } from 'antd';
import XProvider from '@ant-design/x/es/x-provider';
import { useShallow } from 'zustand/react/shallow';
import { useClientStore } from './entities/client/model/useClientStore.js';
import { checkAppUpdate, installLatestAppUpdate } from './shared/api/backendApi.js';
import { recoveryActionMessageFromRPCError } from './shared/recovery/recoveryFailure.js';
import { requiredAppStoragePort } from './shared/api/browser/browserStorage.js';
import { UITestMCPShell } from './devtools/UITestMCPShell.jsx';
import {
  dashboardQueryKey,
  errorMessage,
  optionalSettingsCwd,
  useDashboardFocusInvalidation,
} from './pages/shared/pageShared.js';
import { memoryPageService } from './pages/memory/services/memoryPageService.js';
import { APP_COPY, APP_LANGUAGE_STORAGE_KEY, initialAppLocale } from './shared/i18n/appI18n.js';
import { runBackgroundAction } from './shared/ui/runUIAction.js';
import { requiredOverlayRoot } from './shared/ui/OverlayPortal.jsx';
import './AppChrome.css';
import './AppShell.css';
import {
  appPageFromPathname,
  appRouteForPage,
  normalizeAppPathname,
  selectAppShellStore,
} from './app/appShellModel.js';
import { AppearanceProvider } from './app/appearance/AppearanceProvider.jsx';
import { applyAppearanceToElement, createBrowserAppearanceStore } from './app/appearance/appearanceStore.js';
import { SuiyuanAppWindow } from './app/shell/SuiyuanAppWindow.jsx';
import { appShortcutPlatform } from './app/shell/appShortcutPlatform.js';
import { antdLocaleFor, antdThemeConfig } from './app/antdTheme.js';
import { updateVersionFromResult } from './app/shell/appUpdateVersion.js';
import { createShellLayoutStore } from './app/shell/model/useShellLayoutStore.js';
import { appCommandPreferencePort } from './app/commands/appCommandPreferencePort.js';
import { APP_COMMAND_REGISTRY } from './app/commands/appCommandRegistry.js';
import { useShortcutSettings } from './features/shortcut-settings/hooks/useShortcutSettings.js';



const DASHBOARD_QUERY_STALE_MS = 30_000;

const UPDATE_CHECK_DELAY_MS = 2_000;
const UPDATE_BANNER_DISMISSED_PREFIX = 'super-dolphin-update-dismissed:';

const DASHBOARD_QUERY_GC_MS = 10 * 60_000;

export const APP_PROFILER_ID = 'App';

function appPageFromLocation() {
  if (typeof window === 'undefined') return 'chat';
  return appPageFromPathname(window.location?.pathname) || 'chat';
}

function hasExplicitAppPageRoute() {
  if (typeof window === 'undefined') return false;
  const path = normalizeAppPathname(window.location?.pathname);
  return path !== '/' && Boolean(appPageFromPathname(path));
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
  } catch (cause) {
    addWarning('warn', 'memory.badge.refresh.failed', { error: 'background action failure; see Health diagnostic ID' });
    throw cause;
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
    queryFn: () => runBackgroundAction('memory.badge.load', () => loadMemoryBadgeDashboard({ addWarning, memoryCwd })),
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
    runBackgroundAction('app.bootstrap.background', bootstrap);
    return undefined;
  }, [bootstrap, skipBootstrap]);
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
    if (!isCancelled()) {
      const recoveryMessage = recoveryActionMessageFromRPCError(error);
      if (recoveryMessage) setState({ status: 'recovery', update: null, message: recoveryMessage });
      else console.info('[frontend-app] background update check failed');
    }
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
      const recoveryMessage = recoveryActionMessageFromRPCError(error);
      setState((current) => ({ ...current, status: 'available', message: recoveryMessage || `更新失败：${errorMessage(error)}` }));
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

function useAppShellState(store, skipBootstrap, appearanceState) {
  const routeBootstrapPending = !skipBootstrap && !['ready', 'failed'].includes(store.bootstrapStatus);
  useActivePageHistory(store.activePage, store.setActivePage, routeBootstrapPending);
  useAppBootstrap(store.bootstrap, skipBootstrap);
  const projectPath = store.activeProject && store.activeProject !== '.' ? store.activeProject : store.cwd || '未选择项目';
  const memoryBadge = useMemoryBadgeState(store, projectPath);
  const appearance = appearanceState;
  const toggleTheme = useCallback(() => {
    appearance.setThemeMode(appearance.resolvedTheme === 'dark' ? 'light' : 'dark');
  }, [appearance]);
  const [rightPanelOpen, setRightPanelOpen] = useState(false);
  const updateBanner = useAppUpdateBanner(skipBootstrap);
  return {
    appearance,
    memoryBadge,
    projectPath,
    theme: appearance.resolvedTheme,
    toggleTheme,
    rightPanelOpen,
    setRightPanelOpen,
    updateBanner,
  };
}

function shortcutSettingsCwd(store) {
  const cwd = store.activeProject && store.activeProject !== '.' ? store.activeProject : store.cwd;
  if (cwd === '') return '';
  if (typeof cwd !== 'string' || cwd.trim() !== cwd) throw new Error('shortcut settings cwd is required');
  return cwd;
}

function ConfiguredAppWindow({ language, shortcutCwd, ...props }) {
  const shortcutController = useShortcutSettings({
    copy: language.copy.commands,
    cwd: shortcutCwd,
    getPreference: appCommandPreferencePort.getPreference,
    platform: appShortcutPlatform(),
    registry: APP_COMMAND_REGISTRY,
    setPreference: appCommandPreferencePort.setPreference,
  });
  return <SuiyuanAppWindow {...props} language={language} shortcutController={shortcutController} />;
}

function AppWindowShortcutBoundary({ language, shell, shellLayoutStore, store }) {
  const shortcutCwd = shortcutSettingsCwd(store);
  return <ConfiguredAppWindow language={language} shell={shell} shellLayoutStore={shellLayoutStore} shortcutCwd={shortcutCwd} store={store} />;
}

function AppShell({ appearance, shellLayoutStorage, skipBootstrap = false, uiTestMCPMode = false }) {
  const store = useClientStore(useShallow(selectAppShellStore));
  const shell = useAppShellState(store, skipBootstrap || uiTestMCPMode, appearance);
  const language = useAppLanguage();
  const [shellLayoutStore] = useState(() => createShellLayoutStore({
    storage: shellLayoutStorage === undefined
      ? requiredAppStoragePort('shell layout storage')
      : shellLayoutStorage,
  }));
  const overlayRoot = requiredOverlayRoot();
  useLayoutEffect(() => {
    const projection = applyAppearanceToElement(overlayRoot, {
      ...shell.appearance,
      resolvedTheme: shell.theme,
    });
    return () => {
      Object.entries(projection.attributes).forEach(([name, value]) => {
        if (overlayRoot.getAttribute(name) === value) overlayRoot.removeAttribute(name);
      });
      Object.entries(projection.styles).forEach(([name, value]) => {
        if (overlayRoot.style.getPropertyValue(name) === value) overlayRoot.style.removeProperty(name);
      });
    };
  }, [overlayRoot, shell.appearance, shell.theme]);
  // Ant Design / Ant Design X 主题跟随应用主题（深色/浅色 Command Center 基线），
  // 仅提供 UI 组件与视觉 token，不接管任何请求层、会话状态或业务契约。
  const antdTheme = useMemo(
    () => antdThemeConfig(shell.theme, shell.appearance.accent),
    [shell.appearance.accent, shell.theme],
  );
  const antdLocale = useMemo(() => antdLocaleFor(language.locale), [language.locale]);
  return (
    <AntdConfigProvider theme={antdTheme} locale={antdLocale}>
      <XProvider theme={antdTheme}>
        <UNSAFE_PortalProvider getContainer={() => overlayRoot}>
          {uiTestMCPMode
            ? <UITestMCPShell />
            : <AppWindowShortcutBoundary language={language} shell={shell} shellLayoutStore={shellLayoutStore} store={store} />}
        </UNSAFE_PortalProvider>
      </XProvider>
    </AntdConfigProvider>
  );
}

function App(props) {
  const [queryClient] = useState(createDashboardQueryClient);
  const [appearanceStore] = useState(
    () => props.appearanceStore || createBrowserAppearanceStore(),
  );
  return (
    <AppearanceProvider store={appearanceStore}>
      {(appearance) => (
        <QueryClientProvider client={queryClient}>
          <AppShell {...props} appearance={appearance} />
        </QueryClientProvider>
      )}
    </AppearanceProvider>
  );
}

export default App;
