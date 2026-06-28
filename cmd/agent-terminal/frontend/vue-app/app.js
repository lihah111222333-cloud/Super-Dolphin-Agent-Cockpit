import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from '../lib/vue.esm-browser.prod.js';
import { callAPI, getBuildInfo, onBridgeEvent, onAppWillQuit } from './services/api.js';
import { SidebarNav } from './components/SidebarNav.js';
import { ProjectModal } from './components/ProjectModal.js';
import { DagsPage } from './pages/DagsPage.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.js';
import { ensureThreadSelectionFresh, isStaleThreadSelectionError, requestHistoryLoad } from './utils/thread-page-utils.js';
import { SkillsPage } from './pages/SkillsPage.js';
import { TasksPage } from './pages/TasksPage.js';
import { CommandsPage } from './pages/CommandsPage.js';
import { SettingsPage } from './pages/SettingsPage.ts';
import { SystemPromptPage } from './pages/SystemPromptPage.js';
import { MemoryCenterPage } from './pages/MemoryCenterPage.js';
import { SharedFilesPage } from './pages/SharedFilesPage.js';
import { useProjectStore } from './stores/projects.js';
import { useThreadStore } from './stores/threads.js';
import { resolveProjectActionCwd } from './composables/useThreadActions.js';
import { requireDagNodeStatusPayload } from './composables/useDagStatusEventBridge.js';

/**
 * @typedef {'chat' | 'agents' | 'dags' | 'tasks' | 'skills' | 'commands' | 'memory-center' | 'memory' | 'settings'} AppPage
 * @typedef {{ refreshSidebarState?: () => Promise<void>, state?: { activeThreadId?: string, activeCmdThreadId?: string } }} ChatRefreshThreadStore
 * @typedef {{ command_template?: string }} CommandCard

 * @typedef {{ type?: string, method?: string, params?: { type?: string, method?: string }, payload?: { type?: string, method?: string }, data?: { type?: string, method?: string } }} BridgeEventEnvelope
 */
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
  { key: 'prompts', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><path d="M12 18v-6"/><path d="M9 15l3-3 3 3"/></svg>', label: '提示词' },
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

function createSidebarBadges(memoryCenter) {
  return computed(() => {
    const badges = {};
    const similarCount = countMemorySimilarGroups(memoryCenter);
    if (similarCount > 0) badges['memory-center'] = similarCount;
    return badges;
  });
}

function refreshScopedPageAfterCwdChange(currentPage, deps) {
  if (currentPage === 'memory-center') {
    deps.refreshMemoryCenter().catch((error) => {
      console.warn('refresh memory center after scope change failed', error);
    });
    return;
  }
  if (currentPage === 'skills') {
    deps.refreshDashboardByPage('skills').catch((error) => {
      console.warn('refresh skills after scope change failed', error);
    });
    return;
  }
  if (currentPage === 'memory') {
    deps.refreshDashboardByPage('memory').catch((error) => {
      console.warn('refresh shared files after scope change failed', error);
    });
  }
}

const SHARED_FILES_TIPS = Object.freeze([
  '适合放命令输出摘录、待整理笔记、交接清单。',
  '确认值得长期保留的内容，请转到“记忆中心”查看长期记忆。',
]);

function resetMemoryCenterState(state) {
  state.overview = {};
  state.private = { entries: [] };
  state.team = { entries: [] };
}

function requireArrayField(payload, key, label) {
  const value = payload?.[key];
  if (!Array.isArray(value)) throw new Error(`dashboard ${label} must be an array`);
  return value;
}

function requireSharedFileRetention(payload) {
  const value = payload?.sharedFileRetention;
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new Error('dashboard sharedFileRetention must be an object');
  }
  if (!Array.isArray(value.items)) throw new Error('dashboard sharedFileRetention.items must be an array');
  return value;
}

function dashboardPageRequiresCwd(page) {
  return page === 'skills' || page === 'commands' || page === 'memory';
}

