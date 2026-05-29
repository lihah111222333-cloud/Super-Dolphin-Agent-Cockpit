import React, { useState, useEffect, useMemo, useRef } from 'react';
import { callAPI, getBuildInfo, onBridgeEvent, onAppWillQuit } from './services/api.js';
import { SidebarNav } from './components/SidebarNav.jsx';
import { ProjectModal } from './components/ProjectModal.jsx';
import { UnifiedChatPage } from './pages/UnifiedChatPage.jsx';
import { SystemPromptPage } from './pages/SystemPromptPage.jsx';
import { DagsPage } from './pages/DagsPage.jsx';
import { TasksPage } from './pages/TasksPage.jsx';
import { SkillsPage } from './pages/SkillsPage.jsx';
import { CommandsPage } from './pages/CommandsPage.jsx';
import { MemoryCenterPage } from './pages/MemoryCenterPage.jsx';
import { SharedFilesPage } from './pages/SharedFilesPage.jsx';
import { SettingsPage } from './pages/SettingsPage.jsx';
import { useProjectStore } from './stores/projects.js';
import { useThreadStore } from './stores/threads.js';
import { resolveProjectActionCwd } from './composables/useThreadActions.js';
import { requireDagNodeStatusPayload } from './composables/useDagStatusEventBridge.js';
import { ensureThreadSelectionFresh, isStaleThreadSelectionError, requestHistoryLoad } from './utils/thread-page-utils.js';

const REFRESH_INTERVAL_MS = 10000;
const DAG_DESIGNER_ENABLED_TOOLS = Object.freeze([
  'list_models',
  'prompt_list',
  'command_list',
  'shared_file_list',
  'task_create_dag',
  'task_get_dag',
  'task_dag_apply_ops',
  'task_start_dag',
]);

