/**
 * SystemPromptPage — System 提示词管理页面
 *
 * 布局：
 *   ┌──────────────────────────────────────────┐
 *   │ Header: System 提示词管理    [CWD badge] │
 *   ├──────────────────────────────────────────┤
 *   │ Tabs: [主 Agent] [子 Agent]              │
 *   ├──────────────────────────────────────────┤
 *   │ Card Grid:                               │
 *   │  [+ 新建] [Card1] [Card2] [Card3] ...   │
 *   └──────────────────────────────────────────┘
 *
 * 点击卡片 → 模态编辑器（编辑 / 查看 prompt）
 * 类似 SkillsPage 的 CRUD 管理模式。
 */
// @ts-nocheck
import {
  ref,
  reactive,
  computed,
  onMounted,
  watch,
} from '../../lib/vue.esm-browser.prod.js';

import { callAPI, copyTextToClipboard } from '../services/api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';
import { SectionsEditor } from './SectionsEditor.js';

// ── Helpers (outside setup to keep size-guard happy) ──────────

// Preference key used by SystemPromptPage to record which prompt_template the
// user wants the next blank conversation to use. Read by stores/thread-actions-
// helpers.js startThread() and forwarded to the backend as `prompt_key`, which
// the router consumes via internal/module/thread/router_resolve.go.
export const PREF_KEY_ACTIVE_PROMPT = 'settings.activePromptKey';
// Plan B opt-in. When true, startThread forwards use_classifier=true so the
// backend router runs the prompt classifier (claude -p subprocess) on the
// first-turn user input and auto-picks a prompt_template from the full
// library. Ignored when PREF_KEY_ACTIVE_PROMPT is set — explicit pin wins.
export const PREF_KEY_CLASSIFIER_ENABLED = 'settings.classifierEnabled';

function resolveProjectCwd(projectStore) {
  return (projectStore?.state?.active || '').toString().trim();
}

function resolveReadonlyFallbackCwd(props) {
  return (
    props?.threadStore?.state?.cwd
    || props?.projectStore?.state?.active
    || props?.projectStore?.state?.cwd
    || props?.windowCwd
    || ''
  ).toString().trim();
}

// prefGet / prefSet consolidate the 3 preferences (active launch prompt,
// classifier toggle, candidate pool) behind a single load/save pair. Each
// caller owns its own `key`, default value, and decode step; the wire-level
// RPC + log-on-failure fallback is shared.
async function prefGet(cwd, key, scope, fallback = null) {
  if (!cwd) return fallback;
  try {
    return await callAPI('ui/preferences/get', { key, cwd });
  } catch (error) {
    logDebug('system-prompt', scope + '.load.failed', { error });
    return null;
  }
}

async function prefSet(cwd, key, value) {
  await callAPI('ui/preferences/set', { key, value, cwd });
}

// withCwd attaches the current project cwd to an RPC payload, no-op when the
// scope is unresolved so the caller's payload still works against the global
// default namespace.
function withCwd(cwd, payload) {
  return cwd ? { ...payload, cwd } : payload;
}

function generateId() {
  return Date.now().toString(36) + '-' + Math.random().toString(36).slice(2, 7);
}

function truncate(text, max = 80) {
  if (!text) return '暂无内容';
  return text.length > max ? text.slice(0, max) + '…' : text;
}

