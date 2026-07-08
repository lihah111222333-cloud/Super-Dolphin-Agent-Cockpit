import React from 'react';
import { Brain, CircleUserRound, FolderOpen, Moon, PanelLeftClose, Puzzle, RefreshCw, Search, Settings as SettingsIcon, SquarePlus, Sun } from 'lucide-react';
import { errorMessage, textValue } from './pages/shared/pageShared.js';
import { ProjectSelector } from './pages/chat/components/ProjectSelector.jsx';
import { runUIAction } from './shared/ui/runUIAction.js';
import { SidebarProjectTree, SidebarTaskSummary } from './WorkbenchSidebarProjectTree.jsx';
import { APP_BRAND_NAME, APP_COPY } from './shared/i18n/appI18n.js';
import suiyuanBrandIcon from './assets/suiyuan-brand-icon.png';
import { COLOR_THEMES } from './app/appShellModel.js';

const WORKBENCH_SIDEBAR_MIN_WIDTH = 280;
const WORKBENCH_SIDEBAR_DEFAULT_WIDTH = 340;
const WORKBENCH_SIDEBAR_MAX_WIDTH = 460;

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

function uiActionOptions(store) {
  return {
    onError: (error) => {
      store?.addWarning?.('error', 'ui.action.failed', { error: errorMessage(error) });
    },
  };
}

function SidebarNavButton({ activePage, badgeCount, copy, item, setActivePage }) {
  const Icon = item.icon;
  const label = copy.nav[item.labelKey];
  const displayLabel = item.displayLabelKey ? copy.nav[item.displayLabelKey] : label;
  return (
    <button
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
}

function SidebarNavList(props) {
  const { activePage, className, copy, items, memoryBadgeCount = 0, setActivePage, testId } = props;
  return (
    <nav className={`app-sidebar-nav ${textValue(className)}`} data-testid={testId}>
      {items.map((item) => {
        const badgeCount = item.id === 'memory' ? memoryBadgeCount : 0;
        return <SidebarNavButton key={item.id} activePage={activePage} badgeCount={badgeCount} copy={copy} item={item} setActivePage={setActivePage} />;
      })}
    </nav>
  );
}

export function WorkbenchSidebar({
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