const NAV_ITEMS = Object.freeze([
  { key: 'chat', icon: '💬', label: 'Chat' },
  { key: 'prompts', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><path d="M12 18v-6"/><path d="M9 15l3-3 3 3"/></svg>', label: '提示词' },
  { key: 'dags', icon: 'D', label: '任务流程' },
  { key: 'tasks', icon: 'T', label: '任务' },
  { key: 'skills', icon: 'S', label: '技能' },
  { key: 'commands', icon: 'C', label: '命令' },
  { key: 'memory-center', icon: 'M', label: '记忆中心' },
  { key: 'memory', icon: 'F', label: '共享文件' },
  { key: 'settings', icon: '..', label: '设置' },
]);

const AGENTS_FIELDS = Object.freeze([
  { key: 'agent_id', label: 'Agent' },
  { key: 'status', label: '状态' },
  { key: 'updated_at', label: '更新时间' },
]);

const TASK_ACK_FIELDS = Object.freeze([
  { key: 'ack_key', label: 'ACK' },
  { key: 'title', label: '标题' },
  { key: 'status', label: '状态' },
  { key: 'assigned_to', label: '负责人' },
]);

const TASK_TRACE_FIELDS = Object.freeze([
  { key: 'trace_id', label: 'Trace' },
  { key: 'span_name', label: 'Span' },
  { key: 'status', label: '状态' },
  { key: 'started_at', label: '开始' },
]);

const COMMAND_FIELDS = Object.freeze([
  { key: 'card_key', label: '命令卡' },
  { key: 'title', label: '标题' },
  { key: 'risk_level', label: '风险级别' },
]);

const MEMORY_FIELDS = Object.freeze([
  { key: 'path', label: '路径' },
  { key: 'updated_by', label: '更新者' },
  { key: 'updated_at', label: '更新时间' },
]);

function countMemorySimilarGroups(memoryCenter) {
  const groups = memoryCenter?.overview?.health?.similarGroups;
  return Array.isArray(groups) ? groups.length : 0;
}

function parseWindowCwdFromSearch(search) {
  const raw = (search || '').toString().trim();
  if (!raw) return '';
  const query = raw.startsWith('?') ? raw.slice(1) : raw;
  try {
    return (new URLSearchParams(query).get('ao_window_cwd') || '').toString().trim();
  } catch (error) {
    throw new Error(`parse window cwd query failed: ${error?.message || error}`);
  }
}

function readWindowCwdFromLocation() {
  if (typeof window === 'undefined') return '';
  return parseWindowCwdFromSearch(window.location?.search || '');
}

async function ensureAppActiveThread(threadStore, projectStore, windowCwd = '') {
  let threadId = threadStore.state.activeThreadId || '';
  if (threadId) return threadId;

  threadId = await threadStore.startThread(resolveProjectActionCwd(projectStore, windowCwd));
  if (!threadId) return '';

  await requestHistoryLoad(threadStore, threadId);
  return threadId;
}

async function handleStartDagDesigner(threadStore, projectStore, windowCwd, setPage, DAG_DESIGNER_ENABLED_TOOLS) {
  const cwd = resolveProjectActionCwd(projectStore, windowCwd);
  const threadId = await threadStore.startThread(cwd, {
    focusMode: 'chat',
    name: 'AI 设计流程',
    agentKey: 'dag_designer',
    promptKey: 'main/dag_designer_zh',
    deferSpawn: true,
    config: {
      enabledTools: [...DAG_DESIGNER_ENABLED_TOOLS],
      providerNativeSkills: false,
    },
  });
  if (threadId) {
    await threadStore.saveActiveThread(threadId);
    setPage('chat');
  }
}

function computeSidebarBadges(memoryCenter) {
  const badges = {};
  const similarCount = countMemorySimilarGroups(memoryCenter);
  if (similarCount > 0) badges['memory-center'] = similarCount;
  return badges;
}

function resolveTasksItems(subTab, dashboard) {
  return subTab === 'acks' ? dashboard.taskAcks : dashboard.taskTraces;
}

function resolveTasksFields(subTab, ackFields, traceFields) {
  return subTab === 'acks' ? ackFields : traceFields;
}

function formatCurrentCwdDisplay(windowCwd, activeProjectCwd) {
  if (activeProjectCwd && activeProjectCwd !== windowCwd) {
    return `当前窗口 CWD：${windowCwd}（活动项目：${activeProjectCwd}）`;
  }
  return `当前窗口 CWD：${windowCwd}`;
}

function handleAppBridgeEvent(evt, runtimeRef) {
  const runtime = runtimeRef.current || {};
  runtime.threadStore?.handleBridgeEvent(evt);
  const eventType = (evt?.type || evt?.params?.type || evt?.payload?.type || evt?.data?.type || '').toString().trim().toLowerCase();
  const method = (evt?.method || evt?.params?.method || evt?.payload?.method || evt?.data?.method || '').toString().trim().toLowerCase();
  const payload = evt?.payload || evt?.data || evt?.params || {};

  if ((method === 'skills/changed' || eventType === 'skills/changed') && runtime.page === 'skills') {
    runtime.refreshDashboardByPage?.('skills').catch((err) => console.warn(err));
  }

  if (method === 'task/node/statuschanged') {
    const statusPayload = requireDagNodeStatusPayload(payload, 'dag node status event payload is required');
    runtime.setDagNodeStatusEvents?.((prev) => [
      ...prev,
      { seq: prev.length + 1, payload: statusPayload },
    ]);
    if (runtime.page === 'dags') {
      runtime.refreshDashboardByPage?.('dags').catch((err) => console.warn(err));
    }
  }
}

export function AppRoot() {
  const projectStore = useProjectStore();
  const threadStore = useThreadStore();

  const [page, setPage] = useState('chat');
  const [isExiting, setIsExiting] = useState(false);
  const [bootstrapError, setBootstrapError] = useState('');
  const [inheritedChatPayload, setInheritedChatPayload] = useState(null);
  const [dagDesignerStarting, setDagDesignerStarting] = useState(false);
  const [tasksSubTab, setTasksSubTab] = useState('acks');
  const [buildInfo, setBuildInfo] = useState({});
  const [runtimeConfig, setRuntimeConfig] = useState({ cwd: '' });
  const [queryWindowCwd, setQueryWindowCwd] = useState(readWindowCwdFromLocation());
  const [dagNodeStatusEvents, setDagNodeStatusEvents] = useState([]);
  
  const [dashboard, setDashboard] = useState({
    agents: [],
    dags: [],
    taskAcks: [],
    taskTraces: [],
    skills: [],
    commandCards: [],
    memory: [],
    finalOutputRefs: [],
    sharedFileRetention: { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
  });

  const [dashboardRequest, setDashboardRequest] = useState({
    dags: { loading: false, error: '' },
  });

  const [memoryCenter, setMemoryCenter] = useState({
    loading: false,
    error: '',
    overview: {},
    private: { entries: [] },
    team: { entries: [] },
  });

  // Keep a ref for prevPage to match Vue watch logic
  const prevPageRef = useRef(page);
  const bridgeRuntimeRef = useRef({});

  // Computeds
  const sidebarBadges = useMemo(() => computeSidebarBadges(memoryCenter), [memoryCenter]);

  const tasksItems = useMemo(() => resolveTasksItems(tasksSubTab, dashboard), [tasksSubTab, dashboard]);

  const tasksFields = useMemo(() => resolveTasksFields(tasksSubTab, TASK_ACK_FIELDS, TASK_TRACE_FIELDS), [tasksSubTab]);

  const windowCwd = useMemo(() => {
    const queryCwd = (queryWindowCwd || '').toString().trim();
    const processCwd = (runtimeConfig.cwd || '').toString().trim();
    if (queryCwd && queryCwd !== '.') {
      return queryCwd;
    }
    if (processCwd && processCwd !== '.') {
      return processCwd;
    }
    return '';
  }, [queryWindowCwd, runtimeConfig.cwd]);

  const activeProjectCwd = useMemo(() => {
    const active = (projectStore.state?.active || '').toString().trim();
    if (!active || active === '.') return windowCwd;
    return active;
  }, [projectStore.state?.active, windowCwd]);

  const currentCwdDisplay = useMemo(() => formatCurrentCwdDisplay(windowCwd, activeProjectCwd), [activeProjectCwd, windowCwd]);

  const threadScopeCwd = useMemo(() => activeProjectCwd || '', [activeProjectCwd]);

  // Actions & Helper Functions
  const updateMemoryCenter = (patch) => {
    setMemoryCenter((prev) => ({ ...prev, ...patch }));
  };

  const refreshBuildInfo = async () => {
    const info = await getBuildInfo();
    setBuildInfo(info || {});
  };

  const refreshRuntimeConfig = async () => {
    const info = await callAPI('config/read', {});
    const cwd = (info?.cwd || '').toString().trim();
    if (!cwd) throw new Error('runtime cwd is required');
    setRuntimeConfig({ cwd });
    return cwd;
  };

  const refreshMemoryCenter = async () => {
    const scopedCwd = threadScopeCwd.toString().trim();
    if (!scopedCwd) return;
    updateMemoryCenter({ loading: true, error: '' });
    try {
      const snapshot = await callAPI('ui/memory/get', { cwd: scopedCwd });
      updateMemoryCenter({
        overview: snapshot?.overview && typeof snapshot.overview === 'object' ? snapshot.overview : {},
        private: snapshot?.private || { entries: [] },
        team: snapshot?.team || { entries: [] },
        loading: false,
      });
    } catch (error) {
      console.warn('refresh memory center failed', error);
      updateMemoryCenter({
        overview: {},
        private: { entries: [] },
        team: { entries: [] },
        error: (error?.message || String(error) || '加载失败').toString(),
        loading: false,
      });
    }
  };

  const refreshDashboardByPage = async (targetPage) => {
    if (targetPage === 'chat' || targetPage === 'settings' || targetPage === 'memory-center') return;
    if (targetPage === 'dags') setDashboardRequest({ dags: { loading: true, error: '' } });
    const cwd = threadScopeCwd.toString().trim();
    try {
      const requiresCwd = targetPage === 'skills' || targetPage === 'commands' || targetPage === 'memory';
      if (requiresCwd && !cwd) throw new Error(`dashboard ${targetPage} cwd is required`);
      const res = await callAPI('ui/dashboard/get', cwd ? { page: targetPage, cwd } : { page: targetPage });
      setDashboard((prev) => ({
        ...prev,
        agents: res.agents || [],
        dags: res.dags || [],
        taskAcks: res.taskAcks || [],
        taskTraces: res.taskTraces || [],
        skills: res.skills || [],
        commandCards: res.commandCards || [],
        memory: res.memory || [],
        finalOutputRefs: res.finalOutputRefs || [],
        sharedFileRetention: res.sharedFileRetention || { items: [], protectedCount: 0, cleanupCandidateCount: 0 },
      }));
    } catch (error) {
      if (targetPage === 'dags') setDashboardRequest({ dags: { loading: false, error: '加载任务流程失败，请稍后重试。' } });
      throw error;
    } finally {
      if (targetPage === 'dags') setDashboardRequest((prev) => ({ dags: { ...prev.dags, loading: false } }));
    }
  };
  bridgeRuntimeRef.current = { threadStore, page, refreshDashboardByPage, setDagNodeStatusEvents };

  const runCommandCard = async (card) => {
    const command = (card?.command_template || '').toString().trim();
    if (!command) return;
    const threadId = await ensureAppActiveThread(threadStore, projectStore, windowCwd);
    if (threadId) {
      await threadStore.sendMessage(threadId, `请执行以下命令并反馈结果：\n${command}`);
      setPage('chat');
    }
  };

  const startDagDesignerThread = async () => {
    if (dagDesignerStarting) return;
    setDagDesignerStarting(true);
    try {
      await handleStartDagDesigner(threadStore, projectStore, windowCwd, setPage, DAG_DESIGNER_ENABLED_TOOLS);
    } finally {
      setDagDesignerStarting(false);
    }
  };

  const openDagChildThread = async (threadId) => {
    const id = (threadId || '').toString().trim();
    if (id) {
      await threadStore.saveActiveThread(id);
      setPage('chat');
    }
  };

  const startInheritedChatFromSharedFile = (payload) => {
    const path = (payload?.sharedFilePath || '').toString().trim();
    if (path) {
      setInheritedChatPayload({ sharedFilePath: path, ts: Date.now() });
      setPage('chat');
    }
  };

  // Watch CWD Changes
  useEffect(() => {
    if (typeof threadStore.setPreferenceScopeCwd === 'function') {
      threadStore.setPreferenceScopeCwd(threadScopeCwd);
    }
    if (!threadScopeCwd) return;

    threadStore.refreshSidebarState({ force: true }).catch((error) => {
      console.warn('refresh threads after scope change failed', error);
    });

    if (page === 'memory-center') {
      refreshMemoryCenter().catch((err) => console.error('refreshMemoryCenter failed:', err));
    } else if (page === 'skills' || page === 'memory') {
      refreshDashboardByPage(page).catch((err) => console.error('refreshDashboardByPage failed:', err));
    }
  }, [threadScopeCwd]);

  // Watch Page Changes (Vue watch equivalent)
  useEffect(() => {
    const prev = prevPageRef.current;
    prevPageRef.current = page;

    if (page === 'chat') {
      if (prev && prev !== 'chat') {
        threadStore.refreshSidebarState();
        const activeThreadId = (threadStore?.state?.activeThreadId || '').toString().trim();
        if (activeThreadId) {
          ensureThreadSelectionFresh(threadStore, activeThreadId, { reason: 'page-enter' }).catch(() => {});
        }
      }
      return;
    }

    if (page === 'memory-center') {
      refreshMemoryCenter().catch((err) => console.error('refreshMemoryCenter failed:', err));
      return;
    }

    refreshDashboardByPage(page).catch((err) => console.error('refreshDashboardByPage failed:', err));
  }, [page]);

  // Lifecycle mount/unmount bootstrap
  useEffect(() => {
    let refreshTimer;
    let unsubscribeBridge = () => {};
    let unsubscribeQuit = () => {};

    const doBootstrap = async () => {
      setBootstrapError('');
      
      // Subscribe to events
      unsubscribeBridge = onBridgeEvent((evt) => {
        handleAppBridgeEvent(evt, bridgeRuntimeRef);
      });

      unsubscribeQuit = onAppWillQuit(() => {
        setIsExiting(true);
      });

      try {
        const [, processCwd] = await Promise.all([refreshBuildInfo(), refreshRuntimeConfig()]);
        
        // Trigger Wails window cwd synchronization
        const queryCwd = (queryWindowCwd || '').toString().trim();
        const processCwdNormalized = (processCwd || '').toString().trim();
        let finalCwd = '';
        if (queryCwd && queryCwd !== '.') {
          finalCwd = queryCwd;
        } else if (processCwdNormalized && processCwdNormalized !== '.') {
          finalCwd = processCwdNormalized;
        }
        projectStore.setScopeCwd?.(finalCwd);
        await projectStore.reloadProjects();
        
        const bootstrapSnap = await callAPI('ui/windowBootstrap/get', {});
        if (bootstrapSnap?.snapshot?.cwd) {
          await projectStore.addProject(bootstrapSnap.snapshot.cwd);
          await projectStore.setActive(bootstrapSnap.snapshot.cwd);
        }
        if (bootstrapSnap?.snapshot?.page) {
          setPage(bootstrapSnap.snapshot.page);
        }

        if (typeof threadStore.setPreferenceScopeCwd === 'function') {
          threadStore.setPreferenceScopeCwd(threadScopeCwd);
        }

        await threadStore.refreshSidebarState();

        const activeThreadId = threadStore.state.activeThreadId || '';
        if (activeThreadId) {
          try {
            await ensureThreadSelectionFresh(threadStore, activeThreadId, { reason: 'bootstrap' });
          } catch (err) {
            if (isStaleThreadSelectionError(err)) {
              await threadStore.saveActiveThread('');
            }
          }
        }
        
        const activeCmdThreadId = threadStore.state.activeCmdThreadId || '';
        if (activeCmdThreadId && activeCmdThreadId !== activeThreadId) {
          ensureThreadSelectionFresh(threadStore, activeCmdThreadId, { reason: 'page-enter' }).catch(() => {});
        }

        refreshMemoryCenter().catch(() => {});

        refreshTimer = setInterval(() => {
          threadStore.refreshSidebarState();
        }, REFRESH_INTERVAL_MS);
      } catch (err) {
        setBootstrapError((err?.message || String(err) || 'bootstrap failed').toString());
        console.error('bootstrap failed:', err);
      }
    };

    doBootstrap();

    const handleExit = () => setIsExiting(true);

    window.addEventListener('beforeunload', handleExit);
    window.addEventListener('pagehide', handleExit);

    return () => {
      window.removeEventListener('beforeunload', handleExit);
      window.removeEventListener('pagehide', handleExit);
      unsubscribeBridge();
      unsubscribeQuit();
      if (refreshTimer) clearInterval(refreshTimer);
    };
  }, []);

  return (
    <div className="app-shell" data-testid="app-shell">
      <SidebarNav 
        items={NAV_ITEMS} 
        page={page} 
        badges={sidebarBadges} 
        onChange={setPage} 
      />

      <main id="content" data-testid={`page-${page}`}>
        {bootstrapError ? (
          <div className="app-fatal" data-testid="bootstrap-error">
            {bootstrapError}
          </div>
        ) : page === 'chat' ? (
          <UnifiedChatPage
            key="chat"
            mode="chat"
            projectStore={projectStore}
            threadStore={threadStore}
            windowCwd={windowCwd}
            cwdDisplay={currentCwdDisplay}
            inheritedChatPayload={inheritedChatPayload}
            onClearInheritedChat={() => setInheritedChatPayload(null)}
          />
        ) : page === 'prompts' ? (
          <SystemPromptPage
            key="prompts"
            projectStore={projectStore}
            windowCwd={windowCwd}
          />
        ) : page === 'dags' ? (
          <DagsPage
            key="dags"
            items={dashboard.dags}
            loading={dashboardRequest.dags.loading}
            error={dashboardRequest.dags.error}
            statusEvents={dagNodeStatusEvents}
            onOpenChat={openDagChildThread}
            onDesignFlow={startDagDesignerThread}
            onRefreshDags={() => refreshDashboardByPage('dags')}
          />
        ) : page === 'tasks' ? (
          <TasksPage
            key="tasks"
            tasksSubTab={tasksSubTab}
            items={tasksItems}
            fields={tasksFields}
            onSubTabChange={setTasksSubTab}
          />
        ) : page === 'skills' ? (
          <SkillsPage
            key="skills"
            skills={dashboard.skills}
            cwd={threadScopeCwd}
            projectStore={projectStore}
            onRefreshSkills={() => refreshDashboardByPage('skills')}
          />
        ) : page === 'commands' ? (
          <CommandsPage
            key="commands"
            commandCards={dashboard.commandCards}
            commandFields={COMMAND_FIELDS}
            onRunCommand={runCommandCard}
          />
        ) : page === 'memory' ? (
          <SharedFilesPage
            key="memory"
            files={dashboard.memory}
            cwd={threadScopeCwd}
            finalOutputRefs={dashboard.finalOutputRefs}
            sharedFileRetention={dashboard.sharedFileRetention}
            onOpenMemoryCenter={() => setPage('memory-center')}
            onRefresh={() => refreshDashboardByPage('memory')}
            onStartInheritedChat={startInheritedChatFromSharedFile}
          />
        ) : page === 'settings' ? (
          <SettingsPage
            key="settings"
            buildInfo={buildInfo}
            projectStore={projectStore}
            onRefresh={refreshBuildInfo}
          />
        ) : null}

        {/* KeepAlive Simulation for Memory Center */}
        <div style={{ display: page === 'memory-center' ? 'block' : 'none' }}>
          <MemoryCenterPage
            model={memoryCenter}
            onRefresh={refreshMemoryCenter}
            onOpenSharedFiles={() => setPage('memory')}
          />
        </div>
      </main>

      <ProjectModal store={projectStore} />
      
      <div className={`app-exit-overlay ${isExiting ? 'active' : ''}`} aria-hidden="true">
        <div className="app-exit-overlay-inner">
          <div className="app-exit-overlay-icon" />
          <div className="app-exit-overlay-text">正在退出…</div>
        </div>
      </div>
    </div>
  );
}
