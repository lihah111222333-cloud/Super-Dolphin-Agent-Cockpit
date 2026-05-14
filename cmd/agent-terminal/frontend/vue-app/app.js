import { ref, reactive, computed, onMounted, onBeforeUnmount, watch } from '../lib/vue.esm-browser.prod.js';
import { callAPI, getBuildInfo, onBridgeEvent, onAppWillQuit } from './services/api.js';
import { usePendingCandidates } from './composables/usePendingCandidates.js';
import { SidebarNav } from './components/SidebarNav.js';
import { ProjectModal } from './components/ProjectModal.js';
import { DagDetailModal } from './components/DagDetailModal.js';
import { DagsPage } from './pages/DagsPage.js';
import { useDagDetail } from './composables/useDagDetail.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.js';
import { ensureThreadSelectionFresh, requestHistoryLoad } from './utils/thread-page-utils.js';
import { DataPage } from './pages/DataPage.js';
import { SkillsPage } from './pages/SkillsPage.js';
import { TasksPage } from './pages/TasksPage.js';
import { CommandsPage } from './pages/CommandsPage.js';
import { SettingsPage } from './pages/SettingsPage.ts';
import { SystemPromptPage } from './pages/SystemPromptPage.js';
import { MemoryCenterPage } from './pages/MemoryCenterPage.js';
import { SharedFilesPage } from './pages/SharedFilesPage.js';
import { useProjectStore } from './stores/projects.js';
import { useThreadStore } from './stores/threads.js';

/**
 * @typedef {'chat' | 'agents' | 'dags' | 'tasks' | 'skills' | 'commands' | 'memory-center' | 'memory' | 'settings'} AppPage
 * @typedef {{ refreshSidebarState?: () => Promise<void>, state?: { activeThreadId?: string, activeCmdThreadId?: string } }} ChatRefreshThreadStore
 * @typedef {{ command_template?: string }} CommandCard

 * @typedef {{ type?: string, method?: string, params?: { type?: string, method?: string }, payload?: { type?: string, method?: string }, data?: { type?: string, method?: string } }} BridgeEventEnvelope
 */
const REFRESH_INTERVAL_MS = 10000;

