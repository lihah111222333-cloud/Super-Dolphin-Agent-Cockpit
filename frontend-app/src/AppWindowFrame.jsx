import React, { Suspense } from 'react';
import { Menu, PanelLeftOpen, X } from 'lucide-react';
import { ActivePageContent, PageLoadingFallback } from './AppRoutes.jsx';
import { WorkbenchSidebar } from './WorkbenchSidebar.jsx';
import { firstPresentText } from './pages/shared/pageShared.js';
import { APP_COPY } from './shared/i18n/appI18n.js';

export function AppWindowFrame({ frame }) {
  const SidebarToggleIcon = frame.sidebarOpen ? X : Menu;
  const activeLabel = frame.copy.nav[frame.store.activePage] || frame.copy.nav.chat;
  const currentPageLabel = frame.currentPageLabelOverride || activeLabel;

  return (
    <div className={`sa-window${frame.sidebarOpen ? ' sidebar-open' : ' sidebar-collapsed'}`} data-theme={frame.theme} data-testid="frontend-app">
      <button
        type="button"
        className="workbench-toggle"
        aria-label={frame.sidebarOpen ? frame.copy.workbench.close : frame.copy.workbench.open}
        aria-controls="app-sidebar"
        aria-expanded={frame.sidebarOpen}
        onClick={frame.toggleSidebar}
      >
        <SidebarToggleIcon size={22} aria-hidden="true" />
      </button>
      {frame.sidebarOpen ? <button type="button" className="sidebar-scrim" aria-label={frame.copy.workbench.close} onClick={frame.closeSidebar} /> : null}
      <div className="sa-body" style={{ '--workbench-sidebar-width': `${frame.workbenchSidebarWidth}px` }}>
        {!frame.sidebarOpen ? (
          <button
            type="button"
            className="sidebar-expand-trigger"
            aria-label={frame.copy.workbench.expand}
            title={frame.copy.workbench.expand}
            onClick={frame.openSidebar}
          >
            <PanelLeftOpen size={20} aria-hidden="true" />
          </button>
        ) : null}
        <WorkbenchSidebar
          activePage={frame.store.activePage}
          copy={frame.copy}
          isOpen={frame.sidebarOpen}
          locale={frame.locale}
          sidebarWidth={frame.workbenchSidebarWidth}
          onSidebarResizeKeyDown={frame.onSidebarResizeKeyDown}
          onSidebarResizeStart={frame.onSidebarResizeStart}
          setActivePage={frame.setActivePage}
          store={frame.store}
          projectPath={frame.projectPath}
          theme={frame.theme}
          toggleLocale={frame.toggleLocale}
          toggleTheme={frame.toggleTheme}
          memorySimilarCount={frame.memoryBadge.memorySimilarCount}
          onCloseSidebar={frame.closeSidebar}
        />
        <main className="sa-main">
          <AppUpdateBanner copy={frame.copy.update} updateBanner={frame.updateBanner} />
          <Suspense fallback={<PageLoadingFallback />}>
            <ActivePageContent
              activePage={frame.store.activePage}
              copy={frame.copy}
              store={frame.store}
              projectPath={frame.projectPath}
              memoryRevision={frame.memoryBadge.memoryRevision}
              setMemoryPageSimilarCount={frame.memoryBadge.setMemoryPageSimilarCount}
              onWorkflowViewChange={frame.onWorkflowViewChange}
              rightPanelOpen={frame.rightPanelOpen}
              setRightPanelOpen={frame.setRightPanelOpen}
            />
          </Suspense>
          <span className="sr-only">{frame.copy.currentPagePrefix}: {currentPageLabel}</span>
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

function updateVersionFromResult(result) {
  return firstPresentText(result?.version, result?.artifact?.version);
}
