import { describe, expect, it } from 'vitest';
import {
  appSource,
  suiyuanAppWindowSource,
  css,
  declarationsFor,
  firstDeclarationsFor,
  topLevelDeclarationsFor,
  mediaDeclarationsFor,
  mediaDeclarationFor,
} from './styles.test.fixture.js';
describe('workbench shell styles', () => {
  describe('suiyuan shell layout', () => {
    it('uses warm Suiyuan surfaces for the app shell and primary navigation', () => {
      const navRail = topLevelDeclarationsFor('.app-sidebar.suiyuan-sidebar');
      const activeNav = declarationsFor('.suiyuan-nav-item.active');
      const activeIndicator = declarationsFor('.suiyuan-nav-item.active::before');
      const topCommand = topLevelDeclarationsFor('.suiyuan-top-appbar');
      const mobileTopCommand = mediaDeclarationFor('(max-width: 920px)', '.suiyuan-top-appbar', 'padding');
      const mainCanvas = declarationsFor('.suiyuan-main-canvas');
      const nonChatPage = declarationsFor('.sa-window.suiyuan-shell .suiyuan-main-canvas > .memory-page');
      const skillsPage = declarationsFor('.sa-window.suiyuan-shell .suiyuan-main-canvas > .skills-tabbed-container');
      const main = declarationsFor('.sa-main');

      expect(navRail.background).toBe('var(--sidebar-bg)');
      expect(activeNav.background).toBe('var(--primary-soft)');
      expect(activeNav.color).toBe('var(--text-pri)');
      expect(activeIndicator.width).toBe('4px');
      expect(topCommand.position).toBe('absolute');
      expect(topCommand.height).toBe('64px');
      expect(topCommand.padding).toBe('0 24px');
      expect(mobileTopCommand.padding).toBe('0 14px 0 64px');
      expect(topCommand.background).toBe('var(--bg)');
      expect(topCommand['border-bottom']).toBe('0');
      expect(mainCanvas.height).toBe('100%');
      expect(nonChatPage['padding-top']).toBe('64px');
      expect(skillsPage['padding-top']).toBe('64px');
      expect(main.background).toBe('var(--bg)');
    });

    it('renders the mobile bottom navigation as a fixed bar and keeps content clear of it', () => {
      const desktopNav = topLevelDeclarationsFor('.suiyuan-mobile-nav');
      const mobileNav = mediaDeclarationFor('(max-width: 920px)', '.suiyuan-mobile-nav', 'position');
      const drawerOpenNav = mediaDeclarationFor('(max-width: 920px)', '.sa-window.sidebar-open .suiyuan-mobile-nav', 'display');
      const navItem = mediaDeclarationFor('(max-width: 920px)', '.suiyuan-mobile-nav-item', 'flex-direction');
      const activeItem = mediaDeclarationFor('(max-width: 920px)', '.suiyuan-mobile-nav-item.active', 'color');
      const mainCanvas = mediaDeclarationFor('(max-width: 920px)', '.suiyuan-main-canvas', 'padding-bottom');
      const floatingComposer = mediaDeclarationFor('(max-width: 920px)', '.sa-window .composer.composer--floating[data-file-drop-target]', 'bottom');

      expect(desktopNav.display).toBe('none');
      expect(mobileNav.position).toBe('fixed');
      expect(mobileNav.inset).toBe('auto 0 0 0');
      expect(mobileNav['z-index']).toBe('var(--z-local-sticky)');
      expect(mobileNav.display).toBe('flex');
      expect(drawerOpenNav.display).toBe('none');
      expect(navItem['flex-direction']).toBe('column');
      expect(activeItem.color).toBe('var(--primary)');
      expect(mainCanvas['padding-bottom']).toBe('calc(var(--suiyuan-mobile-nav-height) + env(safe-area-inset-bottom, 0px))');
      expect(floatingComposer.bottom).toBe('calc(var(--suiyuan-mobile-nav-height) + env(safe-area-inset-bottom, 0px))');
    });

    it('keeps the light sidebar on the dark-mode geometry', () => {
      const sharedSidebar = topLevelDeclarationsFor('.app-sidebar.suiyuan-sidebar');
      const sharedBrand = topLevelDeclarationsFor('.suiyuan-brand-block');
      const sharedBrandTitle = topLevelDeclarationsFor('.suiyuan-brand-meta strong');
      const sharedNewChat = topLevelDeclarationsFor('.suiyuan-new-chat');
      const sharedNav = topLevelDeclarationsFor('.suiyuan-nav');
      const sharedNavItem = topLevelDeclarationsFor('.suiyuan-nav-item');
      const lightSidebar = declarationsFor('.sa-window[data-theme="light"] .app-sidebar.suiyuan-sidebar');
      const lightBrand = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-block');
      const lightMark = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-light-mark');
      const lightDarkMark = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-dark-mark');
      const lightBrandTitle = declarationsFor('.sa-window[data-theme="light"] .suiyuan-brand-meta strong');
      const lightNewChat = declarationsFor('.sa-window[data-theme="light"] .suiyuan-new-chat');
      const lightNav = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav');
      const lightNavItem = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav-item');
      const activeNav = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav-item.active');
      const activeIndicator = declarationsFor('.sa-window[data-theme="light"] .suiyuan-nav-item.active::before');
      const appbarTitle = topLevelDeclarationsFor('.suiyuan-appbar-title h1');

      expect(sharedSidebar.gap).toBe('20px');
      expect(sharedSidebar.padding).toBe('24px 18px 18px');
      expect(sharedBrand['min-height']).toBe('42px');
      expect(sharedBrand.gap).toBe('12px');
      expect(sharedBrandTitle['font-size']).toBe('17px');
      expect(sharedNewChat.width).toBe('100%');
      expect(sharedNewChat['min-height']).toBe('40px');
      expect(sharedNav.gap).toBe('6px');
      expect(sharedNavItem['min-height']).toBe('34px');
      expect(sharedNavItem.gap).toBe('10px');
      expect(sharedNavItem.padding).toBe('0 12px 0 14px');
      expect(sharedNavItem['font-size']).toBe('13px');
      expect(sharedNavItem['font-weight']).toBe('620');
      expect(sharedNavItem['line-height']).toBe('18px');
      expect(appbarTitle['line-height']).toBe('1.25');
      for (const property of ['gap', 'padding']) expect(lightSidebar[property]).toBeUndefined();
      for (const property of ['width', 'min-height', 'margin', 'gap']) expect(lightBrand[property]).toBeUndefined();
      for (const property of ['width', 'height', 'display']) expect(lightMark[property]).toBeUndefined();
      expect(lightDarkMark.display).toBeUndefined();
      expect(lightBrandTitle['font-size']).toBeUndefined();
      expect(lightBrandTitle['font-weight']).toBeUndefined();
      for (const property of ['width', 'min-height', 'margin']) {
        expect(lightNewChat[property]).toBeUndefined();
      }
      for (const property of ['font-size', 'font-weight', 'line-height']) {
        expect(lightNewChat[property]).toBeUndefined();
      }
      for (const property of ['width', 'margin', 'gap']) expect(lightNav[property]).toBeUndefined();
      for (const property of ['min-height', 'gap', 'padding']) {
        expect(lightNavItem[property]).toBeUndefined();
      }
      for (const property of ['font-size', 'font-weight', 'line-height']) {
        expect(lightNavItem[property]).toBeUndefined();
      }
      expect(lightNewChat.background).toBe('var(--suiyuan-primary)');
      expect(lightNewChat['box-shadow']).toBe('none');
      expect(lightNewChat.opacity).toBeUndefined();
      expect(activeNav.color).toBe('var(--suiyuan-primary)');
      expect(activeNav['font-weight']).toBeUndefined();
      expect(activeIndicator.inset).toBeUndefined();
    });

    it('keeps marketing tabs and upgrade CTAs out of the Suiyuan app shell', () => {
      expect(appSource).not.toContain('SUIYUAN_APP_TABS');
      expect(appSource).not.toContain("label: 'Overview'");
      expect(appSource).not.toContain("label: 'Usage'");
      expect(appSource).not.toContain("label: 'Limits'");
      expect(appSource).not.toContain('Upgrade Plan');
      expect(appSource).not.toContain('Support');
      expect(css).not.toContain('suiyuan-upgrade-action');
    });

    it('maps Suiyuan design tokens to dark surfaces in dark mode', () => {
      const darkShell = declarationsFor('.sa-window.suiyuan-shell[data-theme="dark"]');

      expect(darkShell['--suiyuan-background']).toBe('#131411');
      expect(darkShell['--suiyuan-surface-bright']).toBe('#131411');
      expect(darkShell['--suiyuan-surface-lowest']).toBe('#1b1c18');
      expect(darkShell['--suiyuan-surface-low']).toBe('#1e1f1b');
      expect(darkShell['--suiyuan-on-surface']).toBe('#e5e2da');
      expect(darkShell['--suiyuan-primary']).toBe('#ffb597');
      expect(darkShell['--suiyuan-card-shadow']).toBe('0 20px 40px -10px rgba(0, 0, 0, 0.3)');
      expect(darkShell['--suiyuan-input-shadow']).toBe('0 8px 30px rgba(0, 0, 0, 0.2)');
    });

    it('renders memory controls as compact Suiyuan components', () => {
      const stats = topLevelDeclarationsFor('.memory-page .memory-stats');
      const panel = declarationsFor('.memory-page .memory-stats .panel');
      const overviewChip = declarationsFor('.memory-page .memory-overview-breakdown > span');
      const autoToggle = topLevelDeclarationsFor('.memory-page .memory-auto-dream-toggle');
      const createButton = declarationsFor('.memory-page .memory-create-button');
      const createMenu = declarationsFor('.memory-page .memory-create-menu');

      expect(stats['grid-template-columns']).toBe('repeat(3, minmax(0, 1fr))');
      expect(panel['border-radius']).toBe('var(--suiyuan-radius-card)');
      expect(overviewChip['border-radius']).toBe('999px');
      expect(autoToggle['grid-column']).toBe('2');
      expect(createButton.background).toBe('var(--primary-action-bg)');
      expect(createButton['border-radius']).toBe('999px');
      expect(createMenu.left).toBe('0');
      expect(createMenu.right).toBe('auto');
      expect(createMenu.width).toBe('max-content');
    });
  });

  it('keeps the screenshot-style sidebar fixed and branded', () => {
    const sidebar = topLevelDeclarationsFor('.app-sidebar.suiyuan-sidebar');
    const body = topLevelDeclarationsFor('.sa-body.suiyuan-shell-body');
    const brand = declarationsFor('.suiyuan-brand-block');
    const brandMeta = declarationsFor('.suiyuan-brand-meta');
    const newChat = declarationsFor('.suiyuan-new-chat');
    const nav = declarationsFor('.suiyuan-nav');
    const chatNavGroup = declarationsFor('.suiyuan-chat-nav-group');
    const projectTree = declarationsFor('.suiyuan-chat-project-tree');
    const collapseButton = declarationsFor('.suiyuan-sidebar-collapse');
    const footer = declarationsFor('.suiyuan-sidebar-footer');

    expect(sidebar.width).toBe('280px');
    expect(sidebar.position).toBe('relative');
    expect(sidebar.background).toBe('var(--sidebar-bg)');
    expect(sidebar['border-right']).toBe('1px solid var(--sidebar-border)');
    expect(sidebar.overflow).toBe('hidden');
    expect(body.height).toBe('100vh');
    expect(body['grid-template-columns']).toBe('280px minmax(0, 1fr)');
    expect(brand.display).toBe('flex');
    expect(brandMeta.display).toBe('grid');
    expect(newChat['min-height']).toBe('40px');
    expect(newChat.background).toBe('var(--primary-action-bg)');
    expect(nav.display).toBe('grid');
    expect(chatNavGroup.display).toBe('grid');
    expect(chatNavGroup['min-height']).toBe('0');
    expect(projectTree['max-height']).toBe('min(300px, 32vh)');
    expect(projectTree['overflow-y']).toBe('auto');
    expect(projectTree['overscroll-behavior']).toBe('contain');
    expect(projectTree['scrollbar-gutter']).toBe('stable');
    expect(collapseButton.width).toBe('32px');
    expect(collapseButton.height).toBe('32px');
    expect(collapseButton['margin-left']).toBe('auto');
    expect(footer['margin-top']).toBe('auto');
  });

  it('keeps the primary product nav while nesting projects under Chat', () => {
    expect(appSource).toContain("import { SuiyuanAppWindow } from './app/shell/SuiyuanAppWindow.jsx';");
    expect(appSource).toContain('<SuiyuanAppWindow');
    expect(suiyuanAppWindowSource).toContain('<ChatSidebarProjectTree');
    expect(suiyuanAppWindowSource).not.toContain('<SidebarTaskSummary');
    expect(suiyuanAppWindowSource).toContain("label: 'Chat'");
    expect(suiyuanAppWindowSource).toContain("label: 'Plugins'");
    expect(suiyuanAppWindowSource).toContain("label: 'Automation'");
    expect(suiyuanAppWindowSource).toContain("label: 'Roles'");
    expect(suiyuanAppWindowSource).toContain("label: 'Files'");
    expect(suiyuanAppWindowSource).toContain("label: 'Memory'");
    expect(suiyuanAppWindowSource).toContain("label: 'Logs'");
  });

  it('exposes a mobile workbench drawer so settings remains reachable', () => {
    const desktopToggle = topLevelDeclarationsFor('.workbench-toggle');
    const mobileToggle = mediaDeclarationsFor('(max-width: 920px)', '.workbench-toggle')[0];
    const mobileSidebar = mediaDeclarationsFor('(max-width: 920px)', '.app-sidebar')[0];
    const openSidebar = mediaDeclarationsFor('(max-width: 920px)', '.app-sidebar.is-open')[0];
    const mobileResizer = mediaDeclarationsFor('(max-width: 920px)', '.workbench-sidebar-resizer')[0];
    const scrim = mediaDeclarationsFor('(max-width: 920px)', '.sidebar-scrim')[0];
    const mobileSettings = mediaDeclarationsFor('(max-width: 920px)', '.sa-window .settings-page')[0];

    expect(desktopToggle.display).toBe('none');
    expect(mobileToggle.display).toBe('inline-flex');
    expect(mobileToggle.position).toBe('fixed');
    expect(mobileToggle['z-index']).toBe('var(--z-shell-control)');
    expect(mobileSidebar.position).toBe('fixed');
    expect(mobileSidebar['--workbench-sidebar-width']).toBe('min(320px, calc(100vw - 52px))');
    expect(mobileSidebar.width).toBe('var(--workbench-sidebar-width)');
    expect(mobileSidebar['margin-left']).toBe('calc(-1 * var(--workbench-sidebar-width))');
    expect(mobileSidebar.transform).toBe('none');
    expect(mobileSidebar.transition).toBe('margin-left 180ms ease');
    expect(mobileSidebar['max-width']).toBe('var(--workbench-sidebar-width)');
    expect(mobileSidebar['box-shadow']).toBe('none');
    expect(openSidebar['margin-left']).toBe('0');
    expect(openSidebar.transform).toBe('none');
    expect(openSidebar['box-shadow']).toBe('var(--shadow)');
    expect(mobileResizer.display).toBe('none');
    expect(scrim.display).toBe('block');
    expect(mobileSettings['padding-top']).toBe('78px');
  });

  it('keeps the chat composer adaptive across desktop client widths and phones', () => {
    const mediumConversation = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .conversation')[0];
    const mediumComposer = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer')[0];
    const mediumActions = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer-actions')[0];
    const mediumModel = mediaDeclarationsFor('(max-width: 1180px)', '.sa-window .composer-model')[0];
    const tabletTitle = mediaDeclarationsFor('(max-width: 920px)', '.empty-chat h2')[0];
    const mobileMeta = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer-meta')[0];
    const mobileProject = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-meta .project-select-wrap', 'width');
    const mobileActions = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-actions', 'display');
    const mobileModel = mediaDeclarationFor('(max-width: 640px)', '.sa-window .composer-model', 'max-width');
    const mobileSend = mediaDeclarationsFor('(max-width: 640px)', '.sa-window .composer .send')[0];

    expect(mediumConversation['--conversation-content-width']).toBe('min(100%, calc(100% - 44px))');
    expect(mediumComposer.width).toBe('min(100%, calc(100% - 44px))');
    expect(mediumActions['min-width']).toBe('0');
    expect(mediumModel['min-width']).toBe('0');
    expect(tabletTitle['white-space']).toBe('normal');
    expect(tabletTitle['overflow-wrap']).toBe('anywhere');
    expect(mobileMeta.display).toBe('flex');
    expect(mobileMeta['flex-wrap']).toBe('nowrap');
    expect(mobileProject.width).toBe('auto');
    expect(mobileActions.display).toBe('inline-flex');
    expect(mobileModel['max-width']).toBe('100%');
    expect(mobileSend.width).toBe('40px');
    expect(mobileSend['min-width']).toBe('40px');
  });
});

