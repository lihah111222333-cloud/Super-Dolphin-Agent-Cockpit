import React, { Suspense, useCallback, useMemo, useState } from 'react';
import {
  Bell,
  Brain,
  ChevronLeft,
  CircleUserRound,
  Clock3,
  Database,
  FolderOpen,
  Menu,
  MessageSquareText,
  Moon,
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
import { APP_COMMAND_IDS, APP_COMMAND_REGISTRY } from '../commands/appCommandRegistry.js';
import { createAppCommandRuntime } from '../commands/appCommandRuntime.js';
import { useAppCommandDispatcher } from '../commands/useAppCommandDispatcher.js';
import { CommandPalette } from '../../features/command-palette/ui/CommandPalette.jsx';
import { errorMessage } from '../../pages/shared/pageShared.js';
import { APP_BRAND_NAME, APP_COPY } from '../../shared/i18n/appI18n.js';
import { runUIAction } from '../../shared/ui/runUIAction.js';
import { ActionFailureSink } from '../../shared/ui/actionFailureSink.jsx';
import superDolphinAgentBrandIcon from '../../assets/super-dolphin-agent-brand-icon.png';
import { appShortcutPlatform } from './appShortcutPlatform.js';
import { updateVersionFromResult } from './appUpdateVersion.js';
import { useShellLayoutStore } from './model/useShellLayoutStore.js';
import { useWorkbenchLayout } from '../../shared/layout/useWorkbenchLayout.js';
import { WorkbenchActivityBar } from './WorkbenchActivityBar.jsx';
import { WorkbenchBottomPanel } from './WorkbenchBottomPanel.jsx';
import { WorkbenchStatusBar } from './WorkbenchStatusBar.jsx';
import { ChatActionsTrigger } from '../../pages/chat/components/ChatPageHeader.jsx';

const SUPER_DOLPHIN_AGENT_NAV_ITEMS = Object.freeze([
  { id: 'chat', label: 'Chat', labelKey: 'chat', icon: MessageSquareText },
  { id: 'skills', label: 'Plugins', labelKey: 'skills', icon: Puzzle },
  { id: 'workflows', label: 'Automation', labelKey: 'workflows', icon: SlidersHorizontal },
  { id: 'prompts', label: 'Roles', labelKey: 'prompts', icon: CircleUserRound },
  { id: 'files', label: 'Files', labelKey: 'files', icon: FolderOpen },
  { id: 'memory', label: 'Memory', labelKey: 'memory', icon: Brain },
  { id: 'observability', label: 'Logs', labelKey: 'observability', icon: Database },
]);

// 移动端底部导航：仅保留核心目的地，完整导航仍由抽屉侧栏提供。
const SUPER_DOLPHIN_AGENT_MOBILE_NAV_ITEMS = Object.freeze([
  { id: 'chat', labelKey: 'chatShort', icon: MessageSquareText },
  { id: 'skills', labelKey: 'skillsShort', icon: Puzzle },
  { id: 'prompts', labelKey: 'promptsShort', icon: CircleUserRound },
  { id: 'memory', labelKey: 'memoryShort', icon: Brain },
  { id: 'settings', labelKey: 'settings', icon: SettingsIcon },
]);
function hasOpenLocalEscapeSurface() {
  return Boolean(document.querySelector('dialog[open], [role="dialog"], [role="menu"], [role="listbox"], [data-escape-scope="local"]'));
}

function uiActionOptions(store) {
  return {
    onError: (error) => {
      store?.addWarning?.('error', 'ui.action.failed', { error: errorMessage(error) });
    },
  };
}

function threadUIActionOptions(store) {
  const options = uiActionOptions(store);
  return store.activeThreadId ? { ...options, threadId: store.activeThreadId } : options;
}

function AppUpdateBanner({ copy = APP_COPY.zh.update, updateBanner }) {
  if (!updateBanner?.update && !updateBanner?.message) return null;
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

function SuperDolphinAgentNavButton({ activePage, copy, item, memoryBadgeCount, setActivePage }) {
  const Icon = item.icon;
  const active = activePage === item.id;
  const label = copy.nav[item.labelKey] || item.label;
  const badgeCount = item.id === 'memory' ? memoryBadgeCount : 0;
  return (
    <button
      type="button"
      className={`super-dolphin-agent-nav-item${active ? ' active' : ''}`}
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

function SuperDolphinAgentChatNavGroup({ copy, item, projectPath, sidebar, store }) {
  const { activePage, setActivePage } = sidebar;
  return (
    <div className="super-dolphin-agent-chat-nav-group">
      <SuperDolphinAgentNavButton
        activePage={activePage}
        copy={copy}
        item={item}
        memoryBadgeCount={0}
        setActivePage={setActivePage}
      />
      {activePage === 'chat' ? (
        <div className="super-dolphin-agent-chat-project-tree">
          <ChatSidebarProjectTree copy={copy.workbench} projectPath={projectPath} setActivePage={setActivePage} store={store} />
        </div>
      ) : null}
    </div>
  );
}

function SuperDolphinAgentSidebar({ copy, layout, projectPath, sidebar, store }) {
  const { activePage, isOpen, memorySimilarCount, setActivePage, startNewChat } = sidebar;
  const memoryBadgeCount = Math.max(0, Number(memorySimilarCount) || 0);

  return (
    <aside
      id="app-sidebar"
      className={`app-sidebar super-dolphin-agent-sidebar${isOpen ? ' is-open' : ''}`}
      data-testid="app-sidebar"
      aria-label={copy.workbench.ariaLabel}
    >
      <div className="super-dolphin-agent-brand-block">
        <span className="super-dolphin-agent-brand-light-mark" data-testid="super-dolphin-agent-brand-light-logo" aria-hidden="true">
          <Sailboat size={14} strokeWidth={2} />
        </span>
        <img className="super-dolphin-agent-brand-dark-mark" data-testid="super-dolphin-agent-brand-dark-logo" src={superDolphinAgentBrandIcon} alt="" aria-hidden="true" />
        <div className="super-dolphin-agent-brand-meta">
          <strong>{APP_BRAND_NAME}</strong>
          <span>AI Desktop</span>
        </div>
      </div>
      <button type="button" className="super-dolphin-agent-new-chat" aria-label={copy.workbench.newChat} onClick={startNewChat}>
        <Plus size={18} aria-hidden="true" />
        <span>{copy.workbench.newChat}</span>
      </button>
      <nav className="super-dolphin-agent-nav" data-testid="sidebar-nav" aria-label="Super Dolphin Agent navigation">
        <SuperDolphinAgentChatNavGroup
          copy={copy}
          item={SUPER_DOLPHIN_AGENT_NAV_ITEMS[0]}
          projectPath={projectPath}
          sidebar={sidebar}
          store={store}
        />
        {SUPER_DOLPHIN_AGENT_NAV_ITEMS.slice(1).map((item) => (
          <SuperDolphinAgentNavButton
            key={item.id}
            activePage={activePage}
            copy={copy}
            item={item}
            memoryBadgeCount={memoryBadgeCount}
            setActivePage={setActivePage}
          />
        ))}
      </nav>
      <div className="super-dolphin-agent-sidebar-footer">
        <button
          type="button"
          className={`super-dolphin-agent-footer-item${activePage === 'settings' ? ' active' : ''}`}
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
          className="super-dolphin-agent-footer-item"
        >
          <HelpCircle size={15} aria-hidden="true" />
          <span>{copy.workbench.help || 'Help'}</span>
        </a>
      </div>
      <button
        type="button"
        className="workbench-sidebar-resizer"
        data-testid="workbench-sidebar-resizer"
        role="separator"
        aria-label={copy.workbench.resize}
        aria-orientation="vertical"
        aria-valuemin={layout.snapshot.aria.railMin}
        aria-valuemax={layout.snapshot.aria.railMax}
        aria-valuenow={layout.snapshot.aria.railNow}
        onKeyDown={layout.actions.rail.keyDown}
        onPointerDown={layout.actions.rail.begin}
      />
    </aside>
  );
}

function SuperDolphinAgentMobileNav({ activePage, copy, setActivePage }) {
  return (
    <nav className="super-dolphin-agent-mobile-nav" data-testid="mobile-nav" aria-label={copy.workbench.mobileNavAriaLabel}>
      {SUPER_DOLPHIN_AGENT_MOBILE_NAV_ITEMS.map((item) => {
        const Icon = item.icon;
        const active = activePage === item.id;
        const label = copy.nav[item.labelKey] || copy.nav[item.id] || item.id;
        return (
          <button
            key={item.id}
            type="button"
            className={`super-dolphin-agent-mobile-nav-item${active ? ' active' : ''}`}
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
      run: () => runUIAction('thread.interrupt', () => store.interruptActiveThread(), threadUIActionOptions(store)),
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

function SuperDolphinAgentTopAppBar({ chatAction, copy, locale, controls }) {
  const {
    isDark,
    setActivePage,
    themeLabel,
    toggleLocale,
    toggleTheme,
  } = controls;
  const ThemeIcon = isDark ? Sun : Moon;
  return (
    <header className="super-dolphin-agent-top-appbar" aria-label="Super Dolphin Agent app bar">
      <div className="super-dolphin-agent-appbar-actions" aria-label="Workspace actions">
        <button
          type="button"
          className="super-dolphin-agent-icon-action"
          aria-label={copy.workbench.notifications}
          title={copy.workbench.notifications}
          onClick={() => setActivePage('observability')}
        >
          <Bell size={15} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="super-dolphin-agent-icon-action"
          aria-label={copy.workbench.history}
          title={copy.workbench.history}
          onClick={() => setActivePage('chat')}
        >
          <Clock3 size={15} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="super-dolphin-agent-icon-action"
          aria-label={`${copy.workbench.switchThemePrefix}${themeLabel}`}
          title={themeLabel}
          onClick={toggleTheme}
        >
          <ThemeIcon size={15} aria-hidden="true" />
        </button>
        <button
          type="button"
          className="super-dolphin-agent-locale-action"
          aria-label={copy.switchLanguage}
          title={copy.switchLanguage}
          onClick={toggleLocale}
        >
          {locale.toUpperCase()}
        </button>
        {chatAction}
      </div>
    </header>
  );
}

function useSuperDolphinAgentWorkbenchLayout({ rightPanelOpen, setRightPanelOpen, shellLayoutStore, sidebarOpen }) {
  const rightPreference = useShellLayoutStore(shellLayoutStore, (state) => state.rightPanelWidth);
  const setRightPreference = useShellLayoutStore(shellLayoutStore, (state) => state.setRightPanelWidth);
  return useWorkbenchLayout({
    railOpen: sidebarOpen,
    rightOpen: rightPanelOpen,
    rightPreference,
    setRightOpen: setRightPanelOpen,
    setRightPreference,
  });
}

function SuperDolphinAgentMainSurface(model) {
  const { appearance, content, copy, header, store, updateBanner } = model;
  const [bottomPanelHeight, setBottomPanelHeight] = useState(36);
  return (
    <main
      className="sa-main super-dolphin-agent-main"
      style={{ '--workbench-bottom-height': `${store.activePage === 'chat' ? bottomPanelHeight : 0}px` }}
    >
      <SuperDolphinAgentTopAppBar
        chatAction={store.activePage === 'chat' ? (
          <ChatActionsTrigger
            copy={copy.chat}
            projectPath={content.projectPath}
            store={store}
          />
        ) : null}
        copy={copy}
        locale={header.locale}
        controls={header.controls}
      />
      <AppUpdateBanner copy={copy.update} updateBanner={updateBanner} />
      <div className="super-dolphin-agent-main-canvas">
        <Suspense fallback={<PageLoadingFallback />}>
          <ActivePageContent
            activePage={store.activePage}
            appearance={appearance}
            copy={copy}
            store={store}
            projectPath={content.projectPath}
            memoryRevision={content.memoryBadge.memoryRevision}
            geometrySnapshot={content.geometrySnapshot}
            layoutActions={content.layoutActions}
            setMemoryPageSimilarCount={content.memoryBadge.setMemoryPageSimilarCount}
            onWorkflowViewChange={content.handleWorkflowViewChange}
            rightPanelOpen={content.rightPanelOpen}
            shortcutController={content.shortcutController}
            setRightPanelOpen={content.layoutActions.right.setOpen}
          />
        </Suspense>
      </div>
      <WorkbenchBottomPanel
        activePage={store.activePage}
        onHeightChange={setBottomPanelHeight}
        projectPath={content.projectPath}
        rightPanelOpen={content.rightPanelOpen}
      />
    </main>
  );
}

export function SuperDolphinAgentAppWindow({ language, shell, shellLayoutStore, shortcutController, store }) {
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
  const [sidebarOpen, setSidebarOpen] = useState(() => {
    const isTest = typeof globalThis !== 'undefined' && globalThis.process?.env?.NODE_ENV === 'test';
    if (isTest) return false;
    if (typeof window !== 'undefined') {
      return window.innerWidth > 920;
    }
    return true;
  });
  const [paletteOpen, setPaletteOpen] = useState(false);
  const workbenchLayout = useSuperDolphinAgentWorkbenchLayout({ rightPanelOpen, setRightPanelOpen, shellLayoutStore, sidebarOpen });
  const geometrySnapshot = workbenchLayout.snapshot;
  const layoutActions = workbenchLayout.actions;
  const SidebarToggleIcon = sidebarOpen ? X : Menu;
  const isDark = theme === 'dark';
  const themeLabel = isDark ? copy.workbench.dayMode : copy.workbench.nightMode;
  const closeSidebar = useCallback(() => setSidebarOpen(false), []);
  const setActivePageFromSidebar = useCallback((page) => {
    store.setActivePage(page);
    const isTest = typeof globalThis !== 'undefined' && globalThis.process?.env?.NODE_ENV === 'test';
    if (isTest || (typeof window !== 'undefined' && window.innerWidth <= 920)) {
      setSidebarOpen(false);
    }
  }, [store]);
  const startNewChat = useCallback(() => {
    setActivePageFromSidebar('chat');
    runUIAction('thread.new', () => store?.newThread?.(), uiActionOptions(store));
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
      className={`sa-window super-dolphin-agent-shell${sidebarOpen ? ' sidebar-open' : ' sidebar-collapsed'}`}
      data-command-palette-open={paletteOpen}
      data-accent={shell.appearance.accent}
      data-brand="super-dolphin-agent"
      data-theme={theme}
      data-theme-mode={shell.appearance.themeMode}
      data-ui-scale={shell.appearance.uiScale}
      data-testid="frontend-app"
      style={{
        ...geometrySnapshot.cssVars,
        '--ui-scale': shell.appearance.uiScale / 100,
      }}
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
      <div className="sa-body super-dolphin-agent-shell-body">
        {!sidebarOpen ? (
          <WorkbenchActivityBar
            activePage={store.activePage}
            items={SUPER_DOLPHIN_AGENT_NAV_ITEMS}
            onSelect={setActivePageFromSidebar}
            onToggleSidebar={() => setSidebarOpen(true)}
            sidebarOpen={false}
          />
        ) : null}
        <SuperDolphinAgentSidebar
          copy={copy}
          layout={workbenchLayout}
          projectPath={projectPath}
          sidebar={{
            activePage: store.activePage,
            isOpen: sidebarOpen,
            memorySimilarCount: memoryBadge.memorySimilarCount,
            setActivePage: setActivePageFromSidebar,
            startNewChat,
          }}
          store={store}
        />
        {sidebarOpen ? (
          <div className="super-dolphin-agent-sidebar-collapse-zone">
            <button
              type="button"
              className="super-dolphin-agent-sidebar-collapse"
              aria-label={copy.workbench.collapse}
              title={copy.workbench.collapse}
              aria-controls="app-sidebar"
              onClick={closeSidebar}
            >
              <ChevronLeft size={17} aria-hidden="true" />
            </button>
          </div>
        ) : null}
        <SuperDolphinAgentMainSurface
          appearance={shell.appearance}
          content={{ geometrySnapshot, layoutActions, memoryBadge, projectPath, rightPanelOpen, shortcutController }}
          copy={copy}
          header={{
            controls: { isDark, setActivePage: setActivePageFromSidebar, themeLabel, toggleLocale, toggleTheme },
            locale,
          }}
          store={store}
          updateBanner={updateBanner}
        />
      </div>
      <WorkbenchStatusBar
        accent={shell.appearance.accent}
        activePage={store.activePage}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        themeMode={shell.appearance.themeMode}
        uiScale={shell.appearance.uiScale}
      />
      <SuperDolphinAgentMobileNav activePage={store.activePage} copy={copy} setActivePage={setActivePageFromSidebar} />
      <AppCommandPalette copy={copy.commands} onClose={() => setPaletteOpen(false)} open={paletteOpen} runtime={commandRuntime} />
      <ActionFailureSink />
    </div>
  );
}
