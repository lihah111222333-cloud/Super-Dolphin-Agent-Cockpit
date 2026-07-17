import React, { useCallback, useEffect, useRef, useState } from 'react';
import { Folder, Plus, SquarePlus } from 'lucide-react';
import { getSidebarState } from './shared/api/backendApi.js';
import { runBackgroundAction, runUIAction } from './shared/ui/runUIAction.js';
import { APP_COPY } from './shared/i18n/appI18n.js';
import { currentTimestampMillis, errorMessage, textValue } from './pages/shared/pageShared.js';
import {
  mergeProjectThreadSources,
  projectDirectoryItems,
  projectThreadItems,
  projectThreadLabel,
  projectTreeKey,
  sidebarActiveProjectPath,
  sidebarSnapshotThreads,
  taskThreadItems,
} from './WorkbenchSidebarModel.js';
import { formatRelativeTime, useSidebarThreadActions } from './WorkbenchSidebarThreadModel.js';
import { SidebarThreadRow } from './WorkbenchSidebarThreads.jsx';

const PROJECT_THREAD_CACHE_TTL_MS = 45_000;

function uiActionOptions(store) {
  return {
    onError: (error) => {
      store?.addWarning?.('error', 'ui.action.failed', { error: errorMessage(error) });
    },
  };
}

function patchProjectThreadCacheEntry(current, path, update) {
  const key = projectTreeKey(path);
  if (!key) return current;
  const previous = current[key] || { path, threads: [], loadedAt: 0, loading: false, error: '' };
  const patch = typeof update === 'function' ? update(previous) : update;
  return {
    ...current,
    [key]: {
      ...previous,
      path,
      ...patch,
    },
  };
}

function renameProjectThreadCacheEntries(current, id, name) {
  let changed = false;
  const next = Object.fromEntries(Object.entries(current).map(([key, entry]) => {
    const threads = Array.isArray(entry?.threads) ? entry.threads : [];
    const nextThreads = threads.map((thread) => {
      if (thread?.id !== id) return thread;
      changed = true;
      return { ...thread, name, title: name };
    });
    return [key, { ...entry, threads: nextThreads }];
  }));
  return changed ? next : current;
}

function removeProjectThreadCacheEntries(current, ids) {
  let changed = false;
  const next = Object.fromEntries(Object.entries(current).map(([key, entry]) => {
    const threads = Array.isArray(entry?.threads) ? entry.threads : [];
    const nextThreads = threads.filter((thread) => !ids.has(textValue(thread?.id)));
    if (nextThreads.length !== threads.length) changed = true;
    return [key, { ...entry, threads: nextThreads }];
  }));
  return changed ? next : current;
}

function refreshProjectThreadCacheEntry(props) {
  const { force = false, path, projectThreadCacheRef, updateProjectThreadCache } = props;
  const key = projectTreeKey(path);
  if (!key) return;
  const current = projectThreadCacheRef.current[key];
  if (!force && current && !current.error && currentTimestampMillis('project thread cache clock') - current.loadedAt < PROJECT_THREAD_CACHE_TTL_MS) return;
  updateProjectThreadCache(path, (previous) => ({
    threads: Array.isArray(previous.threads) ? previous.threads : [],
    loading: true,
    error: '',
  }));
  runBackgroundAction('sidebar.project-threads.load', async () => getSidebarState({ cwd: path }))
    .then((snapshot) => {
      updateProjectThreadCache(path, {
        threads: sidebarSnapshotThreads(snapshot),
        loadedAt: currentTimestampMillis('project thread cache loadedAt'),
        loading: false,
        error: '',
      });
    })
    .catch(() => {
      updateProjectThreadCache(path, (previous) => ({
        threads: Array.isArray(previous.threads) ? previous.threads : [],
        loading: false,
        error: '项目线程加载失败，请查看 Health。',
      }));
    });
}