const NAV_ITEMS = Object.freeze([
  { key: 'chat', icon: '💬', label: 'Chat' },
  { key: 'prompts', icon: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/><polyline points="14 2 14 8 20 8"/><path d="M12 18v-6"/><path d="M9 15l3-3 3 3"/></svg>', label: '提示词' },
  { key: 'dags', icon: 'D', label: 'DAG' },
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

const DAGS_FIELDS = Object.freeze([
  { key: 'dag_key', label: 'DAG' },
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

const EMPTY_MEMORY_CENTER = Object.freeze({
  overview: {},
  private: { entries: [] },
  team: { entries: [] },
});

function countMemorySimilarGroups(memoryCenter) {
  const groups = memoryCenter?.overview?.health?.similarGroups;
  return Array.isArray(groups) ? groups.length : 0;
}

function createSidebarBadges(skillSidebarBadges, memoryCenter) {
  return computed(() => {
    const badges = { ...(skillSidebarBadges.value || {}) };
    const similarCount = countMemorySimilarGroups(memoryCenter);
    if (similarCount > 0) badges['memory-center'] = similarCount;
    return badges;
  });
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

function applyMemoryCenterSnapshot(state, snapshot) {
  state.overview = snapshot?.overview || {};
  state.private = snapshot?.private || { entries: [] };
  state.team = snapshot?.team || { entries: [] };
}

export function routeDagBridgeEvent(method, eventType, payload, deps) {
  const key = (method || eventType || '').toString().trim().toLowerCase();
  if (key !== 'task/node/statuschanged') return;
  deps?.dagDetail?.handleStatusEvent?.(payload || {});
  if (deps?.page?.value === 'dags') {
    deps.refreshDashboardByPage?.('dags').catch((err) => {
      console.warn('refresh dag list after node event failed', err);
    });
  }
}

export function openChatFromDagNode({ turnId, assignedTo }, deps) {
  const trimmed = (turnId || '').toString().trim();
  if (trimmed && typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    navigator.clipboard.writeText(trimmed).catch(() => {});
  }
  if (deps?.page) deps.page.value = 'chat';
  return { turnId: trimmed, assignedTo: (assignedTo || '').toString().trim() };
}

async function refreshRuntimeConfigState(runtimeConfig) {
  try {
    const info = await callAPI('config/read', {});
    runtimeConfig.cwd = (info?.cwd || '').toString().trim();
  } catch (error) {
    console.warn('refresh runtime config failed', error);
    runtimeConfig.cwd = '';
  }
}

async function refreshMemoryCenterState(memoryCenter, cwd) {
  memoryCenter.loading = true;
  memoryCenter.error = '';
  try {
    const snapshot = await callAPI('ui/memory/get', cwd ? { cwd } : {});
    applyMemoryCenterSnapshot(memoryCenter, snapshot && typeof snapshot === 'object' ? snapshot : EMPTY_MEMORY_CENTER);
  } catch (error) {
    console.warn('refresh memory center failed', error);
    resetMemoryCenterState(memoryCenter);
    memoryCenter.error = (error?.message || String(error) || '加载失败').toString();
  } finally {
    memoryCenter.loading = false;
  }
}

async function loadWindowBootstrapSnapshot() {
  try {
    const result = await callAPI('ui/windowBootstrap/get', {});
    return result?.snapshot && typeof result.snapshot === 'object' ? result.snapshot : {};
  } catch (error) {
    console.warn('load window bootstrap snapshot failed', error);
    return {};
  }
}

async function applyWindowBootstrapSnapshot(snapshot, projectStore, threadStore, pageRef) {
  const payload = snapshot && typeof snapshot === 'object' ? snapshot : {};
  const cwd = (payload.cwd || '').toString().trim();
  if (cwd) {
    await projectStore.addProject(cwd);
    await projectStore.setActive(cwd);
  }
  const nextPage = (payload.page || '').toString().trim();
  if (nextPage) {
    pageRef.value = nextPage;
  }
  const taskStart = payload.taskStart && typeof payload.taskStart === 'object' ? payload.taskStart : null;
  if (taskStart) {
    const startOptions = {
      focusMode: taskStart.focusMode === 'cmd' ? 'cmd' : 'chat',
      config: taskStart.config && typeof taskStart.config === 'object' ? taskStart.config : {},
    };
    const startName = typeof taskStart.name === 'string' ? taskStart.name.trim() : '';
    const baseInstructions = typeof taskStart.baseInstructions === 'string' ? taskStart.baseInstructions.trim() : '';
    if (startName) startOptions.name = startName;
    if (baseInstructions) startOptions.baseInstructions = baseInstructions;
    await threadStore.startThread(cwd || projectStore.state.active || '.', startOptions);
  }

}

async function ensureAppActiveThread(threadStore, projectStore) {
  let threadId = threadStore.state.activeThreadId || '';
  if (threadId) return threadId;

  threadId = await threadStore.startThread(projectStore.state.active || '.');
  if (!threadId) return '';

  await requestHistoryLoad(threadStore, threadId);
  return threadId;
}

async function runCommandCardForApp(card, threadStore, projectStore, pageRef) {
  const command = (card?.command_template || '').toString().trim();
  if (!command) return;
  const threadId = await ensureAppActiveThread(threadStore, projectStore);
  if (!threadId) return;

  await threadStore.sendMessage(threadId, `请执行以下命令并反馈结果：\n${command}`);
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
    DataPage,
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
    // Phase 2: 跨页面「用此文件新建对话」传递通道
    const inheritedChatPayload = ref(/** @type {{ sharedFilePath?: string } | null} */ (null));
    function startInheritedChatFromSharedFile(payload) {
      if (!payload || typeof payload !== 'object') return;
      const path = (payload.sharedFilePath || '').toString().trim();
      if (!path) return;
      // 每次创建新引用，watch 必触发
      inheritedChatPayload.value = { sharedFilePath: path, ts: Date.now() };
      page.value = 'chat';
    }
    const tasksSubTab = ref('acks');
    const buildInfo = reactive({});
    const runtimeConfig = reactive({ cwd: '' });

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
    const { pendingCandidates, sidebarBadges: skillSidebarBadges, refreshPendingCandidates } = usePendingCandidates(
      () => (threadScopeCwd.value || '').toString().trim(),
    );

    const memoryCenter = reactive({
      loading: false,
      error: '',
      overview: {},
      private: { entries: [] },
      team: { entries: [] },
    });
    const sidebarBadges = createSidebarBadges(skillSidebarBadges, memoryCenter);

    let refreshTimer;
    let unsubscribeAgentEvent = () => { };
    let unsubscribeBridgeEvent = () => { };
    let unsubscribeAppWillQuit = () => { };
    let removeBeforeUnload = () => { };
    let removePageHide = () => { };
    let chatPageRefreshPromise;



    const tasksItems = computed(() => (tasksSubTab.value === 'acks' ? dashboard.taskAcks : dashboard.taskTraces));
    const tasksFields = computed(() => (tasksSubTab.value === 'acks' ? TASK_ACK_FIELDS : TASK_TRACE_FIELDS));
    const windowCwd = computed(() => {
      const processCwd = (runtimeConfig.cwd || '').toString().trim();
      return processCwd || '.';
    });
    const activeProjectCwd = computed(() => {
      const active = (projectStore.state?.active || '').toString().trim();
      if (!active || active === '.') return '';
      return active;
    });
    const currentCwdDisplay = computed(() => {
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

    const runCommandCard = (/** @type {CommandCard} */ card) => runCommandCardForApp(card, threadStore, projectStore, page);


    async function refreshDashboardByPage(/** @type {AppPage} */ targetPage) {
      if (targetPage === 'chat' || targetPage === 'settings' || targetPage === 'memory-center') return;
      const cwd = (threadScopeCwd.value || '').toString().trim();
      const res = await callAPI('ui/dashboard/get', cwd ? { page: targetPage, cwd } : { page: targetPage });
      dashboard.agents = Array.isArray(res?.agents) ? res.agents : [];
      dashboard.dags = Array.isArray(res?.dags) ? res.dags : [];
      dashboard.taskAcks = Array.isArray(res?.taskAcks) ? res.taskAcks : [];
      dashboard.taskTraces = Array.isArray(res?.taskTraces) ? res.taskTraces : [];
      dashboard.skills = Array.isArray(res?.skills) ? res.skills : [];
      dashboard.commandCards = Array.isArray(res?.commandCards) ? res.commandCards : [];
      dashboard.memory = Array.isArray(res?.memory) ? res.memory : [];
      dashboard.finalOutputRefs = Array.isArray(res?.finalOutputRefs) ? res.finalOutputRefs : [];
      dashboard.sharedFileRetention = (res?.sharedFileRetention && typeof res.sharedFileRetention === 'object')
        ? res.sharedFileRetention
        : { items: [], protectedCount: 0, cleanupCandidateCount: 0 };
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
          refreshPendingCandidates().catch(() => {});
          if (page.value === 'skills') {
            refreshDashboardByPage('skills').catch((error) => { console.warn('refresh page failed: skills', error); });
          }
        }
        routeDagBridgeEvent(method, eventType, payload, { page, refreshDashboardByPage, dagDetail });
      });
      unsubscribeAppWillQuit = onAppWillQuit(() => {
        isExiting.value = true;
      });

      // Initialization — runs after event subscriptions are active
      await Promise.all([
        refreshBuildInfo(),
        refreshRuntimeConfig(),
        projectStore.reloadProjects(),
      ]);
      await applyWindowBootstrapSnapshot(await loadWindowBootstrapSnapshot(), projectStore, threadStore, page);

      if (typeof threadStore.setPreferenceScopeCwd === 'function') {
        threadStore.setPreferenceScopeCwd(threadScopeCwd.value);
      }

      await threadStore.refreshSidebarState();

      if (threadStore.state.activeThreadId) {
        await ensureThreadSelectionFresh(threadStore, threadStore.state.activeThreadId, { reason: 'bootstrap' });
      }
      if (threadStore.state.activeCmdThreadId && threadStore.state.activeCmdThreadId !== threadStore.state.activeThreadId) {
        ensureThreadSelectionFresh(threadStore, threadStore.state.activeCmdThreadId, { reason: 'page-enter' }).catch(() => {});
      }

      refreshPendingCandidates().catch(() => {});
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
        if (next === 'skills') refreshPendingCandidates().catch(() => {});
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
        threadStore.refreshSidebarState().catch((/** @type {unknown} */ error) => {
          console.warn('refresh threads after scope change failed', error);
        });
        if (page.value === 'memory-center') {
          refreshMemoryCenter().catch((error) => {
            console.warn('refresh memory center after scope change failed', error);
          });
        } else if (page.value === 'memory') {
          refreshDashboardByPage('memory').catch((error) => {
            console.warn('refresh shared files after scope change failed', error);
          });
        }
      },
      { immediate: true },
    );

    onMounted(() => {
      bootstrap().catch((error) => {
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

    const dagDetail = useDagDetail();
    const openDagChat = (/** @type {any} */ ev) => openChatFromDagNode(ev, { page });
    return {
      NAV_ITEMS,
      SHARED_FILES_TIPS,
      page,
      isExiting,
      tasksSubTab,
      projectStore,
      threadStore,
      buildInfo,
      dashboard,
      memoryCenter,
      agentsFields: AGENTS_FIELDS,
      dagsFields: DAGS_FIELDS,
      taskAckFields: TASK_ACK_FIELDS,
      taskTraceFields: TASK_TRACE_FIELDS,
      commandFields: COMMAND_FIELDS,
      memoryFields: MEMORY_FIELDS,
      inheritedChatPayload,
      startInheritedChatFromSharedFile,
      clearInheritedChatPayload: () => { inheritedChatPayload.value = null; },
      tasksItems,
      tasksFields,
      windowCwd,
      activeProjectCwd,
      currentCwdDisplay,
      sidebarBadges,
      pendingCandidates,
      refreshPendingCandidates,
      refreshBuildInfo,
      refreshDashboardByPage,
      refreshMemoryCenter,
      runCommandCard,
      dagDetail,
      openDagChat,
    };
  },
  template: `
    <div class="app-shell" data-testid="app-shell">
      <SidebarNav :items="NAV_ITEMS" :page="page" :badges="sidebarBadges" @change="page = $event" />

      <main id="content" :data-testid="'page-' + page">

        <UnifiedChatPage
          v-if="page === 'chat'"
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
          :fields="dagsFields"
          @select="dagDetail.open"
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
          :project-store="projectStore"
          :pending-candidates="pendingCandidates"
          @refresh-skills="refreshDashboardByPage('skills')"
          @refresh-candidates="refreshPendingCandidates"
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
      <DagDetailModal
        :show="dagDetail.state.show"
        :loading="dagDetail.state.loading"
        :error="dagDetail.state.error"
        :dag="dagDetail.state.dag"
        :nodes="dagDetail.state.nodes"
        :runs="dagDetail.state.runs"
        :run="dagDetail.state.run"
        :final-output="dagDetail.state.finalOutput"
        :saving-node-key="dagDetail.state.savingNodeKey"
        :save-error="dagDetail.state.saveError"
        @close="dagDetail.close"
        @update-node-status="(ev) => dagDetail.updateNodeStatus(ev.nodeKey, ev.status)"
        @open-chat="openDagChat"
      />
      <div class="app-exit-overlay" :class="{ active: isExiting }" aria-hidden="true">
        <div class="app-exit-overlay-inner">
          <img src="/vue-app/assets/exit-splash.png" alt="" class="app-exit-overlay-icon" />
          <div class="app-exit-overlay-text">正在退出…</div>
        </div>
      </div>
    </div>
  `,
};
