import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import {
  useDurableMemoryEditor,
  useInlineDeleteConfirm,
} from '../composables/useMemoryEditors.js';
import { useSimilarityIgnore, pairKey } from '../composables/useSimilarityIgnore.js';

const TYPE_BADGE_CLASS = Object.freeze({
  project: 'jr-badge-primary',
  feedback: 'jr-badge-warning',
  reference: 'jr-badge-success',
  user: 'jr-badge-default',
});

function ensureArray(value) {
  return Array.isArray(value) ? value : [];
}

function ensureObject(value) {
  return value && typeof value === 'object' ? value : {};
}

function formatTimestamp(value) {
  if (!value) return '';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '';
  return date.toLocaleString('zh-CN', {
    month: 'numeric', day: 'numeric',
    hour: '2-digit', minute: '2-digit',
    hour12: false,
  });
}

function firstNonEmpty(...values) {
  for (const value of values) {
    const text = (value || '').toString().trim();
    if (text) return text;
  }
  return '';
}

function typeBadgeClass(type) {
  return TYPE_BADGE_CLASS[(type || '').toString()] || 'jr-badge-default';
}

function typeBadgeLabel(type) {
  switch ((type || '').toString()) {
    case 'user': case 'feedback': return '偏好';
    case 'project': case 'reference': return '项目';
    default: return type || '未知';
  }
}

function headlineOf(text) {
  if (!text) return '';
  const s = text.toString().trim();
  const m = s.match(/^[^，。、;,（）“”/]+/);
  const raw = m ? m[0].trim() : s;
  const candidate = raw.length < 5 ? s : raw;
  let width = 0, end = 0;
  const chars = [...candidate];
  for (const ch of chars) {
    const w = /[一-鿿　-〿＀-￯]/.test(ch) ? 2 : 1;
    if (width + w > 24) break;
    width += w;
    end++;
  }
  return end < chars.length ? chars.slice(0, end).join('') + '…' : candidate;
}

function filterEntries(list, needle) {
  const term = (needle || '').toString().trim().toLowerCase();
  if (!term) return list;
  return list.filter((entry) => {
    const haystack = [
      entry?.title,
      entry?.name,
      entry?.description,
      entry?.path,
      entry?.type,
      entry?.preview,
    ].map((part) => (part || '').toString().toLowerCase()).join(' \u0001 ');
    return haystack.includes(term);
  });
}

function statusLabel(enabled) {
  return enabled ? '已启用' : '未启用';
}

function normalizeAutoDreamIntent(value) {
  if (value === true) return true;
  if (value === false) return false;
  return null;
}

function useAutoDreamControls(overview, setNotice, emit) {
  const runtimeEnabled = computed(() => overview.value.autoDreamEnabled === true);
  const intent = computed(() => normalizeAutoDreamIntent(overview.value.autoDreamIntent));
  const enabled = computed(() => (intent.value === null ? runtimeEnabled.value : intent.value));
  const status = computed(() => (enabled.value ? '已开启' : '已关闭'));
  const pendingRestart = computed(() => intent.value !== null && intent.value !== runtimeEnabled.value);
  const toggling = ref(false);

  async function toggle() {
    if (toggling.value) return;
    const next = !enabled.value;
    toggling.value = true;
    try {
      await callAPI('ui/memory/auto-dream/set-intent', { enabled: next });
      setNotice('warning', `自动沉淀已切换为${next ? '开启' : '关闭'}，重启 agent-terminal 后生效`);
      emit('refresh');
    } catch (error) {
      setNotice('error', `切换自动沉淀失败：${(error && error.message) || String(error || '')}`);
    } finally {
      toggling.value = false;
    }
  }

  return { enabled, status, pendingRestart, toggling, toggle };
}

