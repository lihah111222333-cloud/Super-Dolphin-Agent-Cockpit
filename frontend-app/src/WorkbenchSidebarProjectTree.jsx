import React, { useState } from 'react';
import { Folder, Plus, SquarePlus } from 'lucide-react';
import { runUIAction } from './shared/ui/runUIAction.js';
import { uiActionWarningOptions } from './shared/ui/uiActionWarningOptions.js';
import { APP_COPY } from './shared/i18n/appI18n.js';
import {
  projectDirectoryItems,
  projectThreadItems,
  projectThreadLabel,
  projectTreeKey,
  sidebarActiveProjectPath,
  taskThreadItems,
} from './WorkbenchSidebarModel.js';
import { formatRelativeTime, useSidebarThreadActions } from './WorkbenchSidebarThreadModel.js';
import { SidebarThreadRow } from './WorkbenchSidebarThreads.jsx';

async function startProjectThreadAction(props) {
  const { activeProjectPath, path, setActivePage, store } = props;
  if (projectTreeKey(path) !== projectTreeKey(activeProjectPath)) {
    const switched = await store?.setActiveProjectPath?.(path);
    if (switched === false) return;
  }
  store?.newThread?.();
  setActivePage('chat');
}

async function selectProjectThreadAction(props) {
  const { activeProjectPath, path, selectionIntent, store, threadId } = props;
  if (path && projectTreeKey(path) !== projectTreeKey(activeProjectPath)) {
    const switched = await store?.setActiveProjectPath?.(path, {
      preserveActiveThreadId: true,
      selectionIntent,
    });
    if (switched === false) {
      store?.cancelOpeningThread?.(selectionIntent);
      return;
    }
  }
  return store?.setActiveThread?.(threadId, { selectionIntent });
}

function ProjectThreadEntry(props) {
  const { activeThreadId, copy, itemPath, onSelectThread, thread, threadActions } = props;
  const label = projectThreadLabel(thread);
  const active = thread.id === activeThreadId;
  if (thread.id) {
    return (
      <SidebarThreadRow
        key={thread.id}
        active={active}
        copy={copy}
        label={label}
        onSelect={() => onSelectThread(thread, itemPath)}
        openLabel={`${copy.openProjectThread}：${label}`}
        thread={thread}
        threadActions={threadActions}
      />
    );
  }
  return (
    <li key={thread.id || label}>
      <button
        type="button"
        className={`sidebar-project-thread${active ? ' active' : ''}`}
        onClick={() => onSelectThread(thread, itemPath)}
        aria-label={`${copy.openProjectThread}：${label}`}
        title={label}
      >
        <span className="sidebar-thread-title">{label}</span>
        {thread.updatedAt ? (
          <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt, copy.workbench.relativeTime)}</span>
        ) : null}
      </button>
    </li>
  );
}

function ProjectThreadList(props) {
  const { activeThreadId, copy, item, onSelectThread, showLoading, threadActions, visibleThreads } = props;
  if (visibleThreads.length > 0) {
    return visibleThreads.map((thread) => (
      <ProjectThreadEntry
        key={thread.id || projectThreadLabel(thread)}
        activeThreadId={activeThreadId}
        copy={copy}
        itemPath={item.path}
        onSelectThread={onSelectThread}
        thread={thread}
        threadActions={threadActions}
      />
    ));
  }
  if (showLoading) return <li className="sidebar-project-thread-empty">{copy.loadingThreads}</li>;
  return null;
}

function projectTreeItemViewState(props) {
  const { activeProjectPath, expandedProjects, item, store } = props;
  const projectKey = projectTreeKey(item.path);
  const isActiveProject = Boolean(item.path && projectKey === projectTreeKey(activeProjectPath));
  const sidebarThreads = store?.sidebarThreadsByProject?.[projectKey];
  const hasSidebarThreads = Array.isArray(sidebarThreads);
  const projectThreads = projectThreadItems(hasSidebarThreads ? sidebarThreads : [], item.path, hasSidebarThreads ? item.path : activeProjectPath, {
    allowMissingCwdFallback: hasSidebarThreads,
  });
  const hasExplicitState = Object.prototype.hasOwnProperty.call(expandedProjects, item.path);
  const isExpanded = hasExplicitState ? !!expandedProjects[item.path] : isActiveProject;
  return {
    isActiveProject,
    isExpanded,
    newProjectThreadLabel: `${props.copy.newChat} ${item.name}`,
    showLoading: isExpanded && projectTreeKey(store?.chatSurfaceLoadingCwd) === projectKey && projectThreads.length === 0,
    visibleThreads: isExpanded ? projectThreads : [],
  };
}

function ProjectTreeItem(props) {
  const { copy, expandedProjects, item, onSelectProject, onSelectThread, onStartProjectThread, store, threadActions } = props;
  const view = projectTreeItemViewState({
    activeProjectPath: props.activeProjectPath,
    copy,
    expandedProjects,
    item,
    store,
  });
  return (
    <div className="sidebar-tree-project">
      <div className="sidebar-project-header">
        <button
          type="button"
          className={`sidebar-tree-folder${view.isActiveProject ? ' active' : ''}`}
          onClick={() => onSelectProject(item.path)}
          aria-expanded={view.isExpanded}
          aria-label={`${copy.selectProject} ${item.name}`}
        >
          <Folder size={18} aria-hidden="true" />
          <span>{item.name}</span>
        </button>
        <button
          type="button"
          className="sidebar-icon-action sidebar-project-new-thread"
          onClick={(event) => onStartProjectThread(item.path, event)}
          aria-label={view.newProjectThreadLabel}
          title={view.newProjectThreadLabel}
        >
          <SquarePlus size={16} aria-hidden="true" />
        </button>
      </div>
      <ul className="sidebar-project-thread-list" aria-label={`${item.name} ${copy.projectChatsSuffix}`}>
        <ProjectThreadList
          activeThreadId={store?.activeThreadId}
          copy={copy}
          item={item}
          onSelectThread={onSelectThread}
          showLoading={view.showLoading}
          threadActions={threadActions}
          visibleThreads={view.visibleThreads}
        />
        {view.isExpanded && view.visibleThreads.length === 0 && !view.showLoading ? <li className="sidebar-project-thread-empty">{copy.emptyThreads}</li> : null}
      </ul>
    </div>
  );
}

