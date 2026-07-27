import React, { lazy, useMemo } from 'react';
import { APP_COPY } from './shared/i18n/appI18n.js';

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

export function PageLoadingFallback() {
  return (
    <div className="empty-state" aria-live="polite">
      <h2>{APP_COPY.zh.pageLoadingTitle}</h2>
      <p>{APP_COPY.zh.pageLoadingDescription}</p>
    </div>
  );
}

function ChatPageRoute(props) {
  const { copy, projectPath, rightPanelOpen, setRightPanelOpen, shellLayoutStore, store } = props;
  return (
    <ChatPage
      copy={copy.chat}
      store={store}
      projectPath={projectPath}
      rightPanelOpen={rightPanelOpen}
      shellLayoutStore={shellLayoutStore}
      setRightPanelOpen={setRightPanelOpen}
    />
  );
}

function PromptPageRoute({ copy, projectPath, refreshKey, store: sourceStore }) {
  const resolveLaunchPreferences = sourceStore.resolveLaunchPreferences;
  const notifyAction = sourceStore.notifyAction;
  const store = useMemo(
    () => ({ notifyAction, resolveLaunchPreferences }),
    [notifyAction, resolveLaunchPreferences],
  );
  return <PromptPage copy={copy.prompts} projectPath={projectPath} store={store} refreshKey={refreshKey} />;
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
    copy,
    memoryRevision,
    onWorkflowViewChange,
    projectPath,
    rightPanelOpen,
    shellLayoutStore,
    shortcutController,
    setMemoryPageSimilarCount,
    setRightPanelOpen,
    store,
  } = props;
  if (activePage === 'chat') {
    return (
      <ChatPageRoute
        copy={copy}
        projectPath={projectPath}
        rightPanelOpen={rightPanelOpen}
        shellLayoutStore={shellLayoutStore}
        setRightPanelOpen={setRightPanelOpen}
        store={store}
      />
    );
  }
  if (activePage === 'prompts') return <PromptPageRoute copy={copy} projectPath={projectPath} refreshKey={store.promptRevision} store={store} />;
  if (activePage === 'workflows') return <WorkflowPageRoute copy={copy} onWorkflowViewChange={onWorkflowViewChange} projectPath={projectPath} refreshKey={store.workflowRevision} store={store} />;
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
  if (activePage === 'files') return <FilesPageRoute copy={copy} projectPath={projectPath} store={store} />;
  if (activePage === 'observability') return <ObservabilityPage copy={copy.observability} />;
  if (activePage === 'settings') return <SettingsPage copy={copy.settings} projectPath={projectPath} shortcutController={shortcutController} />;
  return null;
}
