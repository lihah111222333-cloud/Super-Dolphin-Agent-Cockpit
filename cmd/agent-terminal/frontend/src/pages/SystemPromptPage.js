/**
 * SystemPromptPage — AI 能力与资料页面（消费端重构版）
 *
 * 布局：
 *   ┌──────────────────────────────────────────┐
 *   │ Header: AI 能力与资料        [CWD badge] │
 *   ├──────────────────────────────────────────┤
 *   │ Tabs: 全部 / 专家能力 / 参考资料 / 默认规则 │
 *   ├──────────────────────────────────────────┤
 *   │ Card Grid:                               │
 *   │  [+ 添加给 AI 的内容] [Card1] [Card2] ... │
 *   └──────────────────────────────────────────┘
 *
 * 点击卡片 → 模态编辑器（编辑 / 查看提示词）。
 * 普通编辑面板只展示语义字段，底层结构放进隐藏调试入口。
 */
// @ts-nocheck
import {
  h,
  defineComponent,
  ref,
  reactive,
  computed,
  onMounted,
  watch,
} from '../../lib/vue.esm-browser.prod.js';

import { callAPI, copyTextToClipboard } from '../services/api.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';
import { TagInput } from '../components/TagInput.js';
import { SectionsEditor } from './SectionsEditor.js';
import { PromptIntentWizard } from './PromptIntentWizard.js';
import {
  editButtonCopy,
  editorTitleCopy,
  emptyStateCopy, canForceLaunchPrompt,
  filterPromptCards,
  isReadonlyFallbackListError,
  normalizeFallbackPromptList,
  normalizePromptList,
  promptAssetBucket,
  promptAssetCounts,
  promptAdvancedDebugEnabled,
  PROMPT_ASSET_TABS,
  PROMPT_SCOPE_FILTERS,
  PROMPT_STATUS_FILTERS,
  promptWritePayloadFromForm,
  resolveProjectCwd,
  resolveReadonlyFallbackCwd,
  saveButtonCopy,
  withCwd,
} from './SystemPromptPage.helpers.js';
import { createPendingDraftActions } from './SystemPromptPage.pendingDraft.js';
import { createLaunchPromptActions, PREF_KEY_ACTIVE_PROMPT } from './SystemPromptPage.launchPrompt.js';

export { isReadonlyFallbackListError };
export { PREF_KEY_ACTIVE_PROMPT };

// ── Helpers (outside setup to keep size-guard happy) ──────────

const DEFAULT_ROLES = [
  { key: 'coder' },
  { key: 'pm' },
  { key: 'designer' },
];