describe('composer control styles', () => {
  it('gives the portalled project menu an opaque component-owned surface', () => {
    const trigger = topLevelDeclarationsFor('.sa-window .composer-meta .project-select');
    const popover = topLevelDeclarationsFor('.project-selector-popover');
    const hostPopover = topLevelDeclarationsFor('#overlay-root .project-selector-popover');
    const menu = topLevelDeclarationsFor('.project-dropdown');
    const row = topLevelDeclarationsFor('.project-dropdown-row');

    expect(trigger.background).toBe('var(--surface)');
    expect(trigger['box-shadow']).toBe('var(--suiyuan-input-highlight)');
    expect(popover.background).toBe('var(--surface)');
    expect(popover.border).toBe('1px solid var(--border)');
    expect(popover['border-radius']).toBe('8px');
    expect(popover['box-shadow']).toBe('var(--shadow)');
    expect(hostPopover['z-index']).toBe('var(--z-overlay-popover)');
    expect(menu.display).toBe('grid');
    expect(menu['overflow-y']).toBe('auto');
    expect(row.display).toBe('grid');
    expect(row['grid-template-columns']).toBe('minmax(0, 1fr) 32px');
  });

  it('keeps the composer project selector clickable without displacing send controls', () => {
    const wrap = topLevelDeclarationsFor('.composer-meta .project-select-wrap');
    const button = topLevelDeclarationsFor('.composer-meta .project-select');
    const label = topLevelDeclarationsFor('.composer-meta .project-select span');

    expect(wrap.flex).toBe('0 1 210px');
    expect(wrap['min-width']).toBe('0');
    expect(wrap['max-width']).toBe('210px');
    expect(button.width).toBe('100%');
    expect(button['max-width']).toBe('100%');
    expect(label['min-width']).toBe('0');
    expect(label.overflow).toBe('hidden');
    expect(label['text-overflow']).toBe('ellipsis');
    expect(label['white-space']).toBe('nowrap');
  });

  it('keeps the collapsed project selector short while allowing a wider project menu', () => {
    const select = declarationsFor('.top-command .project-select');
    const dropdown = declarationsFor('.top-command .project-dropdown');

    expect(select.width).toBe('fit-content');
    expect(select['max-width']).toBe('min(220px, 34vw)');
    expect(dropdown.width).toBe('max-content');
    expect(dropdown['min-width']).toBe('360px');
    expect(dropdown['max-width']).toBe('min(520px, 86vw)');
  });

  it('keeps the right sidebar toggle docked to the page edge', () => {
    const toggle = declarationsFor('.top-command .sidebar-toggle');

    expect(toggle['margin-left']).toBe('auto');
    expect(toggle.display).toBe('inline-flex');
  });

  it('lets the model selector popover escape the adaptive composer card', () => {
    const card = declarationsFor('.composer-card');
    const wrap = topLevelDeclarationsFor('.composer-model-wrap');
    const button = topLevelDeclarationsFor('.composer-model');
    const dropdown = topLevelDeclarationsFor('.model-dropdown');

    expect(card.overflow).toBe('visible');
    expect(wrap.position).toBe('relative');
    expect(wrap.width).toBe('auto');
    expect(wrap['max-width']).toBe('min(210px, 100%)');
    expect(button.width).toBe('100%');
    expect(button.padding).toBe('0 12px');
    expect(dropdown.position).toBe('absolute');
    expect(dropdown.inset).toBe('auto 0 calc(100% + 8px) auto');
    expect(dropdown.bottom).toBe('calc(100% + 8px)');
    expect(dropdown.height).toBe('max-content');
    expect(dropdown['max-height']).toBe('min(320px, calc(100vh - 48px))');
    expect(dropdown['grid-auto-rows']).toBe('max-content');
    expect(dropdown['align-content']).toBe('start');
    expect(dropdown.overflow).toBe('visible');
  });

  it('keeps the slash command palette opaque, bounded, and internally scrollable', () => {
    const card = topLevelDeclarationsFor('.composer-card');
    const palette = topLevelDeclarationsFor('.slash-command-palette');
    const results = topLevelDeclarationsFor('.slash-command-palette__results');
    const mobile = mediaDeclarationFor('(max-width: 720px)', '.slash-command-palette', 'inset-inline');

    expect(card.position).toBe('relative');
    expect(palette.position).toBe('absolute');
    expect(palette.background).toBe('var(--surface)');
    expect(palette.background).not.toMatch(/gradient/u);
    expect(palette['max-height']).toBe('360px');
    expect(palette.overflow).toBe('hidden');
    expect(results['max-height']).toBe('360px');
    expect(results['overflow-y']).toBe('auto');
    expect(results['overscroll-behavior']).toBe('contain');
    expect(mobile['inset-inline']).toBe('8px');
    expect(mobile.width).toBe('auto');
    expect(mobile['max-height']).toBe('min(360px, 52vh)');
  });

  it('keeps the workbench composer send button visible when model text is long', () => {
    const actions = topLevelDeclarationsFor('.composer-actions');
    const wrap = topLevelDeclarationsFor('.composer-model-wrap');
    const button = topLevelDeclarationsFor('.composer-model');
    const label = topLevelDeclarationsFor('.composer-model span');
    const send = topLevelDeclarationsFor('.composer .send');

    expect(actions['min-width']).toBe('0');
    expect(actions.flex).toBe('1 1 0');
    expect(actions['justify-content']).toBe('flex-end');
    expect(wrap.flex).toBe('1 1 auto');
    expect(wrap['min-width']).toBe('0');
    expect(wrap['max-width']).toBe('min(210px, 100%)');
    expect(button.width).toBe('100%');
    expect(button['min-width']).toBe('0');
    expect(button['max-width']).toBe('100%');
    expect(label['min-width']).toBe('0');
    expect(label.overflow).toBe('hidden');
    expect(label['text-overflow']).toBe('ellipsis');
    expect(send.flex).toBe('0 0 40px');
    expect(send['min-width']).toBe('40px');
  });

  it('keeps attachment controls left while model controls sit on the right', () => {
    const meta = topLevelDeclarationsFor('.composer-meta');
    const actions = topLevelDeclarationsFor('.composer-actions');
    const provider = firstDeclarationsFor('.composer .provider');

    expect(meta['align-items']).toBe('center');
    expect(meta.gap).toBe('8px');
    expect(actions['margin-left']).toBe('auto');
    expect(actions['justify-content']).toBe('flex-end');
    expect(actions['padding-left']).toBe('0');
    expect(provider['margin-left']).toBe('0');
  });

  it('renders the provider toggle as a sliding pill control', () => {
    const provider = declarationsFor('.provider');
    const composerProvider = declarationsFor('.composer .provider');
    const track = declarationsFor('.provider-track');
    const thumb = declarationsFor('.provider-thumb');
    const activeThumb = declarationsFor('.provider.active .provider-thumb');
    const label = declarationsFor('.provider-label');

    expect(provider.width).toBe('112px');
    expect(provider.background).toBe('transparent');
    expect(provider['border-color']).toBe('transparent');
    expect(provider['border-radius']).toBe('999px');
    expect(composerProvider['border-color']).toBe('transparent');
    expect(composerProvider.background).toBe('transparent');
    expect(track.width).toBe('43px');
    expect(track.height).toBe('22px');
    expect(track.background).toBe('var(--surface-3)');
    expect(thumb.width).toBe('16px');
    expect(thumb.height).toBe('16px');
    expect(label.width).toBe('52px');
    expect(activeThumb.transform).toBe('translateX(19px)');
  });
});