function applyDashboardPagePayload(dashboard, res) {
  if (!res || typeof res !== 'object' || Array.isArray(res)) throw new Error('dashboard response must be an object');
  dashboard.agents = requireArrayField(res, 'agents', 'agents');
  dashboard.dags = requireArrayField(res, 'dags', 'dags');
  dashboard.taskAcks = requireArrayField(res, 'taskAcks', 'taskAcks');
  dashboard.taskTraces = requireArrayField(res, 'taskTraces', 'taskTraces');
  dashboard.skills = requireArrayField(res, 'skills', 'skills');
  dashboard.commandCards = requireArrayField(res, 'commandCards', 'commandCards');
  dashboard.memory = requireArrayField(res, 'memory', 'memory');
  dashboard.finalOutputRefs = requireArrayField(res, 'finalOutputRefs', 'finalOutputRefs');
  dashboard.sharedFileRetention = requireSharedFileRetention(res);
}

function applyMemoryCenterSnapshot(state, snapshot) {
  if (!snapshot || typeof snapshot !== 'object') throw new Error('memory center snapshot must be an object');
  if (!snapshot.private || typeof snapshot.private !== 'object') throw new Error('memory center private snapshot must be an object');
  if (!snapshot.team || typeof snapshot.team !== 'object') throw new Error('memory center team snapshot must be an object');
  state.overview = snapshot.overview && typeof snapshot.overview === 'object' ? snapshot.overview : {};
  state.private = snapshot.private;
  state.team = snapshot.team;
}

export function routeDagBridgeEvent(method, eventType, payload, deps) {
  const key = (method || eventType || '').toString().trim().toLowerCase();
  if (key !== 'task/node/statuschanged') return;
  const statusPayload = requireDagNodeStatusPayload(payload, 'dag node status event payload is required');
  if (typeof deps?.recordDagNodeStatusEvent !== 'function') throw new Error('dag node status event recorder is required');
  deps.recordDagNodeStatusEvent(statusPayload);
  if (deps?.page?.value === 'dags') {
    if (typeof deps.refreshDashboardByPage !== 'function') throw new Error('dag dashboard refresh handler is required');
    deps.refreshDashboardByPage('dags').catch((err) => {
      console.warn('refresh dag list after node event failed', err);
    });
  }
}

function createDagNodeStatusEventRecorder(target) {
  let seq = 0;
  return (payload) => {
    const next = {
      seq: ++seq,
      payload: payload && typeof payload === 'object' ? { ...payload } : payload,
    };
    target.value = [...target.value, next];
  };
}

async function refreshRuntimeConfigState(runtimeConfig) {
  const info = await callAPI('config/read', {});
  const cwd = (info?.cwd || '').toString().trim();
  runtimeConfig.cwd = cwd;
}

