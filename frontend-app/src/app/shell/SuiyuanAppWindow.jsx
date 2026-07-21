import React, { Suspense, useCallback, useMemo, useState } from 'react';
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
  HelpCircle,
} from 'lucide-react';
import { ActivePageContent, PageLoadingFallback } from '../../AppRoutes.jsx';
import { SidebarProjectTree as ChatSidebarProjectTree } from '../../WorkbenchSidebarProjectTree.jsx';
import { COLOR_THEMES } from '../appShellModel.js';
import { APP_COMMAND_IDS, APP_COMMAND_REGISTRY } from '../commands/appCommandRegistry.js';
import { createAppCommandRuntime } from '../commands/appCommandRuntime.js';
import { useAppCommandDispatcher } from '../commands/useAppCommandDispatcher.js';
import { CommandPalette } from '../../features/command-palette/ui/CommandPalette.jsx';
import { textValue } from '../../pages/shared/pageShared.js';
import { APP_BRAND_NAME, APP_COPY } from '../../shared/i18n/appI18n.js';
import { runUIAction } from '../../shared/ui/runUIAction.js';
import { uiActionWarningOptions } from '../../shared/ui/uiActionWarningOptions.js';
import { ActionFailureSink } from '../../shared/ui/actionFailureSink.jsx';
import suiyuanBrandIcon from '../../assets/suiyuan-brand-icon.png';
import { appShortcutPlatform } from './appShortcutPlatform.js';
import { updateVersionFromResult } from './appUpdateVersion.js';

const SUIYUAN_NAV_ITEMS = Object.freeze([
  { id: 'chat', label: 'Chat', labelKey: 'chat', icon: MessageSquareText },
  { id: 'skills', label: 'Plugins', labelKey: 'skills', icon: Puzzle },
  { id: 'workflows', label: 'Automation', labelKey: 'workflows', icon: SlidersHorizontal },
  { id: 'prompts', label: 'Roles', labelKey: 'prompts', icon: CircleUserRound },
  { id: 'files', label: 'Files', labelKey: 'files', icon: FolderOpen },
  { id: 'memory', label: 'Memory', labelKey: 'memory', icon: Brain },
  { id: 'observability', label: 'Logs', labelKey: 'observability', icon: Database },
]);

// 移动端底部导航：仅保留核心目的地，完整导航仍由抽屉侧栏提供。
const SUIYUAN_MOBILE_NAV_ITEMS = Object.freeze([
  { id: 'chat', labelKey: 'chatShort', icon: MessageSquareText },
  { id: 'skills', labelKey: 'skillsShort', icon: Puzzle },
  { id: 'prompts', labelKey: 'promptsShort', icon: CircleUserRound },
  { id: 'memory', labelKey: 'memoryShort', icon: Brain },
  { id: 'settings', labelKey: 'settings', icon: SettingsIcon },
]);
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

function AppUpdateBanner({ copy = APP_COPY.zh.update, updateBanner }) {
  const checkFailed = updateBanner?.status === 'failed';
  if (!updateBanner?.update && !updateBanner?.message && !checkFailed) return null;
  if (checkFailed) {
    return (
      <section className="app-update-banner" data-testid="app-update-check-failure" role="alert">
        <div className="app-update-copy">
          <strong>{copy.checkFailedTitle}</strong>
          <span>{copy.checkFailedDescription}</span>
        </div>
        <div className="app-update-actions">
          <button type="button" className="app-update-secondary" onClick={updateBanner.dismiss}>{copy.close}</button>
        </div>
      </section>
    );
  }
  const version = updateVersionFromResult(updateBanner.update);
  const installing = updateBanner.status === 'installing';
  const recoveryOnly = !updateBanner.update;
  return (
    <section className="app-update-banner" data-testid="app-update-banner" role={recoveryOnly ? 'alert' : 'status'}>
      <div className="app-update-copy">
        <strong>{recoveryOnly ? '更新需要处理' : `${copy.available}${version ? ` ${version}` : ''}`}</strong>
        {!recoveryOnly ? <span>{copy.description}</span> : null}
        {updateBanner.message ? <small>{updateBanner.message}</small> : null}
      </div>
      {!recoveryOnly ? <div className="app-update-actions">
        <button type="button" className="app-update-primary" onClick={updateBanner.install} disabled={installing}>
          {installing ? copy.installing : copy.install}
        </button>
        <button type="button" className="app-update-secondary" onClick={updateBanner.dismiss} disabled={installing}>
          {copy.dismiss}
        </button>
      </div> : null}
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
          <span>AI Desktop</span>
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
        <a
          href="https://github.com/anthropic-ai/super-agent-v3"
          target="_blank"
          rel="noopener noreferrer"
          className="suiyuan-footer-item"
        >
          <HelpCircle size={15} aria-hidden="true" />
          <span>{copy.workbench.help || 'Help'}</span>
        </a>
      </div>
    </aside>
  );
}