export function SidebarProjectTree({ copy = APP_COPY.zh.workbench, projectPath, setActivePage, store }) {
  const [expandedProjects, setExpandedProjects] = useState({});
  const actionOptions = uiActionWarningOptions(store);
  const projectItems = projectDirectoryItems(projectPath, store?.projects, store?.activeProject);
  const activeProjectPath = sidebarActiveProjectPath(store?.activeProject, projectPath);
  const threadActions = useSidebarThreadActions(store);
  const addProject = () => runUIAction('project.add', async () => {
    const added = await store?.addProjectFromPicker?.();
    if (added) setActivePage('chat');
  }, actionOptions);
  const selectProject = (path) => {
    if (!path) return;
    const hasExplicitState = Object.prototype.hasOwnProperty.call(expandedProjects, path);
    const currentExpanded = hasExplicitState ? !!expandedProjects[path] : projectTreeKey(path) === projectTreeKey(activeProjectPath);
    const nextExpanded = !currentExpanded;
    setExpandedProjects((current) => {
      return {
        ...current,
        [path]: nextExpanded,
      };
    });
    if (nextExpanded) store?.refreshSidebarSnapshotForCwdInBackground?.(path);
  };
  const startProjectThread = (path, event) => {
    event?.stopPropagation?.();
    if (!path) return;
    runUIAction('thread.new', () => startProjectThreadAction({ activeProjectPath, path, setActivePage, store }), actionOptions);
  };
  const selectThread = (thread, path) => {
    const threadId = typeof thread === 'object' ? thread?.id : thread;
    if (!threadId) return;
    const selectionIntent = store?.beginOpeningThread?.(thread);
    if (!selectionIntent) return;
    setActivePage('chat');
    runUIAction('thread.select', () => selectProjectThreadAction({ activeProjectPath, path, selectionIntent, store, threadId }), actionOptions);
  };
  return (
    <section className="sidebar-project-tree" aria-label={copy.projects}>
      <div className="sidebar-section-heading">
        <span className="sidebar-section-title">
          <span>{copy.projects}</span>
        </span>
        <button type="button" className="sidebar-icon-action" aria-label={copy.addProject} onClick={addProject}>
          <Plus size={16} aria-hidden="true" />
        </button>
      </div>
      <div className="sidebar-tree-root">
        {projectItems.map((item) => (
          <ProjectTreeItem
            key={item.path || item.name}
            activeProjectPath={activeProjectPath}
            copy={copy}
            expandedProjects={expandedProjects}
            item={item}
            onSelectProject={selectProject}
            onSelectThread={selectThread}
            onStartProjectThread={startProjectThread}
            store={store}
            threadActions={threadActions}
          />
        ))}
      </div>
    </section>
  );
}

export function SidebarTaskSummary({ copy = APP_COPY.zh.workbench, store, setActivePage }) {
  const tasks = taskThreadItems(store?.threads);
  const threadActions = useSidebarThreadActions(store);
  const actionOptions = uiActionWarningOptions(store);
  const selectThread = (threadId) => {
    if (!threadId) return;
    setActivePage('chat');
    runUIAction('thread.select', () => store?.setActiveThread?.(threadId), actionOptions);
  };
  const taskEntry = (thread) => (
    <SidebarTaskThreadEntry
      key={thread.id || projectThreadLabel(thread)}
      activeThreadId={store?.activeThreadId}
      copy={copy}
      onSelectThread={selectThread}
      thread={thread}
      threadActions={threadActions}
    />
  );
  return (
    <section className="sidebar-task-summary" aria-label={copy.task}>
      <h2>{copy.task}</h2>
      {tasks.length > 0 ? (
        <ul className="sidebar-task-list" aria-label={copy.taskDialogs}>
          {tasks.map(taskEntry)}
        </ul>
      ) : <p>{copy.emptyTasks}</p>}
    </section>
  );
}

function SidebarTaskThreadEntry(props) {
  const { activeThreadId, copy, onSelectThread, thread, threadActions } = props;
  const label = projectThreadLabel(thread);
  const active = thread.id === activeThreadId;
  if (thread.id) {
    return (
      <SidebarThreadRow
        active={active}
        copy={copy}
        label={label}
        onSelect={() => onSelectThread(thread.id)}
        openLabel={`${copy.openTask}：${label}`}
        thread={thread}
        threadActions={threadActions}
      />
    );
  }
  return (
    <li>
      <button
        type="button"
        className={`sidebar-task-thread${active ? ' active' : ''}`}
        onClick={() => onSelectThread(thread.id)}
        aria-label={`${copy.openTask}：${label}`}
        title={label}
      >
        <span className="sidebar-thread-title">{label}</span>
        {thread.updatedAt ? (
          <span className="sidebar-thread-time" aria-hidden="true">{formatRelativeTime(thread.updatedAt, copy.workbench.relativeTime)}</span>
        ) : null}
      </button>
    </li>
  );
}