describe('runtime activity panel styles', () => {
  it('lets activity popovers render above the code diff panel', () => {
    const panel = declarationsFor('.runtime-panel');
    const activity = declarationsFor('.runtime-activity-panel');
    const collapsedActivity = declarationsFor('.runtime-activity-panel.is-log-collapsed');
    const collapsedIcons = declarationsFor('.runtime-activity-panel.is-log-collapsed .runtime-icons');
    const diff = declarationsFor('.diff-empty');
    const tooltip = declarationsFor('.runtime-stat-tooltip');
    const warningPopover = declarationsFor('.warning-log-popover');
    const hostTooltip = declarationsFor('#overlay-root .runtime-stat-tooltip');
    const hostWarningPopover = declarationsFor('#overlay-root .warning-log-popover');

    expect(panel['--activity-panel-height']).toBe('64px');
    expect(panel['--activity-panel-min-height']).toBe('64px');
    expect(panel.overflow).toBe('hidden');
    expect(panel['grid-template-rows']).toContain('var(--activity-panel-height)');
    expect(activity.overflow).toBe('hidden');
    expect(activity.height).toBe('var(--activity-panel-height)');
    expect(collapsedActivity['grid-template-rows']).toBe('minmax(0, 1fr)');
    expect(collapsedIcons.height).toBe('100%');
    expect(collapsedIcons['border-bottom']).toBe('0');
    expect(diff['z-index']).toBe('var(--z-local-raised)');
    expect(activity['z-index']).toBe('var(--z-local-sticky)');
    expect(tooltip.position).toBe('fixed');
    expect(tooltip.left).toBe('var(--runtime-stat-tooltip-left, 12px)');
    expect(tooltip['max-height']).toBe('var(--runtime-stat-tooltip-max-height, min(280px, 42vh))');
    expect(tooltip['z-index']).toBeUndefined();
    expect(warningPopover['z-index']).toBeUndefined();
    expect(hostTooltip['z-index']).toBe('var(--z-overlay-dialog)');
    expect(hostWarningPopover['z-index']).toBe('var(--z-overlay-popover)');
  });
});