function SuiyuanMobileNav({ activePage, copy, setActivePage }) {
  return (
    <nav className="suiyuan-mobile-nav" data-testid="mobile-nav" aria-label={copy.workbench.mobileNavAriaLabel}>
      {SUIYUAN_MOBILE_NAV_ITEMS.map((item) => {
        const Icon = item.icon;
        const active = activePage === item.id;
        const label = copy.nav[item.labelKey] || copy.nav[item.id] || item.id;
        return (
          <button
            key={item.id}
            type="button"
            className={`suiyuan-mobile-nav-item${active ? ' active' : ''}`}
            onClick={() => setActivePage(item.id)}
            aria-label={label}
            aria-current={active ? 'page' : undefined}
          >
            <Icon size={20} aria-hidden="true" />
            <span>{label}</span>
          </button>
        );
      })}
    </nav>
  );
}

function useBoundAppCommandRuntime(options) {
  const { copy, overrides, setActivePage, setPaletteOpen, setSidebarOpen, startNewChat, store } = options;
  return useMemo(() => (overrides ? createAppCommandRuntime({
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
          run: () => runUIAction('thread.interrupt', () => store.interruptActiveThread(), uiActionWarningOptions(store)),
          canExecute: () => store.hasActiveThreadActions() && !hasOpenLocalEscapeSurface(),
          disabledReason: copy.turnInterruptDisabledReason,
        },
      },
      overrides,
      platform: appShortcutPlatform(),
    }) : undefined), [copy, overrides, setActivePage, setPaletteOpen, setSidebarOpen, startNewChat, store]);
}

function AppCommandPalette({ copy, onClose, open, runtime }) {
  if (!runtime) return null;
  return (
    <CommandPalette
      open={open}
      commands={runtime.commands}
      execute={runtime.execute}
      onClose={onClose}
      copy={copy}
    />
  );
}

function SuiyuanTopAppBar({ copy, currentPageLabel, locale, controls }) {
  const {
    isDark,
    setActivePage,
    themeLabel,
    toggleLocale,
    toggleTheme,
  } = controls;
  const ThemeIcon = isDark ? Sun : Moon;
  return (
    <header className="suiyuan-top-appbar" aria-label="Suiyuan app bar">
      <div className="suiyuan-appbar-title">
        <span>{copy.currentPagePrefix}</span>
        <h1>{currentPageLabel}</h1>
      </div>
      <div className="suiyuan-appbar-actions" aria-label="Workspace actions">
        <button
          type="button"
          className="suiyuan-icon-action"
          aria-label={copy.workbench.notifications}
          title={copy.workbench.notifications}
          onClick={() => setActivePage('observability')}
        >
          <Bell size={15} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="suiyuan-icon-action"
          aria-label={copy.workbench.history}
          title={copy.workbench.history}
          onClick={() => setActivePage('chat')}
        >
          <Clock3 size={15} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="suiyuan-icon-action"
          aria-label={`${copy.workbench.switchThemePrefix}${themeLabel}`}
          title={themeLabel}
          onClick={toggleTheme}
        >
          <ThemeIcon size={15} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="suiyuan-locale-action"
          aria-label={copy.switchLanguage}
          title={copy.switchLanguage}
          onClick={toggleLocale}
        >
          {locale.toUpperCase()}
        </button>
      </div>
    </header>
  );
}

export function SuiyuanAppWindow({ language, shell, shellLayoutStore, shortcutController, store }) {
  const {
    memoryBadge,
    projectPath,
    rightPanelOpen,
    setRightPanelOpen,
    theme,
    toggleTheme,
    updateBanner,
  } = shell;
  const { copy, locale, toggleLocale } = language;
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
    runUIAction('thread.new', () => store?.newThread?.(), uiActionWarningOptions(store));
  }, [setActivePageFromSidebar, store]);
  const commandRuntime = useBoundAppCommandRuntime({
    copy: copy.commands,
    overrides: shortcutController?.status === 'ready' ? shortcutController.validatedOverrides : undefined,
    setActivePage: setActivePageFromSidebar,
    setPaletteOpen,
    setSidebarOpen,
    startNewChat,
    store,
  });
  useAppCommandDispatcher({ runtime: commandRuntime });
  return (
    <div
      className={`sa-window suiyuan-shell${sidebarOpen ? ' sidebar-open' : ' sidebar-collapsed'}`}
      data-command-palette-open={paletteOpen}
      data-brand="suiyuan"
      data-theme={theme}
      data-testid="frontend-app"
    >
      {shortcutController?.status === 'error' ? (
        <output role="alert" data-testid="shortcut-config-error">{shortcutController.error}</output>
      ) : null}
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
          <SuiyuanTopAppBar
            copy={copy}
            currentPageLabel={currentPageLabel}
            locale={locale}
            controls={{
              isDark,
              setActivePage: setActivePageFromSidebar,
              themeLabel,
              toggleLocale,
              toggleTheme,
            }}
          />
          <AppUpdateBanner copy={copy.update} updateBanner={updateBanner} />
          <div className="suiyuan-main-canvas">
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
                shellLayoutStore={shellLayoutStore}
                shortcutController={shortcutController}
                setRightPanelOpen={setRightPanelOpen}
              />
            </Suspense>
          </div>
        </main>
      </div>
      <SuiyuanMobileNav activePage={store.activePage} copy={copy} setActivePage={setActivePageFromSidebar} />
      <AppCommandPalette copy={copy.commands} onClose={() => setPaletteOpen(false)} open={paletteOpen} runtime={commandRuntime} />
      <ActionFailureSink />
    </div>
  );
}