export function parseWindowCwdFromSearch(search) {
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

async function refreshMemoryCenterState(memoryCenter, cwd) {
  memoryCenter.loading = true;
  memoryCenter.error = '';
  try {
    const scopedCwd = (cwd || '').toString().trim();
    if (!scopedCwd) throw new Error('memory center cwd is required');
    const snapshot = await callAPI('ui/memory/get', { cwd: scopedCwd });
    applyMemoryCenterSnapshot(memoryCenter, snapshot);
  } catch (error) {
    console.warn('refresh memory center failed', error);
    resetMemoryCenterState(memoryCenter);
    memoryCenter.error = (error?.message || String(error) || '加载失败').toString();
    throw error;
  } finally {
    memoryCenter.loading = false;
  }
}

async function loadWindowBootstrapSnapshot() {
  const result = await callAPI('ui/windowBootstrap/get', {});
  if (!result || typeof result !== 'object') throw new Error('window bootstrap response must be an object');
  if (!Object.prototype.hasOwnProperty.call(result, 'snapshot')) {
    throw new Error('window bootstrap response snapshot is required');
  }
  if (result.snapshot == null) return {};
  if (typeof result.snapshot !== 'object' || Array.isArray(result.snapshot)) {
    throw new Error('window bootstrap snapshot must be an object');
  }
  return result.snapshot;
}

async function applyWindowBootstrapSnapshot(snapshot, projectStore, threadStore, pageRef, windowCwd = '') {
  if (!snapshot || typeof snapshot !== 'object' || Array.isArray(snapshot)) {
    throw new Error('window bootstrap snapshot payload must be an object');
  }
  const payload = snapshot;
  const cwd = (payload.cwd || '').toString().trim();
  if (cwd) {
    await projectStore.addProject(cwd);
    await projectStore.setActive(cwd);
  }
  const nextPage = (payload.page || '').toString().trim();
  if (nextPage) {
    pageRef.value = nextPage;
  }
}

async function ensureAppActiveThread(threadStore, projectStore, windowCwd = '') {
  let threadId = threadStore.state.activeThreadId || '';
  if (threadId) return threadId;

  threadId = await threadStore.startThread(resolveProjectActionCwd(projectStore, windowCwd));
  if (!threadId) return '';

  await requestHistoryLoad(threadStore, threadId);
  return threadId;
}

async function clearStaleThreadSelection(threadStore, threadId, reason) {
  const id = (threadId || '').toString().trim();
  if (!id || !threadStore?.state) return;
  const activeThreadId = (threadStore.state.activeThreadId || '').toString().trim();
  const activeCmdThreadId = (threadStore.state.activeCmdThreadId || '').toString().trim();
  if (activeThreadId === id) {
    if (typeof threadStore.saveActiveThread === 'function') await threadStore.saveActiveThread('');
    else threadStore.state.activeThreadId = '';
  }
  if (activeCmdThreadId === id) {
    if (typeof threadStore.saveActiveCmdThread === 'function') await threadStore.saveActiveCmdThread('');
    else threadStore.state.activeCmdThreadId = '';
  }
  console.warn('cleared stale thread selection after session loss', { thread_id: id, reason });
}

async function ensureBootstrapThreadSelectionFresh(threadStore, threadId, options) {
  try {
    return await ensureThreadSelectionFresh(threadStore, threadId, options);
  } catch (error) {
    if (!isStaleThreadSelectionError(error)) throw error;
    await clearStaleThreadSelection(threadStore, threadId, options?.reason || 'bootstrap');
    return { requestedHistory: false, syncedThreadState: false, forcedHistoryReload: false };
  }
}

async function runCommandCardForApp(card, threadStore, projectStore, pageRef, windowCwd = '') {
  const command = (card?.command_template || '').toString().trim();
  if (!command) return;
  const threadId = await ensureAppActiveThread(threadStore, projectStore, windowCwd);
  if (!threadId) return;

  await threadStore.sendMessage(threadId, `请执行以下命令并反馈结果：\n${command}`);
  pageRef.value = 'chat';
}

function dashboardPageErrorText(targetPage) {
  if (targetPage === 'dags') return '加载任务流程失败，请稍后重试。';
  return '加载页面失败，请稍后重试。';
}

async function startDagDesignerThreadForApp(_context, deps) {
  const cwd = resolveProjectActionCwd(deps.projectStore, deps.windowCwd);
  const threadId = await deps.threadStore.startThread(cwd, {
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
  if (!threadId) return;
  await deps.threadStore.saveActiveThread(threadId);
  deps.page.value = 'chat';
}

async function startDagDesignerThreadOnceForApp(context, page, projectStore, threadStore, windowCwdRef, inFlight) {
  if (inFlight.value) return;
  inFlight.value = true;
  try {
    await startDagDesignerThreadForApp(context, {
      page,
      projectStore,
      threadStore,
      windowCwd: windowCwdRef.value,
    });
  } finally {
    inFlight.value = false;
  }
}

function startInheritedChatFromSharedFileForApp(payload, inheritedChatPayload, pageRef) {
  if (!payload || typeof payload !== 'object') return;
  const path = (payload.sharedFilePath || '').toString().trim();
  if (!path) return;
  inheritedChatPayload.value = { sharedFilePath: path, ts: Date.now() };
  pageRef.value = 'chat';
}

async function openDagChildThreadForApp(threadId, threadStore, pageRef) {
  const id = (threadId || '').toString().trim();
  if (!id) return;
  await threadStore.saveActiveThread(id);
  pageRef.value = 'chat';
}

export function shouldRefreshChatPageOnEnter(/** @type {string} */ nextPage, /** @type {string | null | undefined} */ prevPage) {
  return nextPage === 'chat' && Boolean(prevPage) && prevPage !== nextPage;
}

export async function refreshChatPageData(/** @type {ChatRefreshThreadStore} */ threadStore) {
  if (typeof threadStore?.refreshSidebarState !== 'function') {
    return {
      refreshed: false,
      activeThreadId: '',
      requestedHistory: false,
    };
  }
  await threadStore.refreshSidebarState();
  const activeThreadId = (threadStore?.state?.activeThreadId || '').toString().trim();
  const activeCmdThreadId = (threadStore?.state?.activeCmdThreadId || '').toString().trim();
  const freshness = activeThreadId
    ? await ensureThreadSelectionFresh(threadStore, activeThreadId, { reason: 'page-enter' })
    : {
      requestedHistory: false,
      syncedThreadState: false,
      forcedHistoryReload: false,
    };
  // 同时刷新 cmd 模式的活跃 thread，避免切换卡片时只显示快照
  if (activeCmdThreadId && activeCmdThreadId !== activeThreadId) {
    ensureThreadSelectionFresh(threadStore, activeCmdThreadId, { reason: 'page-enter' }).catch(() => {});
  }
  const requestedHistory = freshness.requestedHistory;

  return {
    refreshed: true,
    activeThreadId,
    requestedHistory,
  };
}

export const AppRoot = {
  name: 'AppRoot',
  components: {
    SidebarNav,
    ProjectModal,
    UnifiedChatPage,
    DagsPage,
    SkillsPage,
    TasksPage,
    CommandsPage,
    SettingsPage,
    SystemPromptPage,
    MemoryCenterPage,
    SharedFilesPage,
  },
  setup() {
    const projectStore = useProjectStore();
    const threadStore = useThreadStore();

    const page = ref('chat');
    const isExiting = ref(false);
    const bootstrapError = ref('');
    // Phase 2: 跨页面「用此文件新建对话」传递通道
    const inheritedChatPayload = ref(/** @type {{ sharedFilePath?: string } | null} */ (null));
    const dagDesignerStarting = ref(false);
    function startInheritedChatFromSharedFile(payload) { startInheritedChatFromSharedFileForApp(payload, inheritedChatPayload, page); }
    async function openDagChildThread(threadId) { await openDagChildThreadForApp(threadId, threadStore, page); }
    async function startDagDesignerThread(context = {}) { await startDagDesignerThreadOnceForApp(context, page, projectStore, threadStore, windowCwd, dagDesignerStarting); }
    const tasksSubTab = ref('acks');
    const buildInfo = reactive({});
    const runtimeConfig = reactive({ cwd: '' });
    const queryWindowCwd = ref(readWindowCwdFromLocation());
    const dagNodeStatusEvents = ref([]);
    const recordDagNodeStatusEvent = createDagNodeStatusEventRecorder(dagNodeStatusEvents);

    const dashboard = reactive({
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
    const dashboardRequest = reactive({
      dags: { loading: false, error: '' },
    });
    const memoryCenter = reactive({
      loading: false,
      error: '',
      overview: {},
      private: { entries: [] },
      team: { entries: [] },
    });
    const sidebarBadges = createSidebarBadges(memoryCenter);

    let refreshTimer;
    let unsubscribeAgentEvent = () => { }, unsubscribeBridgeEvent = () => { }, unsubscribeAppWillQuit = () => { }, removeBeforeUnload = () => { }, removePageHide = () => { };
    let chatPageRefreshPromise;



    const tasksItems = computed(() => (tasksSubTab.value === 'acks' ? dashboard.taskAcks : dashboard.taskTraces));
    const tasksFields = computed(() => (tasksSubTab.value === 'acks' ? TASK_ACK_FIELDS : TASK_TRACE_FIELDS));
    const windowCwd = computed(() => {
      const queryCwd = (queryWindowCwd.value || '').toString().trim();
      const processCwd = (runtimeConfig.cwd || '').toString().trim();
      return queryCwd || processCwd;
    });
    const activeProjectCwd = computed(() => {
      const active = (projectStore.state?.active || '').toString().trim();
      if (!active || active === '.') return windowCwd.value;
      return active;
    });
    const currentCwdDisplay = computed(() => {
      if (!windowCwd.value && !activeProjectCwd.value) return '未选择项目目录';
      if (!windowCwd.value && activeProjectCwd.value) return `活动项目：${activeProjectCwd.value}`;
      if (activeProjectCwd.value && activeProjectCwd.value !== windowCwd.value) {
        return `当前窗口 CWD：${windowCwd.value}（活动项目：${activeProjectCwd.value}）`;
      }
      return `当前窗口 CWD：${windowCwd.value}`;
    });
    const threadScopeCwd = computed(() => activeProjectCwd.value || '');

    async function refreshBuildInfo() {
      const info = await getBuildInfo();
      Object.assign(buildInfo, info || {});
    }

    async function refreshRuntimeConfig() {
      await refreshRuntimeConfigState(runtimeConfig);
    }

    const runCommandCard = (/** @type {CommandCard} */ card) => runCommandCardForApp(card, threadStore, projectStore, page, windowCwd.value);


    async function refreshDashboardByPage(/** @type {AppPage} */ targetPage) {
      if (targetPage === 'chat' || targetPage === 'settings' || targetPage === 'memory-center') return;
      const request = targetPage === 'dags' ? dashboardRequest.dags : null;
      if (request) {
        request.loading = true;
        request.error = '';
      }
      const cwd = (threadScopeCwd.value || '').toString().trim();
      try {
        if (dashboardPageRequiresCwd(targetPage) && !cwd) {
          throw new Error(`dashboard ${targetPage} cwd is required`);
        }
        const res = await callAPI('ui/dashboard/get', cwd ? { page: targetPage, cwd } : { page: targetPage });
        applyDashboardPagePayload(dashboard, res);
      } catch (error) {
        if (request) request.error = dashboardPageErrorText(targetPage);
        throw error;
      } finally {
        if (request) request.loading = false;
      }
    }

    async function refreshMemoryCenter() {
      await refreshMemoryCenterState(memoryCenter, (threadScopeCwd.value || '').toString().trim());
    }

    async function refreshChatPageOnEnter() {
      if (chatPageRefreshPromise) return chatPageRefreshPromise;
      chatPageRefreshPromise = refreshChatPageData(threadStore);
      try {
        return await chatPageRefreshPromise;
      } finally {
        chatPageRefreshPromise = null;
      }
    }

    async function bootstrap() {
      bootstrapError.value = '';
      // Subscribe to events FIRST, before any await — ensures no events are missed
      // even if subsequent initialization steps hang or take long.
      // legacy agent-event channel removed to prevent duplicate event processing
      unsubscribeAgentEvent = () => {};
      unsubscribeBridgeEvent = onBridgeEvent(/** @param {BridgeEventEnvelope} evt */ (evt) => {
        threadStore.handleBridgeEvent(evt);
        const eventType = (evt?.type || evt?.params?.type || evt?.payload?.type || evt?.data?.type || '').toString().trim().toLowerCase();
        const method = (evt?.method || evt?.params?.method || evt?.payload?.method || evt?.data?.method || '').toString().trim().toLowerCase();
        const payload = evt?.payload || evt?.data || evt?.params || {};
        if (method === 'skills/changed' || eventType === 'skills/changed') {
          if (page.value === 'skills') {
            refreshDashboardByPage('skills').catch((error) => { console.warn('refresh page failed: skills', error); });
          }
        }
        routeDagBridgeEvent(method, eventType, payload, { page, refreshDashboardByPage, recordDagNodeStatusEvent });
      });
      unsubscribeAppWillQuit = onAppWillQuit(() => {
        isExiting.value = true;
      });

      // Initialization — runs after event subscriptions are active
      await Promise.all([refreshBuildInfo(), refreshRuntimeConfig()]);
      projectStore.setScopeCwd?.(windowCwd.value);
      if (windowCwd.value) {
        await projectStore.reloadProjects();
      }
      await applyWindowBootstrapSnapshot(await loadWindowBootstrapSnapshot(), projectStore, threadStore, page, windowCwd.value);

      if (typeof threadStore.setPreferenceScopeCwd === 'function') {
        threadStore.setPreferenceScopeCwd(threadScopeCwd.value);
      }

      await threadStore.refreshSidebarState();

      if (threadStore.state.activeThreadId) {
        await ensureBootstrapThreadSelectionFresh(threadStore, threadStore.state.activeThreadId, { reason: 'bootstrap' });
      }
      if (threadStore.state.activeCmdThreadId && threadStore.state.activeCmdThreadId !== threadStore.state.activeThreadId) {
        ensureBootstrapThreadSelectionFresh(threadStore, threadStore.state.activeCmdThreadId, { reason: 'page-enter' }).catch(() => {});
      }

      refreshMemoryCenter().catch((error) => {
        console.warn('refresh memory center badge failed', error);
      });

      refreshTimer = setInterval(() => {
        threadStore.refreshSidebarState();
      }, REFRESH_INTERVAL_MS);
    }

    watch(
      () => page.value,
      async (/** @type {AppPage} */ next, /** @type {AppPage | undefined} */ prev) => {
        if (shouldRefreshChatPageOnEnter(next, prev)) {
          try {
            await refreshChatPageOnEnter();
          } catch (error) {
            console.warn('refresh chat failed', error);
          }
          return;
        }
        if (next === 'chat') return;
        if (next === 'memory-center') {
          refreshMemoryCenter().catch((error) => {
            console.warn('refresh page failed: memory-center', error);
          });
          return;
        }
        refreshDashboardByPage(next).catch((error) => {
          console.warn(`refresh page failed: ${next}`, error);
        });
      },
      { immediate: true },
    );


    watch(
      () => threadScopeCwd.value,
      (/** @type {string} */ next, /** @type {string | undefined} */ prev) => {
        if (typeof threadStore.setPreferenceScopeCwd === 'function') {
          threadStore.setPreferenceScopeCwd(next || '');
        }
        if (!next || next === prev) return;
        threadStore.refreshSidebarState({ force: true }).catch((/** @type {unknown} */ error) => {
          console.warn('refresh threads after scope change failed', error);
        });
        refreshScopedPageAfterCwdChange(page.value, { refreshMemoryCenter, refreshDashboardByPage });
      },
      { immediate: true },
    );

    onMounted(() => {
      bootstrap().catch((error) => {
        bootstrapError.value = (error?.message || String(error) || 'bootstrap failed').toString();
        console.error('bootstrap failed:', error);
      });
      const handleBeforeUnload = () => {
        isExiting.value = true;
      };
      const handlePageHide = () => {
        isExiting.value = true;
      };
      window.addEventListener('beforeunload', handleBeforeUnload);
      window.addEventListener('pagehide', handlePageHide);
      removeBeforeUnload = () => window.removeEventListener('beforeunload', handleBeforeUnload);
      removePageHide = () => window.removeEventListener('pagehide', handlePageHide);
    });

    onBeforeUnmount(() => {
      removeBeforeUnload();
      removePageHide();
      unsubscribeAgentEvent();
      unsubscribeBridgeEvent();
      unsubscribeAppWillQuit();
      if (refreshTimer) clearInterval(refreshTimer);
    });

    return {
      NAV_ITEMS,
      SHARED_FILES_TIPS,
      page,
      isExiting,
      bootstrapError,
      tasksSubTab,
      projectStore,
      threadStore,
      buildInfo,
      dashboard,
      dashboardRequest,
      dagNodeStatusEvents,
      memoryCenter,
      agentsFields: AGENTS_FIELDS,
      taskAckFields: TASK_ACK_FIELDS,
      taskTraceFields: TASK_TRACE_FIELDS,
      commandFields: COMMAND_FIELDS,
      memoryFields: MEMORY_FIELDS,
      inheritedChatPayload,
      startInheritedChatFromSharedFile,
      openDagChildThread,
      startDagDesignerThread,
      clearInheritedChatPayload: () => { inheritedChatPayload.value = null; },
      tasksItems,
      tasksFields,
      windowCwd,
      activeProjectCwd,
      threadScopeCwd,
      currentCwdDisplay,
      sidebarBadges,
      refreshBuildInfo,
      refreshDashboardByPage,
      refreshMemoryCenter,
      runCommandCard,
    };
  },
  template: `
    <div class="app-shell" data-testid="app-shell">
      <SidebarNav :items="NAV_ITEMS" :page="page" :badges="sidebarBadges" @change="page = $event" />

      <main id="content" :data-testid="'page-' + page">

        <div v-if="bootstrapError" class="app-fatal" data-testid="bootstrap-error">
          {{ bootstrapError }}
        </div>

        <UnifiedChatPage
          v-else-if="page === 'chat'"
          mode="chat"
          :project-store="projectStore"
          :thread-store="threadStore"
          :window-cwd="windowCwd"
          :cwd-display="currentCwdDisplay"
          :inherited-chat-payload="inheritedChatPayload"
          @clear-inherited-chat="clearInheritedChatPayload"
        />

        <SystemPromptPage
          v-else-if="page === 'prompts'"
          :project-store="projectStore"
          :window-cwd="windowCwd"
        />

        <DagsPage
          v-else-if="page === 'dags'"
          :items="dashboard.dags"
          :loading="dashboardRequest.dags.loading"
          :error="dashboardRequest.dags.error"
          :status-events="dagNodeStatusEvents"
          @open-chat="openDagChildThread"
          @design-flow="startDagDesignerThread"
          @refresh-dags="refreshDashboardByPage('dags')"
        />

        <TasksPage
          v-else-if="page === 'tasks'"
          :tasks-sub-tab="tasksSubTab"
          :items="tasksItems"
          :fields="tasksFields"
          @update:tasks-sub-tab="tasksSubTab = $event"
        />

        <SkillsPage
          v-else-if="page === 'skills'"
          :skills="dashboard.skills"
          :cwd="threadScopeCwd"
          :project-store="projectStore"
          @refresh-skills="refreshDashboardByPage('skills')"
        />

        <CommandsPage
          v-else-if="page === 'commands'"
          :command-cards="dashboard.commandCards"
          :command-fields="commandFields"
          @run-command="runCommandCard"
        />

        <!-- KeepAlive: 让 MemoryCenterPage 在切走时不卸载，避免 LLM 整合等 long-running RPC 被 abort。 -->
        <KeepAlive>
          <MemoryCenterPage
            v-if="page === 'memory-center'"
            :model="memoryCenter"
            @refresh="refreshMemoryCenter"
            @open-shared-files="page = 'memory'"
            @vue:activated="refreshMemoryCenter"
          />
        </KeepAlive>

        <SharedFilesPage
          v-if="page === 'memory'"
          :files="dashboard.memory"
          :cwd="threadScopeCwd"
          :final-output-refs="dashboard.finalOutputRefs"
          :shared-file-retention="dashboard.sharedFileRetention"
          @open-memory-center="page = 'memory-center'"
          @refresh="refreshDashboardByPage('memory')"
          @start-inherited-chat="startInheritedChatFromSharedFile"
        />

        <SettingsPage
          v-if="page === 'settings'"
          :build-info="buildInfo"
          :project-store="projectStore"
          @refresh="refreshBuildInfo"
        />
      </main>

      <ProjectModal :store="projectStore" />
      <div class="app-exit-overlay" :class="{ active: isExiting }" aria-hidden="true">
        <div class="app-exit-overlay-inner">
          <img src="/vue-app/assets/exit-splash.png" alt="" class="app-exit-overlay-icon" />
          <div class="app-exit-overlay-text">正在退出…</div>
        </div>
      </div>
    </div>
  `,
};