export const MemoryCenterPage = {
  name: 'MemoryCenterPage',
  props: {
    model: { type: Object, required: true },
  },
  emits: ['refresh'],
  setup(props, { emit }) {
    const notice = reactive({ level: 'info', message: '' });
    const busyPath = ref('');
    const searchText = ref('');
    const refreshing = ref(false);

    let noticeTimer = null;

    const overview = computed(() => ensureObject(props.model?.overview));
    const privateMemory = computed(() => ensureObject(props.model?.private));
    const teamMemory = computed(() => ensureObject(props.model?.team));
    const isLoading = computed(() => props.model?.loading === true);

    const currentCwd = computed(() => firstNonEmpty(overview.value.projectRoot));
    const systemDisabled = computed(() => overview.value.enabled === false);
    const autoDream = useAutoDreamControls(overview, setNotice, emit);

    const health = computed(() => overview.value.health || null);
    function healthPercent(count, max) {
      const safeMax = Number(max) || 1;
      const safeCount = Number(count) || 0;
      return Math.min(100, Math.max(0, Math.round((safeCount / safeMax) * 100)));
    }
    const healthPrefPercent = computed(() => {
      if (!health.value) return 0;
      return healthPercent(health.value.preferenceCount, health.value.maxPerCategory);
    });
    const healthProjPercent = computed(() => {
      if (!health.value) return 0;
      return healthPercent(health.value.projectCount, health.value.maxPerCategory);
    });

    function healthBarClass(percent) {
      if (percent >= 100) return 'health-bar-danger';
      if (percent >= 80) return 'health-bar-warning';
      return '';
    }

    function formatScore(score) {
      return Math.round((score || 0) * 100) + '%';
    }

    const mergingGroup = ref(null);
    const mergeConfirm = reactive({ target: null, index: -1 });

    function resetMergeConfirm() {
      mergeConfirm.target = null;
      mergeConfirm.index = -1;
    }

    function askMergeGroup(group, idx) {
      if (!group || mergingGroup.value !== null) return;
      mergeConfirm.target = group;
      mergeConfirm.index = idx;
    }

    async function confirmMergeGroup() {
      const group = mergeConfirm.target;
      if (!group || mergingGroup.value !== null) return;
      mergingGroup.value = mergeConfirm.index;
      try {
        await callAPI('ui/memory/entry/merge', {
          cwd: currentCwd.value,
          targetA: group.targetA, pathA: group.pathA,
          targetB: group.targetB, pathB: group.pathB,
        });
        setNotice('info', `已整合「${group.nameA}」与「${group.nameB}」`);
        resetMergeConfirm();
        emit('refresh');
      } catch (error) {
        setNotice('error', `整合失败：${(error && error.message) || String(error || '')}`);
      } finally {
        mergingGroup.value = null;
      }
    }

    // --- Similar groups: expand + merge all ---
    const similarExpanded = ref(false);
    function toggleSimilarExpand() { similarExpanded.value = !similarExpanded.value; }

    const mergingAll = ref(false);
    async function mergeAllGroups() {
      const total = (health.value?.similarGroups || []).length;
      if (!total || mergingAll.value) return;
      mergingAll.value = true;
      setNotice('info', '智能整合中（通常 10-20 秒），请勿离开', true);
      try {
        const res = await callAPI('ui/memory/similarity/consolidate-all', { cwd: currentCwd.value });
        const merged = (res && res.merged) || 0;
        const ignored = (res && res.ignored) || 0;
        const failed = (res && res.failed) || 0;
        const skipped = (res && res.skipped) || 0;
        const firstErr = res && Array.isArray(res.errors) && res.errors[0];
        const parts = [`已整合 ${merged} 组`];
        if (ignored) parts.push(`${ignored} 组判定不应合（已忽略）`);
        if (failed) parts.push(`${failed} 组失败`);
        if (skipped) parts.push(`${skipped} 组跳过`);
        const detail = firstErr ? `，原因：${firstErr}` : '';
        const level = failed || skipped ? 'warning' : 'info';
        setNotice(level, parts.join('，') + detail);
        emit('refresh');
      } catch (error) {
        setNotice('error', `智能整合失败：${(error && error.message) || String(error || '')}`);
      } finally {
        mergingAll.value = false;
      }
    }



    // --- Type-based grouping ---
    const preferenceEntries = computed(() => {
      const priv = ensureArray(props.model?.private?.entries)
        .filter(e => e.type === 'user' || e.type === 'feedback')
        .map(e => ({ ...e, _scope: 'private', _target: 'private' }));
      const team = ensureArray(props.model?.team?.entries)
        .filter(e => e.type === 'user' || e.type === 'feedback')
        .map(e => ({ ...e, _scope: 'team', _target: 'team' }));
      return [...priv, ...team];
    });

    const projectEntries = computed(() => {
      const priv = ensureArray(props.model?.private?.entries)
        .filter(e => e.type === 'project' || e.type === 'reference')
        .map(e => ({ ...e, _scope: 'private', _target: 'private' }));
      const team = ensureArray(props.model?.team?.entries)
        .filter(e => e.type === 'project' || e.type === 'reference')
        .map(e => ({ ...e, _scope: 'team', _target: 'team' }));
      return [...priv, ...team];
    });

    const filteredPreferenceEntries = computed(() => filterEntries(preferenceEntries.value, searchText.value));
    const filteredProjectEntries = computed(() => filterEntries(projectEntries.value, searchText.value));
    const totalEntries = computed(() => preferenceEntries.value.length + projectEntries.value.length);

    function setNotice(level, message, persistent) {
      notice.level = level || 'info';
      const text = (message || '').toString().trim();
      // C7: 截到 120 字符防止超长 LLM/RPC 错误把通知条撑爆。
      notice.message = text.length > 120 ? text.slice(0, 119) + '…' : text;
      if (noticeTimer) { clearTimeout(noticeTimer); noticeTimer = null; }
      // B2: persistent=true 跳过 5.2s 自清，给 long-running RPC (LLM 整合 10-20s) 用；
      //     正常 info/warning 仍走自清避免通知条永久挂着。
      if (notice.message && level !== 'error' && !persistent) {
        noticeTimer = setTimeout(() => { notice.message = ''; }, 5200);
      }
    }

    function setBusy(path) { busyPath.value = path || ''; }

    // --- Tab switching ---
    const activeTab = ref('pref');
    const visibleEntries = computed(() => {
      const search = searchText.value;
      if (activeTab.value === 'pref') return filterEntries(preferenceEntries.value, search);
      if (activeTab.value === 'proj') return filterEntries(projectEntries.value, search);
      return filterEntries([...preferenceEntries.value, ...projectEntries.value], search);
    });

    function switchTab(tab) { activeTab.value = tab; }

    // --- Create dropdown ---
    const createMenuOpen = ref(false);
    function toggleCreateMenu(e) {
      if (e) e.stopPropagation();
      const next = !createMenuOpen.value;
      createMenuOpen.value = next;
      if (next) {
        setTimeout(() => document.addEventListener('click', closeCreateMenu, { once: true }), 0);
      }
    }
    function handleCreatePreference() {
      createMenuOpen.value = false;
      createPreference();
    }
    function handleCreateProject() {
      createMenuOpen.value = false;
      createProject();
    }
    function closeCreateMenu() { createMenuOpen.value = false; }

    // --- Narrow-screen search toggle ---
    const searchExpanded = ref(false);
    function toggleSearch() { searchExpanded.value = !searchExpanded.value; }

    const memoryEditor = useDurableMemoryEditor({ currentCwd, setNotice, setBusy, emit });
    const inlineDelete = useInlineDeleteConfirm({ currentCwd, setNotice, emit });

    const memoryIdentityLocked = computed(
      () => memoryEditor.mode === 'edit' && Boolean(memoryEditor.form.existingPath),
    );

    function clearSearch() { searchText.value = ''; }

    function handleRefresh() {
      if (refreshing.value) return;
      refreshing.value = true;
      emit('refresh');
      setTimeout(() => { refreshing.value = false; }, 800);
    }

    function createPreference() {
      memoryEditor.openCreate('private');
      memoryEditor.form.type = 'feedback';
    }

    function createProject() {
      memoryEditor.openCreate('private');
      memoryEditor.form.type = 'project';
    }

    function askEditorDelete() {
      inlineDelete.ask(memoryEditor.form.target, {
        path: memoryEditor.form.existingPath,
        name: memoryEditor.form.name || memoryEditor.form.existingPath,
      });
      memoryEditor.close();
    }

    watch(() => props.model, () => { refreshing.value = false; });
    onBeforeUnmount(() => { if (noticeTimer) clearTimeout(noticeTimer); });

    return {
      notice,
      busyPath,
      overview,
      privateMemory,
      teamMemory,
      isLoading,
      currentCwd,

      memoryEditor,
      inlineDelete,
      memoryIdentityLocked,
      preferenceEntries,
      projectEntries,
      totalEntries,
      searchText,
      refreshing,
      activeTab,
      switchTab,
      visibleEntries,
      createMenuOpen,
      toggleCreateMenu,
      handleCreatePreference,
      handleCreateProject,
      closeCreateMenu,
      searchExpanded,
      toggleSearch,
      systemDisabled,
      autoDreamEnabled: autoDream.enabled,
      autoDreamStatusLabel: autoDream.status,
      autoDreamPendingRestart: autoDream.pendingRestart,
      autoDreamToggling: autoDream.toggling,
      toggleAutoDream: autoDream.toggle,
      health,
      healthPrefPercent,
      healthProjPercent,
      healthBarClass,
      formatScore,
      mergingGroup,
      mergeConfirm,
      askMergeGroup,
      confirmMergeGroup,
      resetMergeConfirm,
      similarExpanded,
      toggleSimilarExpand,
      mergingAll, mergeAllGroups,
      ...useSimilarityIgnore({ currentCwd, setNotice, emit }),
      pairKey,
      headlineOf,
      formatTimestamp,
      statusLabel,
      typeBadgeClass,
      typeBadgeLabel,
      clearSearch,
      handleRefresh,
      createPreference,
      createProject,
      askEditorDelete,
    };
  },
  template: `
    <section id="page-memory-center" class="page active mc-page" data-testid="memory-center-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text">
          <h2><span class="mc-toolbar-icon">M</span> 记忆中心</h2>
        </div>
        <div class="mc-toolbar" data-testid="memory-center-toolbar">
          <div class="mc-search-wrap" :class="{ 'is-open': searchExpanded }">
            <button class="mc-search-toggle" aria-label="搜索" @click.stop="toggleSearch">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><circle cx="7" cy="7" r="4.5"/><path d="M10.5 10.5l3 3" stroke-linecap="round"/></svg>
            </button>
            <input
              v-model="searchText"
              class="mc-search-input"
              data-testid="memory-center-search"
              placeholder="搜索 name / description / path"
            />
            <button v-if="searchText" class="mc-search-clear" data-testid="memory-center-search-clear" aria-label="清除" @click="clearSearch">×</button>
          </div>
          <button class="btn btn-ghost btn-toolbar-sm" data-testid="memory-center-refresh" :disabled="refreshing" @click="handleRefresh">
            <span v-if="refreshing" class="memory-refresh-spin" aria-hidden="true"></span>
            {{ refreshing ? '刷新中' : '刷新' }}
          </button>
          <div class="mc-create-dropdown" @click.stop>
            <button class="btn btn-primary btn-toolbar-sm" @click="toggleCreateMenu">+ 新建 ▾</button>
            <div v-if="createMenuOpen" class="mc-create-menu">
              <button class="mc-create-option" @click="handleCreatePreference">新建偏好</button>
              <button class="mc-create-option" @click="handleCreateProject">新建项目</button>
            </div>
          </div>
        </div>
      </div>

      <div class="panel-body mc-body" data-testid="memory-center-body">

        <div class="mc-bento" :class="{ 'mc-bento-2col': !health }">
          <div class="mc-bento-card">
            <div class="mc-bento-label">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><rect x="2" y="2" width="12" height="12" rx="2"/><path d="M2 6h12M6 6v8"/></svg>
              总览
            </div>
            <div class="mc-bento-num">{{ totalEntries }}</div>
            <div class="mc-bento-sub">
              <span><span class="mc-dot mc-dot-pref"></span>{{ preferenceEntries.length }} 偏好</span>
              <span><span class="mc-dot mc-dot-proj"></span>{{ projectEntries.length }} 项目</span>
            </div>
          </div>

          <div v-if="health" class="mc-bento-card" data-testid="memory-center-health-card">
            <div class="mc-bento-label">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M8 2C5.5 2 2 4.5 2 7.5 2 12 8 14.5 8 14.5S14 12 14 7.5C14 4.5 10.5 2 8 2Z"/></svg>
              健康度
            </div>
            <div class="mc-health-row">
              <span class="mc-health-lbl">偏好</span>
              <div class="mc-health-track"><div class="mc-health-fill" :class="healthBarClass(healthPrefPercent)" :style="{ width: healthPrefPercent + '%' }"></div></div>
              <span class="mc-health-val">{{ health.preferenceCount }} / {{ health.maxPerCategory }}</span>
            </div>
            <div class="mc-health-row">
              <span class="mc-health-lbl">项目</span>
              <div class="mc-health-track"><div class="mc-health-fill" :class="healthBarClass(healthProjPercent)" :style="{ width: healthProjPercent + '%' }"></div></div>
              <span class="mc-health-val">{{ health.projectCount }} / {{ health.maxPerCategory }}</span>
            </div>
            <div style="margin-top:8px;font-size:11px;color:var(--text-muted);display:flex;align-items:center;gap:6px">
              <span class="mc-status-dot on"></span> 综合良好
            </div>
          </div>

          <div class="mc-bento-card" data-testid="memory-center-auto-dream-card">
            <div class="mc-bento-label">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5"><path d="M13.5 8.5a5.5 5.5 0 11-6-6 4 4 0 006 6z"/></svg>
              自动沉淀
            </div>
            <div class="mc-auto-status">
              <span class="mc-status-dot" :class="autoDreamEnabled ? 'on' : 'off'" data-testid="memory-center-auto-dream-status"></span>
              {{ autoDreamStatusLabel }}
            </div>
            <div class="mc-auto-sub">对话结束后自动整理重要内容</div>
            <button class="mc-auto-toggle" :disabled="autoDreamToggling" data-testid="memory-center-auto-dream-toggle" @click="toggleAutoDream">
              {{ autoDreamEnabled ? '关闭' : '开启' }}
            </button>
            <div v-if="autoDreamPendingRestart" class="mc-auto-pending" data-testid="memory-center-auto-dream-pending">已保存切换，重启 agent-terminal 后生效</div>
          </div>
        </div>

        <div v-if="health && health.similarGroups && health.similarGroups.length" class="mc-similar-bar">
          <div class="mc-similar-head">
            <svg width="16" height="16" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5" style="flex-shrink:0">
              <path d="M8 1.5L14.5 13H1.5Z" stroke-linejoin="round"/><path d="M8 6v3" stroke-linecap="round"/><circle cx="8" cy="11.5" r="0.5" fill="currentColor"/>
            </svg>
            <span>{{ health.similarGroups.length }} 组条目内容相似</span>
            <button v-if="health.similarGroups.length > 1" class="mc-similar-btn" :disabled="mergingAll || mergingGroup !== null || ignoringGroup !== null" @click="mergeAllGroups">
              {{ mergingAll ? '整合中...' : '一键整合全部' }}
            </button>
            <button class="mc-similar-btn" @click="toggleSimilarExpand">
              {{ similarExpanded ? '收起' : '展开' }}
            </button>
          </div>
          <div v-if="similarExpanded" class="mc-similar-list">
            <div v-for="(group, gi) in health.similarGroups" :key="pairKey(group)" class="mc-similar-item">
              <span class="mc-similar-names">「{{ group.nameA }}」与「{{ group.nameB }}」</span>
              <span class="mc-similar-score">{{ formatScore(group.score) }}</span>
              <button class="btn btn-secondary btn-xs" :disabled="mergingGroup !== null || mergingAll || ignoringGroup !== null" @click="askMergeGroup(group, gi)">整合</button>
              <button class="btn btn-ghost btn-xs" style="opacity:0.5" :disabled="ignoringGroup !== null || mergingAll || mergingGroup !== null" @click="ignoreGroup(group)">{{ ignoringGroup === pairKey(group) ? '...' : '忽略' }}</button>
            </div>
          </div>
        </div>

        <div v-if="notice.message" class="mc-notice memory-notice-fade" :class="'is-' + notice.level" data-testid="memory-center-notice">{{ notice.message }}</div>
        <div v-if="isLoading" class="mc-notice is-info" data-testid="memory-center-loading">正在加载记忆中心...</div>
        <div v-if="model.error" class="mc-notice is-error" data-testid="memory-center-error">{{ model.error }}</div>

        <div class="mc-tabs">
          <div class="mc-tab" :class="{ active: activeTab === 'pref' }" @click="switchTab('pref')">
            <span class="mc-dot mc-dot-pref"></span>
            偏好 <span class="mc-tab-count">{{ preferenceEntries.length }}</span>
          </div>
          <div class="mc-tab" :class="{ active: activeTab === 'proj' }" @click="switchTab('proj')">
            <span class="mc-dot mc-dot-proj"></span>
            项目 <span class="mc-tab-count">{{ projectEntries.length }}</span>
          </div>
          <div class="mc-tab" :class="{ active: activeTab === 'all' }" @click="switchTab('all')">
            全部 <span class="mc-tab-count">{{ totalEntries }}</span>
          </div>
        </div>

        <div v-if="visibleEntries.length === 0" class="mc-empty">
          <svg class="mc-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
            <path d="M10 14h28v26H10z" opacity="0.35"/>
            <path d="M14 20h20M14 26h20M14 32h14" stroke-linecap="round" opacity="0.6"/>
            <circle cx="34" cy="14" r="5" fill="var(--surface)" stroke="currentColor"/>
            <path d="M32 14h4M34 12v4" stroke-linecap="round"/>
          </svg>
          <div class="mc-empty-title">{{ searchText ? '没有匹配的条目' : '暂无记忆' }}</div>
          <div v-if="!searchText" class="mc-empty-text">点击右上角“新建”按钮开始添加记忆。</div>
          <div v-if="searchText" class="mc-empty-actions">
            <button class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button>
          </div>
        </div>

        <div v-else class="mc-entry-grid">
          <article
            v-for="(entry, idx) in visibleEntries"
            :key="entry._target + ':' + (entry.path || entry.name || idx)"
            class="mc-entry-card"
            :class="entry.type === 'project' || entry.type === 'reference' ? 'type-proj' : 'type-pref'"
          >
            <div class="mc-entry-head">
<div class="mc-entry-title">{{ entry.title || headlineOf(entry.description) || entry.name || '未命名' }}</div>
              <div class="mc-entry-badges">
                <span class="jr-badge" :class="typeBadgeClass(entry.type)">{{ typeBadgeLabel(entry.type) }}</span>
                <span class="jr-badge jr-badge-scope">{{ entry._scope === 'team' ? '团队' : '私有' }}</span>
                <span v-if="entry.source === 'dream'" class="jr-badge jr-badge-dream" title="由自动沉淀生成">梦境</span>
              </div>
            </div>
            <div v-if="entry.description" class="mc-entry-desc">{{ entry.description }}</div>
            <pre class="mc-entry-preview" @click="$event.currentTarget.classList.toggle('is-expanded')">{{ entry.preview || '暂无预览' }}</pre>
            <div class="mc-entry-foot">
              <span class="mc-entry-time">{{ formatTimestamp(entry.updatedAt) }}</span>
              <div class="mc-entry-actions">
                <button class="btn btn-secondary btn-xs" :data-testid="'mc-entry-edit-' + idx" :disabled="busyPath === entry._target + ':' + entry.path" @click="memoryEditor.openEdit(entry._target, entry)">
                  {{ busyPath === entry._target + ':' + entry.path ? '加载中...' : '编辑' }}
                </button>
                <button class="btn btn-danger btn-xs" :data-testid="'mc-entry-delete-' + idx" @click="inlineDelete.ask(entry._target, entry)">删除</button>
              </div>
            </div>
          </article>
        </div>
      </div>

      <div v-if="inlineDelete.target" class="modal-overlay mc-modal-overlay" data-testid="memory-center-inline-delete-overlay" @click.self="inlineDelete.cancel">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-inline-delete-modal">
          <div class="mc-panel-head">
            <div>
              <div class="modal-title">删除记忆</div>
              <div class="mc-panel-tip">{{ inlineDelete.target.name }} · {{ inlineDelete.target.target }}</div>
            </div>
            <button class="btn btn-ghost" :disabled="inlineDelete.deleting" @click="inlineDelete.cancel">关闭</button>
          </div>
          <div class="mc-form-helper">删除后无法恢复。如果后续可能重用，建议先“编辑”备份内容。</div>
          <div class="mc-panel-actions">
            <button class="btn btn-ghost" :disabled="inlineDelete.deleting" @click="inlineDelete.cancel">取消</button>
            <button class="btn btn-danger" data-testid="memory-center-inline-delete-confirm" :disabled="inlineDelete.deleting" @click="inlineDelete.confirm">
              {{ inlineDelete.deleting ? '删除中...' : '确认删除' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="mergeConfirm.target" class="modal-overlay mc-modal-overlay" data-testid="memory-center-merge-overlay" @click.self="resetMergeConfirm">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-merge-modal">
          <div class="mc-panel-head">
            <div>
              <div class="modal-title">整合相似记忆</div>
              <div class="mc-panel-tip">相似度 {{ formatScore(mergeConfirm.target.score) }}</div>
            </div>
            <button class="btn btn-ghost" :disabled="mergingGroup !== null" @click="resetMergeConfirm">关闭</button>
          </div>
          <div class="mc-form-helper">
            <div>合并到：{{ mergeConfirm.target.nameA }} · {{ mergeConfirm.target.targetA }}（内容将合并）</div>
            <div>移除：{{ mergeConfirm.target.nameB }} · {{ mergeConfirm.target.targetB }}（合并后删除）</div>
            <div v-if="mergeConfirm.crossScope" class="mc-notice is-warning" style="margin-top:8px">跨作用域相似条目不会自动整合，请手动整理。</div>
          </div>
          <div class="mc-panel-actions">
            <button class="btn btn-ghost" :disabled="mergingGroup !== null" @click="resetMergeConfirm">取消</button>
            <button class="btn btn-primary" data-testid="memory-center-merge-confirm" :disabled="mergingGroup !== null || mergeConfirm.crossScope" @click="confirmMergeGroup">
              {{ mergingGroup !== null ? '整合中...' : '确认整合' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="memoryEditor.open" class="mc-panel-overlay" @click.self="memoryEditor.close"></div>
      <div class="mc-panel" :class="{ 'is-open': memoryEditor.open }" data-testid="memory-center-editor">
        <div class="mc-panel-head">
          <div>
            <div class="modal-title">{{ memoryEditor.mode === 'edit' ? '编辑记忆' : '新建记忆' }}</div>
            <div class="mc-panel-tip">{{ memoryEditor.form.target === 'team' ? '团队记忆' : '私有记忆' }}</div>
          </div>
          <button class="btn btn-ghost" data-testid="memory-center-editor-close" @click="memoryEditor.close">×</button>
        </div>
        <div class="mc-form-row">
          <label class="mc-form-label">目标</label>
          <select v-model="memoryEditor.form.target" class="modal-input" data-testid="memory-center-editor-target" :disabled="memoryIdentityLocked">
            <option value="private">私有</option><option value="team">团队</option>
          </select>
        </div>
        <div class="mc-form-row">
          <label class="mc-form-label">类型</label>
          <select v-model="memoryEditor.form.type" class="modal-input" data-testid="memory-center-editor-type" :disabled="memoryIdentityLocked">
            <option value="feedback">偏好</option><option value="project">项目</option>
          </select>
        </div>
        <div class="mc-form-row">
          <label class="mc-form-label">标识名</label>
          <input v-model="memoryEditor.form.name" class="modal-input" data-testid="memory-center-editor-name" :disabled="memoryIdentityLocked" placeholder="内部标识，如 reply-in-chinese" />
        </div>
        <div class="mc-form-row">
          <label class="mc-form-label">描述</label>
          <input v-model="memoryEditor.form.description" class="modal-input" data-testid="memory-center-editor-description" placeholder="一句话描述为什么值得长期保留" />
        </div>
        <div class="mc-form-row">
          <label class="mc-form-label">卡片标题</label>
          <input v-model="memoryEditor.form.title" class="modal-input" data-testid="memory-center-editor-title" placeholder="卡片上显示的短标题，留空则自动截取描述" />
        </div>
        <div v-if="memoryIdentityLocked" class="mc-form-helper">
现有记忆的标识名和类型暂时锁定；如需修改，请删除后重建。
        </div>
        <div class="mc-form-row">
          <label class="mc-form-label">内容</label>
          <textarea v-model="memoryEditor.form.content" rows="12" class="modal-input mc-form-textarea" data-testid="memory-center-editor-content"></textarea>
        </div>
        <div class="mc-form-helper">
          <button class="btn btn-secondary btn-xs" data-testid="memory-center-editor-template" @click="memoryEditor.fillTemplate">套用当前类型模板</button>
        </div>
        <div class="mc-panel-actions">
          <button class="btn btn-ghost" data-testid="memory-center-editor-cancel" @click="memoryEditor.close">取消</button>
          <button v-if="memoryEditor.form.existingPath" class="btn btn-danger" data-testid="memory-center-editor-delete" :disabled="memoryEditor.deleting" @click="askEditorDelete">
            删除
          </button>
          <button
            class="btn btn-primary"
            data-testid="memory-center-editor-save"
            :disabled="memoryEditor.saving || !memoryEditor.form.name.trim() || !memoryEditor.form.description.trim() || !memoryEditor.form.content.trim()"
            @click="memoryEditor.save"
          >{{ memoryEditor.saving ? '保存中...' : '保存' }}</button>
        </div>
      </div>
    </section>
  `,
};
