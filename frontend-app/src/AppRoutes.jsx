import React, { lazy } from 'react';
import { APP_COPY } from './shared/i18n/appI18n.js';

function lazyNamedPage(loader, exportName) {
  return lazy(() => loader().then((module) => ({ default: module[exportName] })));
}

const ChatPage = lazyNamedPage(() => import('./pages/chat/ChatPage.jsx'), 'ChatPage');
const FilesPage = lazyNamedPage(() => import('./pages/files/FilesPage.jsx'), 'FilesPage');
const ObservabilityPage = lazyNamedPage(() => import('./pages/observability/ObservabilityPage.jsx'), 'ObservabilityPage');
const SettingsPage = lazyNamedPage(() => import('./pages/settings/SettingsPage.jsx'), 'SettingsPage');
const WorkflowPage = lazyNamedPage(() => import('./pages/workflows/WorkflowPage.jsx'), 'WorkflowPage');

export function PageLoadingFallback() {
  return (
    <div className="empty-state" aria-live="polite">
      <h2>{APP_COPY.zh.pageLoadingTitle}</h2>
      <p>{APP_COPY.zh.pageLoadingDescription}</p>
    </div>
  );
}

function ChatPageRoute(props) {
  const { copy, geometrySnapshot, layoutActions, projectPath, rightPanelOpen, setRightPanelOpen, store } = props;
  return (
    <ChatPage
      actionsHostedInTopBar
      copy={copy.chat}
      geometrySnapshot={geometrySnapshot}
      layoutActions={layoutActions}
      store={store}
      projectPath={projectPath}
      rightPanelOpen={rightPanelOpen}
      setRightPanelOpen={setRightPanelOpen}
    />
  );
}

function WorkflowPageRoute({ copy, onWorkflowViewChange, projectPath, refreshKey, store }) {
  return <WorkflowPage copy={copy.workflow} onWorkflowViewChange={onWorkflowViewChange} projectPath={projectPath} store={store} refreshKey={refreshKey} />;
}

function FilesPageRoute({ copy, projectPath, store }) {
  return <FilesPage copy={copy.files} projectPath={projectPath} store={store} />;
}

export function ActivePageContent(props) {
  const {
    activePage,
    appearance,
    copy,
    geometrySnapshot,
    layoutActions,
    onWorkflowViewChange,
    projectPath,
    rightPanelOpen,
    shortcutController,
    setRightPanelOpen,
    store,
  } = props;
  if (activePage === 'chat') {
    return (
      <ChatPageRoute
        copy={copy}
        geometrySnapshot={geometrySnapshot}
        layoutActions={layoutActions}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        setRightPanelOpen={setRightPanelOpen}
        store={store}
      />
    );
  }
  if (activePage === 'workflows') return <WorkflowPageRoute copy={copy} onWorkflowViewChange={onWorkflowViewChange} projectPath={projectPath} refreshKey={store.workflowRevision} store={store} />;
  if (activePage === 'files') return <FilesPageRoute copy={copy} projectPath={projectPath} store={store} />;
  if (activePage === 'observability') return <ObservabilityPage copy={copy.observability} />;
  if (activePage === 'settings') return <SettingsPage appearance={appearance} copy={copy.settings} projectPath={projectPath} shortcutController={shortcutController} />;
  return null;
}