describe('runtime resize styles', () => {
  it('keeps resized runtime sidebar content visible instead of requiring horizontal scrolling', () => {
    const toolbar = declarationsFor('.runtime-toolbar');
    const toolbarStat = declarationsFor('.runtime-toolbar .runtime-stat');
    const score = declarationsFor('.score');
    const goodScore = declarationsFor('.score.good');
    const diffView = declarationsFor('.diff-view');
    const diffFileGroup = declarationsFor('.diff-file-group');
    const diffFileToggle = declarationsFor('.diff-file-toggle');
    const diffFileCaret = declarationsFor('.diff-file-caret');
    const diffFileStats = declarationsFor('.diff-file-stats');
    const diffFileLines = declarationsFor('.diff-file-lines');
    const diffFileLinesVirtual = declarationsFor('.diff-file-lines-virtual');
    const diffLine = declarationsFor('.diff-line');
    const diffContent = declarationsFor('.diff-line-content');
    const icons = declarationsFor('.runtime-icons');
    const stat = declarationsFor('.runtime-stat');

    expect(toolbar['min-width']).toBe('0');
    expect(toolbar.display).toBe('grid');
    expect(toolbar['grid-template-columns']).toBe('repeat(2, minmax(0, 1fr))');
    expect(toolbar['align-content']).toBe('center');
    expect(toolbar['overflow']).toBe('hidden');
    expect(toolbarStat['min-width']).toBe('0');
    expect(toolbarStat['justify-content']).toBe('center');
    expect(toolbarStat.cursor).toBeUndefined();
    expect(score['min-width']).toBe('0');
    expect(score['justify-content']).toBe('center');
    expect(goodScore['margin-left']).toBe('0');
    expect(diffView.display).toBe('grid');
    expect(diffFileGroup.overflow).toBe('hidden');
    expect(diffFileToggle['min-width']).toBe('0');
    expect(diffFileToggle['grid-template-columns']).toBe('minmax(0, 1fr)');
    expect(diffFileCaret.width).toBe('14px');
    expect(diffFileCaret.height).toBe('14px');
    expect(diffFileCaret['flex-shrink']).toBe('0');
    expect(diffFileStats['justify-content']).toBe('flex-end');
    expect(diffFileLines['max-height']).toBe('420px');
    expect(diffFileLines.overflow).toBe('auto');
    expect(diffFileLines.position).toBe('relative');
    expect(diffFileLinesVirtual.position).toBe('relative');
    expect(diffLine['grid-template-columns']).toBe('42px 42px 14px minmax(0, 1fr)');
    expect(diffContent['white-space']).toBe('pre-wrap');
    expect(diffContent['overflow-wrap']).toBe('anywhere');
    expect(icons['min-width']).toBe('0');
    expect(icons['display']).toBe('grid');
    expect(icons['grid-template-columns']).toBe('repeat(4, minmax(0, 1fr))');
    expect(icons['overflow']).toBe('visible');
    expect(stat['min-width']).toBe('0');
    expect(stat['justify-content']).toBe('center');
  });

  it('wraps long runtime tool names inside the click tooltip instead of using native hover titles', () => {
    const toolName = declarationsFor('.runtime-stat-tooltip-name');

    expect(toolName['min-width']).toBe('0');
    expect(toolName.overflow).toBe('visible');
    expect(toolName['overflow-wrap']).toBe('anywhere');
    expect(toolName['text-overflow']).toBeUndefined();
    expect(toolName['white-space']).toBe('normal');
  });

  it('keeps warning log details inside hover popovers', () => {
    const line = declarationsFor('.warning-log-line');
    const popover = declarationsFor('.warning-log-popover');
    const hostPopover = declarationsFor('#overlay-root .warning-log-popover');
    const code = declarationsFor('.warning-log-popover code');

    expect(line['white-space']).toBe('nowrap');
    expect(popover.position).toBe('fixed');
    expect(popover['box-sizing']).toBe('border-box');
    expect(popover['min-width']).toBe('0');
    expect(popover.left).toBe('var(--warning-log-popover-left, 12px)');
    expect(popover.right).toBe('var(--warning-log-popover-right, 12px)');
    expect(popover['pointer-events']).toBe('auto');
    expect(popover['z-index']).toBeUndefined();
    expect(hostPopover['z-index']).toBe('var(--z-overlay-popover)');
    expect(code.display).toBe('block');
    expect(code['max-width']).toBe('100%');
    expect(code['overflow-wrap']).toBe('anywhere');
    expect(code['word-break']).toBe('break-word');
  });
});