function countStats(text) {
  if (!text) return { lines: 0, chars: 0 };
  return { lines: text.split('\n').length, chars: text.length };
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

function normalizeFallbackErrorToken(value) {
  return (value == null ? '' : String(value)).trim().toLowerCase().replace(/[\s-]+/g, '_');
}

export function isReadonlyFallbackListError(error) {
  if (!error || typeof error !== 'object') return false;
  const status = Number(error.status ?? error.statusCode);
  if (status === 404) return true;
  if (error.code === -32601 || Number(error.code) === -32601) return true;
  const normalizedName = normalizeFallbackErrorToken(error.name);
  if (normalizedName === 'method_not_found' || normalizedName === 'notfounderror') return true;
  const normalizedCode = normalizeFallbackErrorToken(error.code);
  return normalizedCode === 'method_not_found';
}

/** Normalize a prompt item from API response. */
function normalizePromptItem(raw, agentType) {
  return {
    id: (raw?.id || raw?.prompt_key || generateId()).toString(),
    name: (raw?.name || raw?.title || raw?.prompt_key || '').toString(),
    content: (raw?.content || raw?.prompt_text || raw?.hint || '').toString(),
    description: (raw?.description || '').toString(),
    agentType: (raw?.agentType || raw?.agent_key || agentType || 'main').toString(),
    isDefault: Boolean(raw?.isDefault),
    createdAt: (raw?.createdAt || raw?.created_at || '').toString(),
    // match_when 透传 —— 编辑弹窗重新打开时需要还原在表单里。null / 缺失
    // 都保留为 null，代表「该模板不参与自动路由」，serializeMatchWhenForEditor 会处理。
    match_when: raw?.match_when ?? null,
    priority: Number.isFinite(Number(raw?.priority)) ? Number(raw.priority) : 0,
  };
}

function normalizePromptList(items) {
  return Array.isArray(items)
    ? items.map(item => normalizePromptItem(item))
    : [];
}

function normalizeFallbackPromptList(items) {
  if (!Array.isArray(items)) return null;
  const normalized = items
    .map(item => normalizePromptItem(item))
    .filter(item => item.name || item.content);
  if (items.length > 0 && normalized.length === 0) return null;
  return normalized;
}

// 'all' 返回全部行；其他 activeTab 等值匹配行的 agentType (介面 agent_key)。
function filterPromptCards(cards, activeTab) {
  return activeTab === 'all' ? cards : cards.filter(c => c.agentType === activeTab);
}

// serializeMatchWhenForEditor 把后端返回的 match_when（undefined / null / 对象 /
// 字符串）统一变成给 textarea 展示的字符串。undefined / null → 空串（opt-out）；
// 对象 → 两空格缩进的 JSON；字符串 → 原样（兼容旧 payload 偶尔回传字符串的场景）。
function serializeMatchWhenForEditor(value) {
  if (value === undefined || value === null) return '';
  if (typeof value === 'string') return value;
  try { return JSON.stringify(value, null, 2); } catch { return ''; }
}

// applyMatchWhenToPayload 把用户编辑的 match_when 文本塞进 prompts/write payload。
// 返回 null = 成功（空串代表 opt-out，直接不塞字段）；返回字符串 = 人类可读的
// 错误消息，调用方应展示给用户并中止保存。
function applyMatchWhenToPayload(payload, rawText) {
  const text = (rawText || '').trim();
  if (!text) return null;
  try {
    payload.match_when = JSON.parse(text);
    return null;
  } catch (err) {
    return `自动匹配条件不是合法 JSON：${toErrorMessage(err)}`;
  }
}

// probeTemplateSectionCount 查某个模板的 sections 数量，给基础 tab 的
// 「这里改不生效」提示当依据。失败一律当 0 处理，不阻断用户正常编辑。
async function probeTemplateSectionCount(cwd, promptId) {
  const id = (promptId || '').toString().trim();
  if (!id) return 0;
  try {
    const res = await callAPI('prompt_sections/list', withCwd(cwd, { prompt_id: id }));
    return Array.isArray(res?.sections) ? res.sections.length : 0;
  } catch {
    return 0;
  }
}

function createClassifierActions(deps) {
  const { getCwd, classifierEnabled, setNotice } = deps;
  async function loadClassifierEnabled() {
    const raw = await prefGet(getCwd(), PREF_KEY_CLASSIFIER_ENABLED, 'classifier', false);
    if (raw === null) return classifierEnabled.value;
    classifierEnabled.value = raw === true || raw === 'true';
    return classifierEnabled.value;
  }
  async function toggleClassifier(next) {
    const cwd = getCwd();
    if (!cwd) { setNotice('error', '当前作用域未确定，无法保存开关'); return; }
    const desired = Boolean(next);
    try {
      await prefSet(cwd, PREF_KEY_CLASSIFIER_ENABLED, desired);
      classifierEnabled.value = desired;
      setNotice('info', desired ? '已开启智能启动分类；新对话会以第一条消息自动选提示词' : '已关闭智能启动分类');
    } catch (error) {
      logWarn('system-prompt', 'classifier.persist.failed', { error });
      setNotice('error', `切换智能启动失败：${toErrorMessage(error)}`);
    }
  }
  return { loadClassifierEnabled, toggleClassifier };
}

function refreshPromptsInBackground(loaders) {
  for (const loader of loaders) {
    if (typeof loader === 'function') loader().catch(() => {});
  }
}

function createLaunchPromptActions(deps) {
  const { getCwd, fallbackMode, activePromptId, activatingId, setNotice, setReadonlyActionNotice } = deps;
  async function loadActivePromptId() {
    const raw = await prefGet(getCwd(), PREF_KEY_ACTIVE_PROMPT, 'active', '');
    if (raw === null) return activePromptId.value;
    activePromptId.value = (typeof raw === 'string' ? raw : '').trim();
    return activePromptId.value;
  }
  async function applyActivePromptId(nextId, successMessage) {
    const cwd = getCwd();
    if (!cwd) { setNotice('error', '当前作用域未确定，无法记录启动提示词'); return; }
    try {
      await prefSet(cwd, PREF_KEY_ACTIVE_PROMPT, (nextId || '').toString());
      activePromptId.value = (nextId || '').toString();
      setNotice('info', successMessage);
    } catch (error) {
      logWarn('system-prompt', 'active.persist.failed', { error });
      setNotice('error', `设置启动提示词失败：${toErrorMessage(error)}`);
    }
  }
  async function setLaunchPrompt(item) {
    if (fallbackMode.value) { setReadonlyActionNotice('设为启动'); return; }
    const id = (item?.id || '').toString();
    if (!id || activatingId.value) return;
    activatingId.value = id;
    try { await applyActivePromptId(id, `已设为启动提示词：${item?.name || id}`); }
    finally { activatingId.value = ''; }
  }
  async function clearLaunchPrompt() {
    if (fallbackMode.value) { setReadonlyActionNotice('取消启动'); return; }
    if (activatingId.value) return;
    activatingId.value = 'clear';
    try { await applyActivePromptId('', '已取消启动提示词，新对话将使用默认路由'); }
    finally { activatingId.value = ''; }
  }
  return { loadActivePromptId, setLaunchPrompt, clearLaunchPrompt };
}

export const SystemPromptPage = {
  name: 'SystemPromptPage',
  components: { SectionsEditor },
  props: {
    projectStore: { type: Object, default: null },
    threadStore: { type: Object, default: null },
    windowCwd: { type: String, default: '' },
  },
  setup(props) {
    // ── State ────────────────────────────────────────
    const activeTab = ref('main'); // 'main' | 'sub' | 'all'
    const currentScopeCwd = ref('');
    const promptCards = ref([]); // all prompt cards
    const loading = ref(false);
    const notice = reactive({ level: 'info', message: '' });
    const fallbackMode = ref(false);
    const readonlyReason = ref('');
    const fallbackSource = ref('');

    // Editor state
    const editorOpen = ref(false);
    const editorMode = ref('edit'); // 'edit' | 'create'
    const saving = ref(false);
    const deletingId = ref('');
    // Active launch prompt: the row whose PromptText will be injected as
    // BaseInstructions on the next thread/start. Persisted via ui/preferences
    // under PREF_KEY_ACTIVE_PROMPT, scoped to project cwd so different repos
    // can pin different prompts independently.
    const activePromptId = ref('');
    const activatingId = ref('');
    // Plan B opt-in: when true, startThread forwards use_classifier=true so
    // the backend router auto-picks a prompt_template from the full library
    // via claude -p. Explicit activePromptId still wins backend-side.
    const classifierEnabled = ref(false);
    const editingHasSections = ref(false); // sections>0 → 基础 tab 内容被 router 覆盖，锁住
    // matchWhen/priority 供路由的 match_when 自动路由挡位；matchWhen 空串 → opt-out。
    const form = reactive({
      id: '', name: '', content: '', description: '',
      // 从 'all' tab 编辑时回灰原 agent_key；新建留空 → savePrompt 兑底到 activeTab。
      agentKey: '',
      matchWhen: '', priority: 0,
    });

    // ── Computed ─────────────────────────────────────
    const filteredCards = computed(() => filterPromptCards(promptCards.value, activeTab.value));
    const cwdDisplay = computed(() =>
      currentScopeCwd.value || props.windowCwd || '未知'
    );
    // currentProjectCwd 是发给后端 RPC / 子组件的原始路径（空也 OK）；
    // cwdDisplay 含中文 fallback '未知'，专供 UI 显示用，不能当参数发出去。
    const currentProjectCwd = computed(() => resolveProjectCwd(props.projectStore));
    // Any state where the editor should be read-only matches exactly
    // `fallbackMode.value`.
    // 在 'all' tab 下创建会丢失 agent_key 归属（不知道应该存到 main 还是别的），
    // 所以只让主/子 tab 创建。全部 tab 仅用于查看/编辑/删除/设为启动。
    const createDisabled = computed(() => fallbackMode.value || activeTab.value === 'all');
    const saveDisabled = computed(() => fallbackMode.value || saving.value);
    const deleteDisabled = computed(() => fallbackMode.value || Boolean(deletingId.value));
    const activateDisabled = computed(() => fallbackMode.value || Boolean(activatingId.value));
    const readonlyBannerMessage = computed(() => {
      if (!fallbackMode.value) return '';
      const dataSourceTip = fallbackSource.value === 'dashboard/prompts'
        ? '当前列表来自 dashboard/prompts 只读旁路。'
        : '当前保留已有列表或空态。';
      const reasonTip = readonlyReason.value
        ? `原因：${readonlyReason.value}。`
        : '';
      return `prompts/list 暂不可用，页面已切换为只读模式；新建/保存/删除已禁用，后端恢复后会自动恢复。${dataSourceTip}${reasonTip}`;
    });

    // ── Helpers ─────────────────────────────────────
    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
    }

    function getCwd() {
      return resolveProjectCwd(props.projectStore);
    }

    function setReadonlyActionNotice(action) {
      setNotice('info', `当前为只读降级，暂不支持${action}`);
    }

    function enterReadonlyFallback(reason) {
      const nextReason = (reason || 'prompts/list not found').trim();
      const wasFallback = fallbackMode.value;
      fallbackMode.value = true;
      readonlyReason.value = nextReason;
      fallbackSource.value = 'prompts/list';
      const fields = { reason: nextReason, method: 'prompts/list' };
      if (wasFallback) {
        logInfo('system-prompt', 'fallback.retry', fields);
        return;
      }
      logWarn('system-prompt', 'fallback.enter', fields);
    }

    function clearReadonlyFallback(recoveredBy = 'prompts/list') {
      const hadFallback = fallbackMode.value || readonlyReason.value || fallbackSource.value;
      if (!hadFallback) return;
      fallbackMode.value = false;
      readonlyReason.value = '';
      fallbackSource.value = '';
      logInfo('system-prompt', 'fallback.recovered', { source: recoveredBy });
    }

    async function hydrateReadonlyPrompts() {
      try {
        const res = await callAPI('dashboard/prompts', { cwd: resolveReadonlyFallbackCwd(props) });
        const nextCards = normalizeFallbackPromptList(res?.prompts);
        if (!nextCards) return false;
        promptCards.value = nextCards;
        fallbackSource.value = 'dashboard/prompts';
        return true;
      } catch {
        return false;
      }
    }

    function switchTab(tab) {
      if (activeTab.value === tab) return;
      activeTab.value = tab;
      setNotice('info', '');
      logDebug('system-prompt', 'tab.switch', { tab });
    }

    // ── API Actions ─────────────────────────────────
    async function loadPrompts() {
      loading.value = true;
      try {
        const res = await callAPI('prompts/list', withCwd(getCwd(), {}));
        promptCards.value = normalizePromptList(res?.prompts);
        clearReadonlyFallback('prompts/list');
        setNotice('info', '');
        return promptCards.value;
      } catch (error) {
        if (isReadonlyFallbackListError(error)) {
          enterReadonlyFallback(toErrorMessage(error));
          await hydrateReadonlyPrompts();
          setNotice('info', '');
          return promptCards.value;
        }
        logWarn('system-prompt', 'load.failed', { error });
        setNotice('error', `加载失败：${toErrorMessage(error)}`);
        throw error;
      } finally {
        loading.value = false;
      }
    }

    async function savePrompt() {
      if (fallbackMode.value) {
        setReadonlyActionNotice('保存');
        return;
      }
      if (saving.value) return;
      const name = (form.name || '').trim();
      if (!name) {
        setNotice('error', '请填写提示词名称');
        return;
      }
      saving.value = true;
      try {
        // Edit 保留原 agent_key；create 走 activeTab；'all' 创建被 createDisabled
        // 拦了，这里仍透到 'main' 避免后端拿到 'all' 这个垃圾值。
        const payload = {
          id: form.id || '',
          name,
          content: form.content || '',
          description: form.description || '',
          agentType: form.agentKey || (activeTab.value === 'all' ? 'main' : activeTab.value),
          priority: Number.isFinite(Number(form.priority)) ? Number(form.priority) : 0,
        };
        const matchWhenErr = applyMatchWhenToPayload(payload, form.matchWhen);
        if (matchWhenErr) { setNotice('error', matchWhenErr); saving.value = false; return; }
        await callAPI('prompts/write', withCwd(getCwd(), payload));
        await loadPrompts();
        editorOpen.value = false;
        setNotice('info', `提示词已保存：${name}`);
      } catch (error) {
        logWarn('system-prompt', 'save.failed', { error });
        setNotice('error', `保存失败：${toErrorMessage(error)}`);
      } finally {
        saving.value = false;
      }
    }

    async function deletePrompt(item) {
      if (fallbackMode.value) {
        setReadonlyActionNotice('删除');
        return;
      }
      const id = (item?.id || '').toString();
      if (!id || deletingId.value) return;
      deletingId.value = id;
      try {
        await callAPI('prompts/delete', withCwd(getCwd(), { id }));
        await loadPrompts();
        if (editorOpen.value && form.id === id) {
          editorOpen.value = false;
        }
        setNotice('info', `已删除：${item?.name || ''}`);
      } catch (error) {
        logWarn('system-prompt', 'delete.failed', { error });
        setNotice('error', `删除失败：${toErrorMessage(error)}`);
      } finally {
        deletingId.value = '';
      }
    }

    const { loadActivePromptId, setLaunchPrompt, clearLaunchPrompt } = createLaunchPromptActions({ getCwd, fallbackMode, activePromptId, activatingId, setNotice, setReadonlyActionNotice });
    const { loadClassifierEnabled, toggleClassifier } = createClassifierActions({ getCwd, classifierEnabled, setNotice });

    async function copyPromptContent(item) {
      const text = (item?.content || '').trim();
      if (!text) {
        setNotice('error', '暂无可复制内容');
        return;
      }
      try {
        const ok = await copyTextToClipboard(text);
        setNotice(ok ? 'info' : 'error', ok ? '已复制提示词内容' : '复制失败');
      } catch (error) {
        setNotice('error', `复制失败：${toErrorMessage(error)}`);
      }
    }

    // ── Editor ──────────────────────────────────────
    function openCreate() {
      if (fallbackMode.value) { setReadonlyActionNotice('新建'); return; }
      Object.assign(form, { id: '', name: '', content: '', description: '', agentKey: '', matchWhen: '', priority: 0 });
      editingHasSections.value = false;
      editorMode.value = 'create'; editorOpen.value = true; editorTab.value = 'basic';
      setNotice('info', '');
      logDebug('system-prompt', 'editor.create');
    }

    function openEdit(item) {
      Object.assign(form, {
        id: item.id || '', name: item.name || '', content: item.content || '',
        description: item.description || '', agentKey: (item.agentType || '').toString(),
        matchWhen: serializeMatchWhenForEditor(item.match_when),
        priority: Number.isFinite(Number(item.priority)) ? Number(item.priority) : 0,
      });
      editingHasSections.value = false;
      editorMode.value = 'edit'; editorOpen.value = true; editorTab.value = 'basic';
      setNotice('info', '');
      logDebug('system-prompt', 'editor.edit', { id: item.id });
      probeTemplateSectionCount(getCwd(), item.id).then(c => { if (form.id === (item.id || '')) editingHasSections.value = c > 0; });
    }

    function closeEditor() {
      editorOpen.value = false;
      setNotice('info', '');
    }

    // ── Advanced-debug tab state ─────────────────
    // editorTab 是编辑弹窗内部的 tab 开关；[basic] / [🔧 高级调试]。
    // sections 的实际 CRUD 已抽到 <SectionsEditor> 子组件，本组件只
    // 负责 tab 的显/隐；SectionsEditor 自己按 props.visible + props.promptId
    // watch 按需加载。
    const editorTab = ref('basic'); // 'basic' | 'advanced'
    function switchEditorTab(tab) {
      if (editorTab.value !== tab) editorTab.value = tab;
    }

    // ── CWD ─────────────────────────────────────────
    async function loadCurrentScopeCwd() {
      try {
        const cfg = await callAPI('config/read', {});
        currentScopeCwd.value = (cfg?.cwd || '').toString().trim();
      } catch {
        currentScopeCwd.value = '';
      }
    }

    const refreshPromptsSilently = () => refreshPromptsInBackground([loadPrompts, loadActivePromptId, loadClassifierEnabled]);

    // ── Lifecycle ───────────────────────────────────
    onMounted(() => {
      logInfo('system-prompt', 'page.mounted');
      loadCurrentScopeCwd();
      refreshPromptsSilently();
    });

    watch(
      () => resolveProjectCwd(props.projectStore),
      (next, prev) => {
        if (next === prev) return;
        loadCurrentScopeCwd();
        refreshPromptsSilently();
      },
    );

    return {
      activeTab, promptCards, filteredCards, loading,
      notice, fallbackMode, readonlyReason, fallbackSource,
      readonlyBannerMessage,
      editorOpen, editorMode, saving, deletingId,
      createDisabled, saveDisabled, deleteDisabled,
      activePromptId, activatingId, activateDisabled,
      classifierEnabled,
      form, editingHasSections, cwdDisplay, currentProjectCwd,
      switchTab, loadPrompts, savePrompt, deletePrompt,
      copyPromptContent, openCreate, openEdit, closeEditor,
      setLaunchPrompt, clearLaunchPrompt, loadActivePromptId,
      toggleClassifier, loadClassifierEnabled,
      truncate, countStats,
      // Advanced-debug tab toggle (actual CRUD lives in <sections-editor>)
      editorTab, switchEditorTab,
    };
  },
  template: `
    <section id="page-prompts" class="page active sp-page" data-testid="system-prompt-page">

      <!-- Header -->
      <div class="panel-header" data-testid="sp-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>System 提示词管理</h2></div>
        <span class="sp-cwd-badge" data-testid="sp-cwd-badge" :title="cwdDisplay">
          <svg class="sp-cwd-icon" viewBox="0 0 16 16" fill="none">
            <path d="M2 4.5A1.5 1.5 0 013.5 3h3.293a1 1 0 01.707.293L8.707 4.5H12.5A1.5 1.5 0 0114 6v5.5a1.5 1.5 0 01-1.5 1.5h-9A1.5 1.5 0 012 11.5v-7z" stroke="currentColor" stroke-width="1.2"/>
          </svg>
          <span class="sp-cwd-text">{{ cwdDisplay }}</span>
        </span>
      </div>

      <!-- 智能启动 · 候选池面板（把两个相关的路由开关放在一起） -->
      <div class="sp-routing-panel" data-testid="sp-routing-panel">
        <div class="sp-routing-row">
          <label class="sp-toggle-card" data-testid="sp-classifier-toggle"
            :class="{ 'is-active': classifierEnabled }"
            :title="'池为空时的兜底：新对话的第一条消息由 claude haiku 在全库自动选提示词（5-15 秒有较明显延迟）。候选池非空时会走多源注入，本开关不生效。显式「设为启动」始终优先。'">
            <input type="checkbox" :checked="classifierEnabled"
              @change="toggleClassifier($event.target.checked)"
              data-testid="sp-classifier-checkbox" />
            <div class="sp-toggle-body">
              <div class="sp-toggle-title">智能启动分类</div>
              <div class="sp-toggle-sub">新对话按首条消息语义自动选一条提示词</div>
            </div>
          </label>
</div>
      </div>

      <!-- Tabs -->
      <div class="sub-tabs" data-testid="sp-tabs">
        <button class="sub-tab" :class="{ active: activeTab === 'main' }" data-testid="sp-tab-main" @click="switchTab('main')">主 Agent</button>
        <button class="sub-tab" :class="{ active: activeTab === 'sub' }" data-testid="sp-tab-sub" @click="switchTab('sub')">子 Agent</button>
        <button class="sub-tab" :class="{ active: activeTab === 'all' }" data-testid="sp-tab-all" @click="switchTab('all')">全部</button>
      </div>

      <!-- Card list -->
      <div class="section-header">
        提示词列表
        <span v-if="filteredCards.length > 0" class="sp-count-tip">{{ filteredCards.length }} 条</span>
      </div>

      <div class="panel-body sp-list-panel" data-testid="sp-body">

        <!-- Toolbar -->
        <div class="sp-toolbar" data-testid="sp-toolbar">
          <button class="btn btn-secondary" data-testid="sp-create-btn" :disabled="createDisabled" @click="openCreate">+ 新建提示词</button>
          <button class="btn btn-ghost" data-testid="sp-refresh-btn" :disabled="loading" @click="loadPrompts">
            {{ loading ? '加载中...' : '刷新' }}
          </button>
        </div>

        <div
          v-if="fallbackMode"
          class="sp-notice is-warn sp-readonly-banner"
          data-testid="sp-readonly-banner"
        >
          {{ readonlyBannerMessage }}
        </div>

        <!-- Empty state -->
        <div v-if="!loading && filteredCards.length === 0" class="empty-state" data-testid="sp-empty">
          <div class="es-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" style="width:24px;height:24px">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
              <polyline points="14 2 14 8 20 8"/>
            </svg>
          </div>
          <h3>暂无{{ activeTab === 'main' ? '主 Agent' : (activeTab === 'sub' ? '子 Agent' : '') }}提示词</h3>
          <p>{{ fallbackMode ? '当前为只读降级；待后端恢复后会自动恢复。' : (activeTab === 'all' ? '库里还没有提示词，先去主/子 Agent tab 创建' : '点击"新建提示词"开始创建') }}</p>
        </div>

        <!-- Loading -->
        <div v-else-if="loading" class="sp-loading" data-testid="sp-loading">
          <div class="sp-spinner"></div>
          <span>加载中...</span>
        </div>

        <!-- Card Grid -->
        <div v-else class="sp-card-grid" data-testid="sp-card-grid">
          <article
            v-for="(item, idx) in filteredCards"
            :key="item.id"
            class="data-card-vue sp-card"
            :class="{ active: editorOpen && form.id === item.id }"
            :data-testid="'sp-card-' + idx"
          >
            <div class="sp-card-header">
              <div class="sp-card-heading">
                <div class="sp-card-title">{{ item.name || '未命名' }}</div>
                <span v-if="item.isDefault" class="sp-card-badge is-default">默认</span>
                <span v-if="activePromptId === item.id" class="sp-card-badge is-active" :data-testid="'sp-active-badge-' + idx">启动中</span>
                <span v-if="activeTab === 'all' && item.agentType" class="sp-card-badge" :data-testid="'sp-agentkey-badge-' + idx">{{ item.agentType }}</span>
              </div>
            </div>
            <div v-if="item.description" class="sp-card-desc">{{ item.description }}</div>
            <div class="sp-card-preview">{{ truncate(item.content) }}</div>
            <div class="sp-card-meta">{{ countStats(item.content).lines }} 行 · {{ countStats(item.content).chars }} 字符</div>
            <div class="sp-card-actions">
<button class="btn btn-secondary btn-xs" :data-testid="'sp-edit-btn-' + idx" @click="openEdit(item)">{{ fallbackMode ? '查看' : '编辑' }}</button>
              <button class="btn btn-ghost btn-xs" :data-testid="'sp-copy-btn-' + idx" @click="copyPromptContent(item)">复制</button>
              <button
                v-if="activePromptId === item.id"
                class="btn btn-ghost btn-xs"
                :data-testid="'sp-clear-launch-btn-' + idx"
                :disabled="activateDisabled"
                @click="clearLaunchPrompt"
              >{{ activatingId === 'clear' ? '处理中...' : '取消启动' }}</button>
              <button
                v-else
                class="btn btn-ghost btn-xs"
                :data-testid="'sp-set-launch-btn-' + idx"
                :disabled="activateDisabled"
                @click="setLaunchPrompt(item)"
              >{{ activatingId === item.id ? '处理中...' : '设为启动' }}</button>
              <button class="btn btn-ghost btn-xs btn-warning" :data-testid="'sp-delete-btn-' + idx" :disabled="deleteDisabled" @click="deletePrompt(item)">
                {{ deletingId === item.id ? '删除中...' : '删除' }}
              </button>
            </div>
          </article>
        </div>

        <!-- Notice -->
        <div v-if="notice.message && !editorOpen" class="sp-notice" :class="'is-' + notice.level" data-testid="sp-notice">
          {{ notice.message }}
        </div>
      </div>

      <!-- Modal Editor -->
      <div
        v-if="editorOpen"
        class="modal-overlay sp-editor-overlay"
        data-testid="sp-editor-overlay"
        tabindex="0"
        @click.self="closeEditor"
        @keydown.esc.prevent="closeEditor"
      >
        <div class="modal-box sp-editor-modal" role="dialog" aria-modal="true" data-testid="sp-editor-panel">
          <div class="sp-editor-head">
            <div>
              <div class="modal-title">{{ fallbackMode ? '查看提示词' : (editorMode === 'create' ? '新建提示词' : '编辑提示词') }}</div>
              <div class="sp-editor-tip">{{ form.agentKey || (activeTab === 'main' ? '主 Agent' : (activeTab === 'sub' ? '子 Agent' : '未分类')) }} · 作用域 {{ cwdDisplay }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="sp-editor-close-btn" @click="closeEditor">关闭</button>
          </div>

          <div class="sp-editor-body">
            <!-- Tab switch: [基础] / [🔧 高级调试] -->
            <div class="sub-tabs sp-editor-tabs" data-testid="sp-editor-tabs">
              <button class="sub-tab"
                :class="{ active: editorTab === 'basic' }"
                data-testid="sp-editor-tab-basic"
                @click="switchEditorTab('basic')">基础</button>
              <button class="sub-tab"
                :class="{ active: editorTab === 'advanced' }"
                data-testid="sp-editor-tab-advanced"
                :title="'高级调试：编辑 prompt_template_sections 的分段 / region / enable_when。普通用户无需使用。'"
                @click="switchEditorTab('advanced')">🔧 高级调试</button>
            </div>

            <!-- 基础 tab：原有的名称 / 描述 / 内容 -->
            <div v-show="editorTab === 'basic'" data-testid="sp-editor-basic">
              <div v-if="fallbackMode" class="sp-notice is-warn" data-testid="sp-editor-readonly-banner">
                {{ readonlyBannerMessage }}
              </div>

              <div class="sp-field">
                <label>名称</label>
                <input class="modal-input" data-testid="sp-name-input" v-model="form.name" placeholder="例如：代码审查专家" :disabled="saving || fallbackMode" />
              </div>

              <div class="sp-field">
                <label>描述（可选）</label>
                <input class="modal-input" data-testid="sp-desc-input" v-model="form.description" placeholder="一句话描述用途" :disabled="saving || fallbackMode" />
              </div>

              <div class="sp-field">
                <label>自动匹配条件（可选，JSON）</label>
                <textarea
                  class="sp-textarea"
                  data-testid="sp-matchwhen-input"
                  rows="3"
                  v-model="form.matchWhen"
                  placeholder='{} 代表总是参与自动匹配；{"cwd_prefix":"/Users/mac/work"} 代表仅在该目录下命中；留空 = 不参与自动路由'
                  :disabled="saving || fallbackMode"
                ></textarea>
                <div class="sp-field-meta">未打开 pin / 智能分类器时，系统会在这里命中的模板里按「优先级」挑一个注入</div>
              </div>

              <div class="sp-field">
                <label>优先级（整数，默认 0；数字越大越优先）</label>
                <input type="number" class="modal-input" data-testid="sp-priority-input" v-model.number="form.priority" :disabled="saving || fallbackMode" />
              </div>

              <div v-if="editingHasSections" class="sp-notice is-warn" data-testid="sp-editor-sections-banner">
                ⚠️ 此模板的实际注入内容由「🔧 高级调试」里的 sections 控制，下面的「提示词内容」仅作备份，在运行时会被 sections 覆盖。要改 AI 行为，请切到高级调试 tab 编辑对应 section。
              </div>

              <div class="sp-field">
                <label>提示词内容{{ editingHasSections ? '（备份，运行时被 sections 覆盖）' : '' }}</label>
                <textarea
                  class="sp-textarea"
                  data-testid="sp-content-input"
                  rows="12"
                  v-model="form.content"
                  placeholder="输入 System Prompt 内容..."
                  :disabled="saving || fallbackMode || editingHasSections"
                ></textarea>
                <div class="sp-field-meta">{{ countStats(form.content).lines }} 行 · {{ countStats(form.content).chars }} 字符</div>
              </div>

              <div v-if="notice.message" class="sp-notice" :class="'is-' + notice.level" data-testid="sp-editor-notice">
                {{ notice.message }}
              </div>

              <div class="sp-editor-actions" data-testid="sp-editor-actions">
                <button class="btn btn-ghost" @click="closeEditor">取消</button>
                <button class="btn btn-primary sp-save-btn" data-testid="sp-save-btn" :disabled="saveDisabled" @click="savePrompt">
                  {{ fallbackMode ? '只读模式' : (saving ? '保存中...' : '保存') }}
                </button>
              </div>
            </div>

            <!-- 高级调试 tab：sections 实际 CRUD 抽到 <sections-editor> 子组件 -->
            <sections-editor
              v-show="editorTab === 'advanced'"
              :prompt-id="form.id"
              :cwd="currentProjectCwd"
              :fallback-mode="fallbackMode"
              :visible="editorTab === 'advanced' && editorOpen"
              data-testid="sp-editor-advanced"
            />
          </div>
        </div>
      </div>
    </section>
  `,
};