function truncate(text, max = 80) {
  if (!text) return '暂无内容';
  return text.length > max ? text.slice(0, max) + '…' : text;
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
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
// 返回 null = 成功（空串代表 opt-out，显式发送 null）；返回字符串 = 人类可读的
// 错误消息，调用方应展示给用户并中止保存。
function applyMatchWhenToPayload(payload, rawText) {
  const text = (rawText || '').trim();
  if (!text) {
    payload.match_when = null;
    return null;
  }
  try {
    const parsed = JSON.parse(text);
    if (parsed && typeof parsed === 'object' && !Array.isArray(parsed)) {
      delete parsed.tags_has;
      if (Object.keys(parsed).length === 0) {
        payload.match_when = null;
        return null;
      }
    }
    payload.match_when = parsed;
    return null;
  } catch (err) {
    return `自动匹配条件不是合法 JSON：${toErrorMessage(err)}`;
  }
}

function setCopyPromptNotice(setNotice, ok) {
  if (ok) {
    setNotice('info', '已复制提示词内容');
  } else {
    setNotice('error', '复制失败');
  }
}

async function resolvePromptCopyText(item, deps) {
  const { getCwd, fallbackMode } = deps;
  const id = (item?.id || '').toString().trim();
  const cwd = getCwd();
  if (!cwd) return '';
  if (!id || fallbackMode.value) return (item?.content || '').toString();
  const res = await callAPI('prompts/get', withCwd(cwd, { id }));
  return (res?.prompt?.content || res?.prompt?.prompt_text || '').toString();
}

async function copyPromptContentWithDeps(item, deps) {
  const { getCwd, fallbackMode, setNotice } = deps;
  if (item?.isPendingDraft) {
    setNotice('info', '这条草稿还在待确认，确认保存后才能复制内容');
    return;
  }
  let text = '';
  try {
    text = (await resolvePromptCopyText(item, { getCwd, fallbackMode })).trim();
  } catch (error) {
    logWarn('system-prompt', 'copy.load.failed', { error });
    setNotice('error', `复制失败：${toErrorMessage(error)}`);
    return;
  }
  if (!text) {
    setNotice('error', '暂无可复制内容');
    return;
  }
  try {
    const ok = await copyTextToClipboard(text);
    setCopyPromptNotice(setNotice, ok);
  } catch (error) {
    setNotice('error', `复制失败：${toErrorMessage(error)}`);
  }
}

function finishPromptSave(form, editorMode, editorOpen, res) {
  const savedID = (res?.prompt?.id || res?.prompt?.prompt_key || '').toString().trim();
  if (editorMode.value === 'create' && savedID) {
    form.id = savedID;
    editorMode.value = 'edit';
    editorOpen.value = true;
    return;
  }
  editorOpen.value = false;
}

function refreshPromptsInBackground(loaders) {
  for (const loader of loaders) {
    if (typeof loader === 'function') loader().catch(() => {});
  }
}

function createPromptLoadActions(deps) {
  const {
    props, promptCards, loading, fallbackMode, readonlyReason, fallbackSource,
    getCwd, setNotice, enterReadonlyFallback, clearReadonlyFallback,
  } = deps;
  async function hydrateReadonlyPrompts() {
    const cwd = resolveReadonlyFallbackCwd(props);
    if (!cwd) return false;
    try {
      const res = await callAPI('dashboard/prompts', { cwd });
      const nextCards = normalizeFallbackPromptList(res?.prompts);
      if (!nextCards) return false;
      promptCards.value = nextCards;
      fallbackSource.value = 'dashboard/prompts';
      return true;
    } catch {
      return false;
    }
  }
  async function loadPrompts() {
    loading.value = true;
    try {
      const cwd = getCwd();
      if (!cwd) {
        promptCards.value = [];
        fallbackMode.value = true;
        readonlyReason.value = 'cwd unresolved';
        fallbackSource.value = '';
        setNotice('info', '');
        return promptCards.value;
      }
      const res = await callAPI('prompt-assets/list', withCwd(cwd, {}));
      promptCards.value = normalizePromptList(res?.prompts);
      clearReadonlyFallback('prompt-assets/list');
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
  return { loadPrompts };
}

function createIntentWizardActions(deps) {
  const { fallbackMode, getCwd, intentWizardOpen, pendingDraftForWizard, setReadonlyActionNotice, setNotice, loadPrompts } = deps;
  function openCreate() {
    if (fallbackMode.value) { setReadonlyActionNotice('新建'); return; }
    if (!getCwd()) { setNotice('error', '当前作用域未确定，无法新建提示词'); return; }
    pendingDraftForWizard.value = null;
    intentWizardOpen.value = true;
    setNotice('info', '');
    logDebug('system-prompt', 'intent.create');
  }
  async function handleIntentSaved() {
    intentWizardOpen.value = false;
    pendingDraftForWizard.value = null;
    await loadPrompts();
    setNotice('info', '已保存，可在新对话中被 AI 发现和使用');
  }
  function handleIntentClosed() {
    intentWizardOpen.value = false;
    pendingDraftForWizard.value = null;
  }
  return { openCreate, handleIntentSaved, handleIntentClosed };
}

export const SystemPromptPage = defineComponent({
  name: 'SystemPromptPage',
  components: { TagInput, SectionsEditor, PromptIntentWizard },
  props: {
    projectStore: { type: Object, default: null },
    threadStore: { type: Object, default: null },
    windowCwd: { type: String, default: '' },
  },
  setup(props) {
    // ── State ────────────────────────────────────────
    const activeTab = ref('all');
    const scopeFilter = ref('all');
    const statusFilter = ref('all');
    const roles = ref([...DEFAULT_ROLES]);
    const currentScopeCwd = ref('');
    const promptCards = ref([]);
    const loading = ref(false);
    const notice = reactive({ level: 'info', message: '' });
    const fallbackMode = ref(false);
    const readonlyReason = ref('');
    const fallbackSource = ref('');

    // Editor state
    const editorOpen = ref(false), editorMode = ref('edit'); // 'edit' | 'create'
    const saving = ref(false);
    const deletingId = ref('');
    const intentWizardOpen = ref(false), pendingDraftForWizard = ref(null);
    const advancedDebugAvailable = ref(promptAdvancedDebugEnabled()), advancedDebugOpen = ref(false), editorReadonly = computed(() => fallbackMode.value);
    // Active launch prompt: the row whose PromptText will be injected as
    // BaseInstructions on the next thread/start. Persisted via ui/preferences
    // under PREF_KEY_ACTIVE_PROMPT, scoped to project cwd so different repos
    // can pin different prompts independently.
    const activePromptId = ref('');
    const activatingId = ref('');
    const matchWhenDirty = ref(false);
    const form = reactive({
      id: '', name: '', content: '', originalContent: '', description: '',
      whenToUse: '',
      agentKey: '',
      tags: [],
      scope: 'project',
      matchWhen: '', priority: 0,
      enabled: true,
    });

    // ── Computed ─────────────────────────────────────
    const promptCounts = computed(() => promptAssetCounts(promptCards.value));
    const filteredCards = computed(() => filterPromptCards(promptCards.value, activeTab.value, scopeFilter.value, statusFilter.value));
    const cwdDisplay = computed(() =>
      currentScopeCwd.value || resolveProjectCwd(props.projectStore, props.windowCwd) || '未知'
    );
    // currentProjectCwd 是发给后端 RPC / 子组件的原始路径（空也 OK）；
    // cwdDisplay 含中文 fallback '未知'，专供 UI 显示用，不能当参数发出去。
    const currentProjectCwd = computed(() => resolveProjectCwd(props.projectStore, props.windowCwd));
    // Any state where the editor should be read-only matches exactly
    // `fallbackMode.value`.
    const createDisabled = computed(() => fallbackMode.value || !currentProjectCwd.value);
    const saveDisabled = computed(() => fallbackMode.value || saving.value || !currentProjectCwd.value);
    const deleteDisabled = computed(() => fallbackMode.value || Boolean(deletingId.value) || !currentProjectCwd.value);
    const activateDisabled = computed(() => fallbackMode.value || Boolean(activatingId.value) || !currentProjectCwd.value);
    const readonlyBannerMessage = computed(() => {
      if (!fallbackMode.value) return '';
      const dataSourceTip = fallbackSource.value === 'dashboard/prompts'
        ? '当前列表来自 dashboard/prompts 只读旁路。'
        : '当前保留已有列表或空态。';
      const reasonTip = readonlyReason.value
        ? `原因：${readonlyReason.value}。`
        : '';
      return `prompt-assets/list 暂不可用，页面已切换为只读模式；新建/保存/删除已禁用，后端恢复后会自动恢复。${dataSourceTip}${reasonTip}`;
    });

    // ── Helpers ─────────────────────────────────────
    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
    }

    function getCwd() {
      return resolveProjectCwd(props.projectStore, props.windowCwd);
    }

    function setReadonlyActionNotice(action) {
      setNotice('info', `当前为只读降级，暂不支持${action}`);
    }

    function enterReadonlyFallback(reason) {
      const nextReason = (reason || 'prompt-assets/list not found').trim();
      const wasFallback = fallbackMode.value;
      fallbackMode.value = true;
      readonlyReason.value = nextReason;
      fallbackSource.value = 'prompt-assets/list';
      const fields = { reason: nextReason, method: 'prompt-assets/list' };
      if (wasFallback) {
        logInfo('system-prompt', 'fallback.retry', fields);
        return;
      }
      logWarn('system-prompt', 'fallback.enter', fields);
    }

    function clearReadonlyFallback(recoveredBy = 'prompt-assets/list') {
      const hadFallback = fallbackMode.value || readonlyReason.value || fallbackSource.value;
      if (!hadFallback) return;
      fallbackMode.value = false;
      readonlyReason.value = '';
      fallbackSource.value = '';
      logInfo('system-prompt', 'fallback.recovered', { source: recoveredBy });
    }

    function switchTab(tab) {
      if (activeTab.value === tab) return;
      activeTab.value = tab;
      setNotice('info', '');
      logDebug('system-prompt', 'tab.switch', { tab });
    }

    function switchScopeFilter(nextFilter) {
      if (scopeFilter.value === nextFilter) return;
      scopeFilter.value = nextFilter;
      setNotice('info', '');
      logDebug('system-prompt', 'scope_filter.switch', { filter: nextFilter });
    }

    function switchStatusFilter(nextFilter) {
      if (statusFilter.value === nextFilter) return;
      statusFilter.value = nextFilter;
      setNotice('info', '');
      logDebug('system-prompt', 'status_filter.switch', { filter: nextFilter });
    }

    const { loadPrompts } = createPromptLoadActions({
      props, promptCards, loading, fallbackMode, readonlyReason, fallbackSource,
      getCwd, setNotice, enterReadonlyFallback, clearReadonlyFallback,
    });

    async function savePrompt() {
      if (fallbackMode.value) {
        setReadonlyActionNotice('保存');
        return;
      }
      if (saving.value) return;
      if (!getCwd()) {
        setNotice('error', '当前作用域未确定，无法保存提示词');
        return;
      }
      const name = (form.name || '').trim();
      if (!name) {
        setNotice('error', '请填写提示词名称');
        return;
      }
      saving.value = true;
      try {
        // Edit 保留原 agent_key；ordinary create defaults to main because
        // asset tabs are UI categories, not runtime agent keys.
        const payload = promptWritePayloadFromForm(form, activeTab.value, name);
        // 仅当用户实际敲过 matchWhen textarea（matchWhenDirty=true）才尊重它。
        // 未触碰时不发 match_when，让后端保留现有路由配置。
        if (matchWhenDirty.value) {
          const matchWhenErr = applyMatchWhenToPayload(payload, form.matchWhen || '');
          if (matchWhenErr) { setNotice('error', matchWhenErr); saving.value = false; return; }
        }
        const res = await callAPI('prompts/write', withCwd(getCwd(), payload));
        await loadPrompts();
        form.originalContent = form.content || '';
        finishPromptSave(form, editorMode, editorOpen, res);
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
      if (item?.isPendingDraft) {
        setNotice('info', '这条草稿还在待确认，暂不能作为已保存提示词删除');
        return;
      }
      if (!getCwd()) {
        setNotice('error', '当前作用域未确定，无法删除提示词');
        return;
      }
      const id = (item?.id || '').toString();
      if (!id || deletingId.value) return;
      deletingId.value = id;
      try {
        await callAPI('prompts/delete', withCwd(getCwd(), {
          id,
          scope: item?.scope === 'global' ? 'global' : 'project',
        }));
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

    const { loadActivePromptId, setLaunchPrompt, clearLaunchPrompt, sanitizeActivePromptId } = createLaunchPromptActions({
      getCwd, fallbackMode, promptCards, activePromptId, activatingId, setNotice, setReadonlyActionNotice,
    });

    const copyPromptContent = item => copyPromptContentWithDeps(item, { getCwd, fallbackMode, setNotice });

    // ── Editor ──────────────────────────────────────
    const { openCreate, handleIntentSaved, handleIntentClosed } = createIntentWizardActions({ fallbackMode, getCwd, intentWizardOpen, pendingDraftForWizard, setReadonlyActionNotice, setNotice, loadPrompts });

    const { continuePendingDraft, discardPendingDraft } = createPendingDraftActions({
      getCwd, fallbackMode, deletingId, intentWizardOpen, pendingDraftForWizard,
      loadPrompts, setNotice, setReadonlyActionNotice,
    });

    function openEdit(item) {
      if (item?.isPendingDraft) {
        setNotice('info', '这条草稿还在待确认，请在新建提示词流程中确认保存');
        return;
      }
      Object.assign(form, {
        id: item.id || '', name: item.name || '', content: item.content || '',
        originalContent: item.content || '',
        description: item.description || '', whenToUse: item.whenToUse || '',
        agentKey: (item.agentType || '').toString(),
        tags: Array.isArray(item.tags) ? [...item.tags] : [],
        scope: item.scope === 'global' ? 'global' : 'project',
        matchWhen: serializeMatchWhenForEditor(item.match_when),
        priority: Number.isFinite(Number(item.priority)) ? Number(item.priority) : 0,
        enabled: item.enabled !== false,
      });
      matchWhenDirty.value = false;
      advancedDebugOpen.value = false;
      editorMode.value = 'edit'; editorOpen.value = true;
      setNotice('info', '');
      logDebug('system-prompt', 'editor.edit', { id: item.id });
    }

    function closeEditor() {
      editorOpen.value = false;
      advancedDebugOpen.value = false;
      setNotice('info', '');
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

    const refreshPromptsSilently = () => refreshPromptsInBackground([loadPrompts, loadActivePromptId]);

    // ── Lifecycle ───────────────────────────────────
    onMounted(() => {
      logInfo('system-prompt', 'page.mounted');
      loadCurrentScopeCwd();
      refreshPromptsSilently();
    });

    watch(
      () => resolveProjectCwd(props.projectStore, props.windowCwd),
      (next, prev) => {
        if (next === prev) return;
        loadCurrentScopeCwd();
        refreshPromptsSilently();
      },
    );

    watch(() => [activePromptId.value, promptCards.value], () => { sanitizeActivePromptId().catch(() => {}); });

    return {
      activeTab, roles, promptCounts,
      assetTabs: PROMPT_ASSET_TABS, scopeFilters: PROMPT_SCOPE_FILTERS, statusFilters: PROMPT_STATUS_FILTERS,
      scopeFilter, statusFilter,
      promptCards, filteredCards, loading,
      notice, fallbackMode, readonlyReason, fallbackSource,
      readonlyBannerMessage, emptyStateCopy, editorTitleCopy, saveButtonCopy, editButtonCopy,
      editorOpen, editorMode, editorReadonly, saving, deletingId,
      intentWizardOpen, pendingDraftForWizard, advancedDebugAvailable, advancedDebugOpen,
      createDisabled, saveDisabled, deleteDisabled,
      activePromptId, activatingId, activateDisabled,
      form, matchWhenDirty, cwdDisplay, currentProjectCwd,
      switchTab, switchScopeFilter, switchStatusFilter, loadPrompts, savePrompt, deletePrompt,
      copyPromptContent, openCreate, openEdit, closeEditor,
      continuePendingDraft, discardPendingDraft,
      handleIntentSaved, handleIntentClosed,
      setLaunchPrompt, clearLaunchPrompt, loadActivePromptId,
      truncate, promptAssetBucket, canForceLaunchPrompt,
    };
  },
  template: `
    <section id="page-prompts" class="page active sp-page" data-testid="system-prompt-page">
      <prompt-intent-wizard
        :cwd="currentProjectCwd"
        :visible="intentWizardOpen"
        :fallback-mode="fallbackMode || !currentProjectCwd"
        :initial-draft="pendingDraftForWizard"
        @close="handleIntentClosed"
        @drafted="loadPrompts"
        @saved="handleIntentSaved"
      />

      <!-- Header -->
      <div class="panel-header" data-testid="sp-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>AI 能力与资料</h2></div>
        <span class="sp-cwd-badge" data-testid="sp-cwd-badge" :title="cwdDisplay">
          <svg class="sp-cwd-icon" viewBox="0 0 16 16" fill="none">
            <path d="M2 4.5A1.5 1.5 0 013.5 3h3.293a1 1 0 01.707.293L8.707 4.5H12.5A1.5 1.5 0 0114 6v5.5a1.5 1.5 0 01-1.5 1.5h-9A1.5 1.5 0 012 11.5v-7z" stroke="currentColor" stroke-width="1.2"/>
          </svg>
          <span class="sp-cwd-text">{{ cwdDisplay }}</span>
        </span>
      </div>

      <div class="sp-asset-tabs" data-testid="sp-asset-tabs">
        <button
          v-for="tab in assetTabs"
          :key="tab.key"
          class="sp-asset-tab"
          :class="{ active: activeTab === tab.key }"
          :disabled="fallbackMode"
          :data-testid="'sp-asset-tab-' + tab.key"
          @click="switchTab(tab.key)"
        >
          <span class="sp-asset-tab-icon">{{ tab.icon }}</span>
          <span class="sp-asset-tab-label">{{ tab.label }}</span>
          <span class="sp-asset-tab-count">{{ promptCounts[tab.key] || 0 }} 条</span>
        </button>
      </div>

      <div class="sp-secondary-filters" data-testid="sp-secondary-filters">
        <div class="sp-filter-group" data-testid="sp-scope-filter">
          <span class="sp-filter-label">范围</span>
          <button
            v-for="item in scopeFilters"
            :key="item.key"
            class="sp-filter-chip"
            :class="{ active: scopeFilter === item.key }"
            :disabled="fallbackMode"
            :data-testid="'sp-scope-filter-' + item.key"
            @click="switchScopeFilter(item.key)"
          >{{ item.label }}</button>
        </div>
        <div class="sp-filter-group" data-testid="sp-status-filter">
          <span class="sp-filter-label">状态</span>
          <button
            v-for="item in statusFilters"
            :key="item.key"
            class="sp-filter-chip"
            :class="{ active: statusFilter === item.key }"
            :disabled="fallbackMode"
            :data-testid="'sp-status-filter-' + item.key"
            @click="switchStatusFilter(item.key)"
          >{{ item.label }}</button>
        </div>
      </div>

      <!-- Card list -->
      <div class="section-header">
        内容列表
        <span v-if="filteredCards.length > 0" class="sp-count-tip">{{ filteredCards.length }} 条</span>
      </div>

      <div class="panel-body sp-list-panel" data-testid="sp-body">

        <!-- Toolbar -->
        <div class="sp-toolbar" data-testid="sp-toolbar">
          <button class="btn btn-secondary" data-testid="sp-create-btn" :disabled="createDisabled" @click="openCreate">+ 添加给 AI 的内容</button>
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
          <h3>暂无内容</h3>
          <p>{{ emptyStateCopy(fallbackMode, activeTab) }}</p>
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
            :class="{ active: editorOpen && form.id === item.id, 'is-disabled': item.enabled === false && !item.isPendingDraft, 'is-pending-draft': item.isPendingDraft, 'is-recall': item.assetType === 'recall', 'is-default-rule': item.assetType === 'default_rule' }"
            :data-testid="'sp-card-' + idx"
          >
            <div class="sp-card-header">
              <div class="sp-card-heading">
                <div class="sp-card-title">{{ item.name || '未命名' }}</div>
                <span v-if="item.isDefault" class="sp-card-badge is-default">默认</span>
                <span v-if="promptAssetBucket(item) === 'expert'" class="sp-card-badge is-asset">专家能力</span>
                <span v-if="item.assetType === 'recall'" class="sp-card-badge is-asset">参考资料</span>
                <span v-if="item.assetType === 'default_rule'" class="sp-card-badge is-asset">默认规则</span>
                <span v-if="item.scope === 'global'" class="sp-card-badge is-global" :data-testid="'sp-scope-badge-' + idx">全局可用</span>
                <span v-if="item.isPendingDraft" class="sp-card-badge is-pending">待确认</span>
                <span v-if="item.enabled === false && !item.isPendingDraft" class="sp-card-badge is-disabled">已停用</span>
                <span v-if="activePromptId === item.id && canForceLaunchPrompt(item)" class="sp-card-badge is-active" :data-testid="'sp-active-badge-' + idx">强制中</span>
              </div>
            </div>
            <div v-if="item.description" class="sp-card-desc">{{ item.description }}</div>
            <div v-if="item.tags && item.tags.length" class="sp-card-tags">
              <span class="sp-card-tag" v-for="tag in item.tags" :key="tag">{{ tag }}</span>
            </div>
            <div class="sp-card-preview">{{ truncate(item.preview) }}</div>
            <div v-if="item.isPendingDraft" class="sp-card-actions">
              <button class="btn btn-primary btn-xs" :data-testid="'sp-pending-continue-btn-' + idx" @click="continuePendingDraft(item)">继续确认</button>
              <button class="btn btn-ghost btn-xs btn-warning" :data-testid="'sp-pending-discard-btn-' + idx" :disabled="deletingId === item.id || deletingId === item.draftKey" @click="discardPendingDraft(item)">
                {{ deletingId === item.id || deletingId === item.draftKey ? '丢弃中...' : '丢弃' }}
              </button>
            </div>
            <div v-else class="sp-card-actions">
              <button class="btn btn-secondary btn-xs" :data-testid="'sp-edit-btn-' + idx" :disabled="item.isPendingDraft" @click="openEdit(item)">{{ editButtonCopy(item, fallbackMode) }}</button>
              <button class="btn btn-ghost btn-xs" :data-testid="'sp-copy-btn-' + idx" @click="copyPromptContent(item)">复制</button>
              <button
                v-if="activePromptId === item.id && canForceLaunchPrompt(item)"
                class="btn btn-ghost btn-xs"
                :data-testid="'sp-clear-launch-btn-' + idx"
                :disabled="activateDisabled"
                @click="clearLaunchPrompt"
              >{{ activatingId === 'clear' ? '处理中...' : '取消强制' }}</button>
              <button
                v-else-if="canForceLaunchPrompt(item)"
                class="btn btn-ghost btn-xs"
                :data-testid="'sp-set-launch-btn-' + idx"
                :disabled="activateDisabled"
                @click="setLaunchPrompt(item)"
              >{{ activatingId === item.id ? '处理中...' : '强制使用' }}</button>
              <button class="btn btn-ghost btn-xs btn-warning" :data-testid="'sp-delete-btn-' + idx" :disabled="deleteDisabled || item.isPendingDraft" @click="deletePrompt(item)">
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
              <div class="modal-title">{{ editorTitleCopy(fallbackMode, editorMode) }}</div>
              <div class="sp-editor-tip">{{ form.scope === 'global' ? '全局可用' : '这个项目' }} · {{ cwdDisplay }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="sp-editor-close-btn" @click="closeEditor">关闭</button>
          </div>

          <div class="sp-editor-body" data-testid="sp-editor-basic">
              <div v-if="fallbackMode" class="sp-notice is-warn" data-testid="sp-editor-readonly-banner">
                {{ readonlyBannerMessage }}
              </div>

              <div class="sp-scope-copy" data-testid="sp-scope-copy">
                <div>可用范围：{{ form.scope === 'global' ? '全局可用' : '这个项目' }}</div>
                <div class="sp-scope-segmented" data-testid="sp-editor-scope-group">
                  <label class="sp-scope-option" :class="{ active: form.scope !== 'global' }">
                    <input
                      type="radio"
                      value="project"
                      v-model="form.scope"
                      data-testid="sp-editor-scope-project"
                      :disabled="saving || fallbackMode"
                    />
                    <span>这个项目</span>
                  </label>
                  <label class="sp-scope-option" :class="{ active: form.scope === 'global' }">
                    <input
                      type="radio"
                      value="global"
                      v-model="form.scope"
                      data-testid="sp-editor-scope-global"
                      :disabled="saving || fallbackMode"
                    />
                    <span>全局可用</span>
                  </label>
                </div>
                <div>{{ form.scope === 'global' ? '说明：其他项目也可以使用；当前项目同名资产优先。' : '说明：只在当前项目的对话中使用。' }}</div>
              </div>

              <div class="sp-field">
                <label>名称</label>
                <input class="modal-input" data-testid="sp-name-input" v-model="form.name" placeholder="例如：代码审查专家" :disabled="saving || fallbackMode" />
              </div>

              <div class="sp-field">
                <label>一句话描述</label>
                <input class="modal-input" data-testid="sp-desc-input" v-model="form.description" placeholder="简要说明用途" :disabled="saving || fallbackMode" />
              </div>

              <div class="sp-field">
                <label>AI 什么时候会使用它</label>
                <textarea class="sp-textarea" rows="3" v-model="form.whenToUse" placeholder="例如：当用户需要代码审查、缺陷定位或提交前风险检查时使用" :disabled="saving || fallbackMode" data-testid="sp-when-to-use-input"></textarea>
              </div>

              <div class="sp-field">
                <label>AI 使用时怎么做</label>
                <textarea class="sp-textarea" rows="5" v-model="form.content" placeholder="写给 AI 的执行说明：先做什么、重点检查什么、输出什么结果" :disabled="saving || fallbackMode" data-testid="sp-execution-input"></textarea>
              </div>

              <div class="sp-field">
                <label>保存后 AI 会看到什么</label>
                <textarea class="sp-textarea sp-textarea-readonly" data-testid="sp-preview-input" rows="3" :value="form.content || form.whenToUse || form.description || '已保存，AI 会在相关场景中使用'" readonly></textarea>
              </div>

              <div class="sp-field">
                <label class="sp-toggle-inline">
                  <input type="checkbox" data-testid="sp-enabled-checkbox" v-model="form.enabled" :disabled="saving || fallbackMode" />
                  <span>启用状态</span>
                </label>
              </div>

              <details class="sp-advanced-debug" data-testid="sp-advanced-debug" v-if="advancedDebugAvailable" @toggle="advancedDebugOpen = $event.target.open">
                <summary>高级调试</summary>
                <div class="sp-advanced-body">
                  <div class="sp-field">
                    <label>Agent Key</label>
                    <select class="modal-input" v-model="form.agentKey" :disabled="saving || fallbackMode" data-testid="sp-agent-key-select">
                      <option value="">未分类</option>
                      <option v-for="r in roles" :key="r.key" :value="r.key">{{ r.key }}</option>
                    </select>
                  </div>

                  <div class="sp-field">
                    <label>场景标签</label>
                    <tag-input v-model="form.tags" placeholder="输入标签后按回车" :disabled="saving || fallbackMode" data-testid="sp-tags-input" />
                  </div>

                  <div class="sp-field">
                    <label>自动匹配 JSON</label>
                    <textarea
                      class="sp-textarea"
                      rows="4"
                      v-model="form.matchWhen"
                      placeholder='{"cwd_prefix":"/repo"}'
                      :disabled="saving || fallbackMode"
                      @input="matchWhenDirty = true"
                      data-testid="sp-match-when-input"
                    ></textarea>
                  </div>

                  <div class="sp-field">
                    <label>排序权重</label>
                    <input type="number" class="modal-input" v-model.number="form.priority" :disabled="saving || fallbackMode" data-testid="sp-priority-input" />
                  </div>

                  <sections-editor
                    v-if="advancedDebugOpen"
                    :prompt-id="form.id"
                    :cwd="currentProjectCwd"
                    :prompt-scope="form.scope"
                    :fallback-mode="fallbackMode"
                    :visible="advancedDebugOpen"
                  />
                </div>
              </details>

              <div v-if="notice.message" class="sp-notice" :class="'is-' + notice.level" data-testid="sp-editor-notice">
                {{ notice.message }}
              </div>

              <div class="sp-editor-actions" data-testid="sp-editor-actions">
                <button class="btn btn-ghost" @click="closeEditor">取消</button>
                <button class="btn btn-primary sp-save-btn" data-testid="sp-save-btn" :disabled="saveDisabled" @click="savePrompt">
                  {{ saveButtonCopy(fallbackMode, saving) }}
                </button>
              </div>
          </div>
        </div>
      </div>
    </section>
  `,
});