function useActiveProjectThreadCacheSync(props) {
  const { activeProjectPath, store, updateProjectThreadCache } = props;
  useEffect(() => {
    const activeKey = projectTreeKey(activeProjectPath);
    if (!activeKey) return;
    if (store?.bootstrapStatus !== 'ready') return;
    const loadingKey = projectTreeKey(store?.chatSurfaceLoadingCwd);
    if (loadingKey && loadingKey === activeKey) return;
    // 只在全局 bootstrap 完成后同步可信来源，避免刷新初期把临时 activeProject 的空列表写成新鲜缓存。
    const sidebarThreadCache = store?.sidebarThreadsByProject && typeof store.sidebarThreadsByProject === 'object' ? store.sidebarThreadsByProject : {};
    const hasSidebarThreads = Object.prototype.hasOwnProperty.call(sidebarThreadCache, activeKey);
    const sidebarThreads = hasSidebarThreads ? sidebarThreadCache[activeKey] : null;
    const hasStoreThreads = Array.isArray(store?.threads) && store.threads.length > 0;
    if (!hasSidebarThreads && !hasStoreThreads) return;
    const sourceThreads = hasSidebarThreads ? sidebarThreads : store?.threads;
    updateProjectThreadCache(activeProjectPath, {
      threads: Array.isArray(sourceThreads) ? sourceThreads : [],
      loadedAt: currentTimestampMillis('project thread cache loadedAt'),
      loading: false,
      error: '',
    });
  }, [activeProjectPath, store?.bootstrapStatus, store?.chatSurfaceLoadingCwd, store?.sidebarThreadsByProject, store?.threads, updateProjectThreadCache]);
}

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
  const { activeProjectPath, expandedProjects, item, projectThreadCache, store } = props;
  const projectKey = projectTreeKey(item.path);
  const isActiveProject = Boolean(item.path && projectKey === projectTreeKey(activeProjectPath));
  const cacheEntry = projectThreadCache[projectKey];
  const sidebarThreads = store?.sidebarThreadsByProject?.[projectKey];
  const cachedThreads = cacheEntry?.threads;
  const activeThreads = isActiveProject ? mergeProjectThreadSources(cachedThreads || sidebarThreads, store?.threads) : (cachedThreads || sidebarThreads);
  const sourceThreads = Array.isArray(activeThreads) ? activeThreads : [];
  const projectThreads = projectThreadItems(sourceThreads, item.path, cacheEntry ? item.path : activeProjectPath, {
    allowMissingCwdFallback: !cacheEntry,
  });
  const hasExplicitState = Object.prototype.hasOwnProperty.call(expandedProjects, item.path);
  const isExpanded = hasExplicitState ? !!expandedProjects[item.path] : isActiveProject;
  return {
    isActiveProject,
    isExpanded,
    newProjectThreadLabel: `${props.copy.newChat} ${item.name}`,
    showLoading: isExpanded && cacheEntry?.loading && projectThreads.length === 0,
    visibleThreads: isExpanded ? projectThreads : [],
  };
}

function ProjectTreeItem(props) {
  const { copy, expandedProjects, item, onSelectProject, onSelectThread, onStartProjectThread, projectThreadCache, store, threadActions } = props;
  const view = projectTreeItemViewState({
    activeProjectPath: props.activeProjectPath,
    copy,
    expandedProjects,
    item,
    projectThreadCache,
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
  const [projectThreadCache, setProjectThreadCache] = useState({});
  const projectThreadCacheRef = useRef({});
  const actionOptions = uiActionOptions(store);
  const projectItems = projectDirectoryItems(projectPath, store?.projects, store?.activeProject);
  const activeProjectPath = sidebarActiveProjectPath(store?.activeProject, projectPath);
  useEffect(() => {
    projectThreadCacheRef.current = projectThreadCache;
  }, [projectThreadCache]);
  const updateProjectThreadCache = useCallback((path, update) => {
    const key = projectTreeKey(path);
    if (!key) return;
    setProjectThreadCache((current) => patchProjectThreadCacheEntry(current, path, update));
  }, []);
  const renameCachedProjectThread = useCallback((threadId, name) => {
    const id = textValue(threadId);
    if (!id) return;
    setProjectThreadCache((current) => renameProjectThreadCacheEntries(current, id, name));
  }, []);
  const removeCachedProjectThreads = useCallback((threadIds = []) => {
    const ids = new Set((Array.isArray(threadIds) ? threadIds : [threadIds]).map(textValue).filter(Boolean));
    if (ids.size === 0) return;
    setProjectThreadCache((current) => removeProjectThreadCacheEntries(current, ids));
  }, []);
  const threadActions = useSidebarThreadActions(store, {
    onDeleteThreads: removeCachedProjectThreads,
    onRenameThread: renameCachedProjectThread,
  });
  const refreshProjectThreadCache = useCallback((path, options = {}) => {
    refreshProjectThreadCacheEntry({ force: options.force, path, projectThreadCacheRef, updateProjectThreadCache });
  }, [updateProjectThreadCache]);
  useActiveProjectThreadCacheSync({ activeProjectPath, store, updateProjectThreadCache });
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
    if (nextExpanded) refreshProjectThreadCache(path);
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
            projectThreadCache={projectThreadCache}
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
  const actionOptions = uiActionOptions(store);
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
