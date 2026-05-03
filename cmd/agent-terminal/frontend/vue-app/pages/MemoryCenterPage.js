import { computed, onBeforeUnmount, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import {
  useDurableMemoryEditor,
  useInlineDeleteConfirm,
} from '../composables/useMemoryEditors.js';

const GUIDE_PREF_KEY = 'memory-center.guide-collapsed';

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

function filterEntries(list, needle) {
  const term = (needle || '').toString().trim().toLowerCase();
  if (!term) return list;
  return list.filter((entry) => {
    const haystack = [
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

async function loadPreference(key, fallback) {
  try {
    const value = await callAPI('ui/preferences/get', { key });
    if (value === null || value === undefined) return fallback;
    return value;
  } catch {
    return fallback;
  }
}

async function savePreference(key, value) {
  try {
    await callAPI('ui/preferences/set', { key, value });
  } catch { /* non-critical */ }
}

export const MemoryCenterPage = {
  name: 'MemoryCenterPage',
  props: {
    model: { type: Object, required: true },
  },
  emits: ['refresh', 'open-shared-files'],
  setup(props, { emit }) {
    const notice = reactive({ level: 'info', message: '' });
    const busyPath = ref('');
    const searchText = ref('');
    const guideCollapsed = ref(false);
    const refreshing = ref(false);

    let noticeTimer = null;

    loadPreference(GUIDE_PREF_KEY, false).then((value) => {
      guideCollapsed.value = Boolean(value);
    });

    const overview = computed(() => ensureObject(props.model?.overview));
    const privateMemory = computed(() => ensureObject(props.model?.private));
    const teamMemory = computed(() => ensureObject(props.model?.team));
    const isLoading = computed(() => props.model?.loading === true);

    const currentCwd = computed(() => firstNonEmpty(overview.value.projectRoot));
    const systemDisabled = computed(() => overview.value.enabled === false);
    const autoDreamRuntimeEnabled = computed(() => overview.value.autoDreamEnabled === true);
    const autoDreamIntent = computed(() => {
      const v = overview.value.autoDreamIntent;
      return v === true ? true : v === false ? false : null;
    });
    const autoDreamEnabled = computed(() => (
      autoDreamIntent.value === null ? autoDreamRuntimeEnabled.value : autoDreamIntent.value
    ));
    const autoDreamStatusLabel = computed(() => (autoDreamEnabled.value ? '已开启' : '已关闭'));
    const autoDreamPendingRestart = computed(() => (
      autoDreamIntent.value !== null && autoDreamIntent.value !== autoDreamRuntimeEnabled.value
    ));
    const autoDreamToggling = ref(false);

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
    const mergeConfirm = reactive({ target: null, index: -1, crossScope: false });

    function resetMergeConfirm() {
      mergeConfirm.target = null;
      mergeConfirm.index = -1;
      mergeConfirm.crossScope = false;
    }

    function askMergeGroup(group, idx) {
      if (!group || mergingGroup.value !== null) return;
      mergeConfirm.target = group;
      mergeConfirm.index = idx;
      mergeConfirm.crossScope = (group.targetA || '') !== (group.targetB || '');
    }

    async function confirmMergeGroup() {
      const group = mergeConfirm.target;
      if (!group || mergingGroup.value !== null) return;
      if (mergeConfirm.crossScope) {
        setNotice('warning', '跨作用域相似条目不会自动整合，请手动整理私有/团队记忆');
        resetMergeConfirm();
        return;
      }
      mergingGroup.value = mergeConfirm.index;
      try {
        await callAPI('ui/memory/entry/merge', {
          cwd: currentCwd.value,
          targetA: group.targetA,
          pathA: group.pathA,
          targetB: group.targetB,
          pathB: group.pathB,
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

    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
      if (noticeTimer) { clearTimeout(noticeTimer); noticeTimer = null; }
      if (notice.message && level !== 'error') {
        noticeTimer = setTimeout(() => { notice.message = ''; }, 5200);
      }
    }

    function setBusy(path) { busyPath.value = path || ''; }

    async function toggleAutoDream() {
      if (autoDreamToggling.value) return;
      const next = !autoDreamEnabled.value;
      autoDreamToggling.value = true;
      try {
        await callAPI('ui/memory/auto-dream/set-intent', { enabled: next });
        setNotice('warning', `自动沉淀已切换为${next ? '开启' : '关闭'} — 重启 agent-terminal 后生效`);
        emit('refresh');
      } catch (error) {
        setNotice('error', `切换自动沉淀失败：${(error && error.message) || String(error || '')}`);
      } finally {
        autoDreamToggling.value = false;
      }
    }

    const memoryEditor = useDurableMemoryEditor({ currentCwd, setNotice, setBusy, emit });
    const inlineDelete = useInlineDeleteConfirm({ currentCwd, setNotice, emit });

    const memoryIdentityLocked = computed(
      () => memoryEditor.mode === 'edit' && Boolean(memoryEditor.form.existingPath),
    );

    function clearSearch() { searchText.value = ''; }

    function toggleGuide() {
      guideCollapsed.value = !guideCollapsed.value;
      savePreference(GUIDE_PREF_KEY, guideCollapsed.value);
    }

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
      filteredPreferenceEntries,
      filteredProjectEntries,
      totalEntries,
      searchText,
      guideCollapsed,
      refreshing,
      systemDisabled,
      autoDreamEnabled,
      autoDreamStatusLabel,
      autoDreamPendingRestart,
      autoDreamToggling,
      toggleAutoDream,
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
      formatTimestamp,
      statusLabel,
      typeBadgeClass,
      typeBadgeLabel,
      clearSearch,
      toggleGuide,
      handleRefresh,
      createPreference,
      createProject,
      askEditorDelete,
      openSharedFiles: () => emit('open-shared-files'),
    };
  },
  template: `
    <section id="page-memory-center" class="page active memory-center-page" data-testid="memory-center-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>记忆中心</h2></div>
        <div class="memory-center-toolbar" data-testid="memory-center-toolbar">
          <div class="memory-center-search">
            <span class="memory-center-search-icon" aria-hidden="true">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.5">
                <circle cx="7" cy="7" r="4.5"/>
                <path d="M10.5 10.5l3 3" stroke-linecap="round"/>
              </svg>
            </span>
            <input
              v-model="searchText"
              class="memory-center-search-input"
              data-testid="memory-center-search"
              placeholder="搜索 name / description / path"
            />
            <button
              v-if="searchText"
              class="memory-center-search-clear"
              data-testid="memory-center-search-clear"
              aria-label="清除"
              @click="clearSearch"
            >×</button>
          </div>
          <button class="btn btn-secondary btn-toolbar-sm" data-testid="memory-center-open-shared-files" @click="openSharedFiles">查看共享文件</button>
          <button class="btn btn-primary btn-toolbar-sm" data-testid="memory-center-refresh" :disabled="refreshing" @click="handleRefresh">
            <span v-if="refreshing" class="memory-refresh-spin" aria-hidden="true"></span>
            {{ refreshing ? '刷新中' : '刷新' }}
          </button>
        </div>
      </div>

      <div class="panel-body memory-center-body memory-center-body-has-toolbar" data-testid="memory-center-body">
        <div class="data-card-vue memory-center-callout" :class="{ 'is-collapsed': guideCollapsed }" data-testid="memory-center-callout">
          <div class="memory-center-callout-head">
            <div>
              <div class="memory-center-callout-title">
                长期记忆
                <span class="jr-badge jr-badge-default">{{ totalEntries }} 条</span>
                <span
                  class="jr-badge"
                  :class="systemDisabled ? 'jr-badge-error' : 'jr-badge-success'"
                  data-testid="memory-center-system-status"
                >记忆系统 · {{ systemDisabled ? '未启用' : '已启用' }}</span>
              </div>
              <div v-if="!guideCollapsed" class="memory-center-callout-subtitle">
                仅保存值得跨会话复用的稳定内容；临时草稿请放到“共享文件”。
              </div>
            </div>
            <div class="memory-center-callout-actions">
              <button class="btn btn-ghost btn-toolbar-sm" data-testid="memory-center-guide-toggle" @click="toggleGuide">
                {{ guideCollapsed ? '展开指引' : '收起指引' }}
              </button>
            </div>
          </div>
          <div v-if="!guideCollapsed" class="memory-center-callout-body">
            <div class="memory-center-guide-grid">
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">偏好记忆</div>
                <div class="memory-center-guide-text">保存个人习惯、行为规则等跨会话复用的偏好设定。</div>
              </article>
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">项目记忆</div>
                <div class="memory-center-guide-text">保存项目上下文、架构决策等长期参考信息。</div>
              </article>
            </div>
          </div>
        </div>

        <div class="data-card-vue memory-center-auto-card" data-testid="memory-center-auto-dream-card">
          <div class="memory-center-auto-card-head">
            <div class="memory-center-auto-title">自动沉淀</div>
            <div class="memory-center-auto-card-actions">
              <span
                class="jr-badge"
                :class="autoDreamEnabled ? 'jr-badge-success' : 'jr-badge-default'"
                data-testid="memory-center-auto-dream-status"
              >{{ autoDreamStatusLabel }}</span>
              <button
                type="button"
                class="memory-center-auto-toggle"
                :disabled="autoDreamToggling"
                @click="toggleAutoDream"
                data-testid="memory-center-auto-dream-toggle"
              >{{ autoDreamEnabled ? '关闭' : '开启' }}</button>
            </div>
          </div>
          <div class="memory-center-auto-text">对话结束后，帮你把重要内容整理进长期记忆</div>
          <div
            v-if="autoDreamPendingRestart"
            class="memory-center-auto-pending"
            data-testid="memory-center-auto-dream-pending"
          >已保存切换，重启 agent-terminal 后生效</div>
        </div>

        <div v-if="health" class="data-card-vue memory-health-card" data-testid="memory-center-health-card">
          <div class="memory-health-head">
            <div class="memory-health-title">记忆健康</div>
          </div>
          <div class="memory-health-bars">
            <div class="memory-health-row">
              <span class="memory-health-label">偏好</span>
              <div class="memory-health-track">
                <div
                  class="memory-health-fill"
                  :class="healthBarClass(healthPrefPercent)"
                  :style="{ width: healthPrefPercent + '%' }"
                ></div>
              </div>
              <span class="memory-health-count">{{ health.preferenceCount }} / {{ health.maxPerCategory }}</span>
            </div>
            <div class="memory-health-row">
              <span class="memory-health-label">项目</span>
              <div class="memory-health-track">
                <div
                  class="memory-health-fill"
                  :class="healthBarClass(healthProjPercent)"
                  :style="{ width: healthProjPercent + '%' }"
                ></div>
              </div>
              <span class="memory-health-count">{{ health.projectCount }} / {{ health.maxPerCategory }}</span>
            </div>
          </div>
          <div v-if="health.similarGroups && health.similarGroups.length" class="memory-health-similar">
            <div class="memory-health-similar-head">
              <svg width="14" height="14" viewBox="0 0 16 16" fill="none" stroke="var(--warning, #d99a3a)" stroke-width="1.5" style="flex-shrink:0;margin-top:1px;">
                <path d="M8 1.5L14.5 13H1.5Z" stroke-linejoin="round"/>
                <path d="M8 6v3" stroke-linecap="round"/>
                <circle cx="8" cy="11.5" r="0.5" fill="var(--warning, #d99a3a)"/>
              </svg>
              <span>{{ health.similarGroups.length }} 组条目内容相似，建议整理</span>
            </div>
            <div class="memory-health-similar-list">
              <div v-for="(group, gi) in health.similarGroups" :key="gi" class="memory-health-similar-item">
                <span class="memory-health-similar-names">「{{ group.nameA }}」与「{{ group.nameB }}」</span>
                <span class="memory-health-similar-score">{{ formatScore(group.score) }}</span>
                <button
                  class="btn btn-secondary btn-xs memory-health-merge-btn"
                  :disabled="mergingGroup !== null"
                  @click="askMergeGroup(group, gi)"
                >{{ mergingGroup === gi ? '整合中...' : '整合' }}</button>
              </div>
            </div>
          </div>
        </div>

        <div v-if="notice.message" class="settings-prompt-notice memory-notice-fade" :class="'is-' + notice.level" data-testid="memory-center-notice">{{ notice.message }}</div>
        <div v-if="isLoading" class="settings-prompt-notice is-info" data-testid="memory-center-loading">正在加载记忆中心...</div>
        <div v-if="model.error" class="settings-prompt-notice is-error" data-testid="memory-center-error">{{ model.error }}</div>

        <div class="memory-center-section memory-center-section--preference">
          <span class="memory-center-section-title">偏好</span>
          <span class="memory-center-section-subtitle" style="font-size:0.8em;opacity:0.7;margin-left:0.5em;">个人习惯 · 行为规则</span>
          <span class="memory-center-section-count">{{ filteredPreferenceEntries.length }}/{{ preferenceEntries.length }}</span>
          <div class="memory-center-section-tail">
            <button class="btn btn-primary btn-xs" data-testid="memory-center-preference-create" @click="createPreference">新建</button>
          </div>
        </div>
        <div v-if="preferenceEntries.length === 0" class="memory-empty memory-scope-empty" data-testid="memory-center-preference-empty">
          <svg class="memory-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
            <path d="M10 14h28v26H10z" opacity="0.35"/>
            <path d="M14 20h20M14 26h20M14 32h14" stroke-linecap="round" opacity="0.6"/>
            <circle cx="34" cy="14" r="5" fill="var(--surface)" stroke="currentColor"/>
            <path d="M32 14h4M34 12v4" stroke-linecap="round"/>
          </svg>
          <div class="memory-empty-title">暂无偏好记忆</div>
          <div class="memory-empty-text">保存个人习惯和行为规则到这里，下次会话会自动带上。点击“新建”开始。</div>
        </div>
        <div v-else-if="filteredPreferenceEntries.length === 0" class="memory-empty" data-testid="memory-center-preference-filter-empty">
          <div class="memory-empty-title">当前搜索没有匹配的偏好条目</div>
          <div class="memory-empty-actions">
            <button class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button>
          </div>
        </div>
        <div v-else class="memory-entry-grid" data-testid="memory-center-preference-list">
          <article v-for="(entry, idx) in filteredPreferenceEntries" :key="entry.path || entry.name || idx" class="data-card-vue memory-entry-card">
            <div class="memory-entry-head">
              <div class="memory-entry-title-row">
                <div class="memory-entry-title" :title="entry.name || '未命名条目'">{{ entry.name || '未命名条目' }}</div>
                <span class="jr-badge" :class="typeBadgeClass(entry.type)">{{ typeBadgeLabel(entry.type) }}</span>
                <span class="jr-badge jr-badge-scope">{{ entry._scope === 'team' ? '团队' : '私有' }}</span>
                <span v-if="entry.source === 'dream'" class="jr-badge jr-badge-dream" title="由自动沉淀生成">梦境</span>
              </div>
              <div class="memory-entry-actions">
                <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-preference-edit-' + idx" :disabled="busyPath === entry._target + ':' + entry.path" @click="memoryEditor.openEdit(entry._target, entry)">
                  {{ busyPath === entry._target + ':' + entry.path ? '加载中...' : '编辑' }}
                </button>
                <button class="btn btn-danger btn-xs" :data-testid="'memory-center-preference-delete-' + idx" @click="inlineDelete.ask(entry._target, entry)">删除</button>
              </div>
            </div>
            <div class="memory-entry-sub">
              <span v-if="entry.description" class="memory-entry-description">{{ entry.description }}</span>
              <span class="memory-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</span>
            </div>
            <pre class="memory-entry-preview" @click="$event.currentTarget.classList.toggle('is-expanded')">{{ entry.preview || '暂无预览' }}</pre>
          </article>
        </div>

        <div class="memory-center-section memory-center-section--project">
          <span class="memory-center-section-title">项目</span>
          <span class="memory-center-section-subtitle" style="font-size:0.8em;opacity:0.7;margin-left:0.5em;">项目上下文 · 架构决策</span>
          <span class="memory-center-section-count">{{ filteredProjectEntries.length }}/{{ projectEntries.length }}</span>
          <div class="memory-center-section-tail">
            <button class="btn btn-primary btn-xs" data-testid="memory-center-project-create" @click="createProject">新建</button>
          </div>
        </div>
        <div v-if="projectEntries.length === 0" class="memory-empty memory-scope-empty" data-testid="memory-center-project-empty">
          <svg class="memory-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
            <path d="M10 14h28v26H10z" opacity="0.35"/>
            <path d="M14 20h20M14 26h20M14 32h14" stroke-linecap="round" opacity="0.6"/>
            <circle cx="34" cy="14" r="5" fill="var(--surface)" stroke="currentColor"/>
            <path d="M32 14h4M34 12v4" stroke-linecap="round"/>
          </svg>
          <div class="memory-empty-title">暂无项目记忆</div>
          <div class="memory-empty-text">保存项目上下文和架构决策到这里，下次会话会自动带上。点击“新建”开始。</div>
        </div>
        <div v-else-if="filteredProjectEntries.length === 0" class="memory-empty" data-testid="memory-center-project-filter-empty">
          <div class="memory-empty-title">当前搜索没有匹配的项目条目</div>
          <div class="memory-empty-actions"><button class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button></div>
        </div>
        <div v-else class="memory-entry-grid" data-testid="memory-center-project-list">
          <article v-for="(entry, idx) in filteredProjectEntries" :key="entry.path || entry.name || idx" class="data-card-vue memory-entry-card">
            <div class="memory-entry-head">
              <div class="memory-entry-title-row">
                <div class="memory-entry-title" :title="entry.name || '未命名条目'">{{ entry.name || '未命名条目' }}</div>
                <span class="jr-badge" :class="typeBadgeClass(entry.type)">{{ typeBadgeLabel(entry.type) }}</span>
                <span class="jr-badge jr-badge-scope">{{ entry._scope === 'team' ? '团队' : '私有' }}</span>
                <span v-if="entry.source === 'dream'" class="jr-badge jr-badge-dream" title="由自动沉淀生成">梦境</span>
              </div>
              <div class="memory-entry-actions">
                <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-project-edit-' + idx" :disabled="busyPath === entry._target + ':' + entry.path" @click="memoryEditor.openEdit(entry._target, entry)">
                  {{ busyPath === entry._target + ':' + entry.path ? '加载中...' : '编辑' }}
                </button>
                <button class="btn btn-danger btn-xs" :data-testid="'memory-center-project-delete-' + idx" @click="inlineDelete.ask(entry._target, entry)">删除</button>
              </div>
            </div>
            <div class="memory-entry-sub">
              <span v-if="entry.description" class="memory-entry-description">{{ entry.description }}</span>
              <span class="memory-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</span>
            </div>
            <pre class="memory-entry-preview" @click="$event.currentTarget.classList.toggle('is-expanded')">{{ entry.preview || '暂无预览' }}</pre>
          </article>
        </div>
      </div>

      <div v-if="inlineDelete.target" class="modal-overlay" data-testid="memory-center-inline-delete-overlay" @click.self="inlineDelete.cancel">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-inline-delete-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">删除记忆</div>
              <div class="memory-modal-tip">{{ inlineDelete.target.name }} · {{ inlineDelete.target.target }}</div>
            </div>
            <button class="btn btn-ghost" :disabled="inlineDelete.deleting" @click="inlineDelete.cancel">关闭</button>
          </div>
          <div class="memory-form-helper">删除后无法恢复。如果后续可能重用，建议先“编辑”备份内容。</div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" :disabled="inlineDelete.deleting" @click="inlineDelete.cancel">取消</button>
            <button class="btn btn-danger" data-testid="memory-center-inline-delete-confirm" :disabled="inlineDelete.deleting" @click="inlineDelete.confirm">
              {{ inlineDelete.deleting ? '删除中...' : '确认删除' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="mergeConfirm.target" class="modal-overlay" data-testid="memory-center-merge-overlay" @click.self="resetMergeConfirm">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-merge-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">整合相似记忆</div>
              <div class="memory-modal-tip">相似度 {{ formatScore(mergeConfirm.target.score) }}</div>
            </div>
            <button class="btn btn-ghost" :disabled="mergingGroup !== null" @click="resetMergeConfirm">关闭</button>
          </div>
          <div class="memory-form-helper">
            <div>保留：{{ mergeConfirm.target.nameA }} · {{ mergeConfirm.target.targetA }} · {{ mergeConfirm.target.pathA }}</div>
            <div>删除：{{ mergeConfirm.target.nameB }} · {{ mergeConfirm.target.targetB }} · {{ mergeConfirm.target.pathB }}</div>
            <div v-if="mergeConfirm.crossScope" class="settings-prompt-notice is-warning">跨作用域相似条目不会自动整合，请手动整理。</div>
          </div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" :disabled="mergingGroup !== null" @click="resetMergeConfirm">取消</button>
            <button
              class="btn btn-primary"
              data-testid="memory-center-merge-confirm"
              :disabled="mergingGroup !== null || mergeConfirm.crossScope"
              @click="confirmMergeGroup"
            >{{ mergingGroup !== null ? '整合中...' : '确认整合' }}</button>
          </div>
        </div>
      </div>

      <div v-if="memoryEditor.open" class="modal-overlay" data-testid="memory-center-editor-overlay" @click.self="memoryEditor.close">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-editor">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">{{ memoryEditor.mode === 'edit' ? '编辑记忆' : '新建记忆' }}</div>
              <div class="memory-modal-tip">{{ memoryEditor.form.target === 'team' ? '团队记忆' : '私有记忆' }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="memory-center-editor-close" @click="memoryEditor.close">关闭</button>
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">目标</label>
              <select v-model="memoryEditor.form.target" class="modal-input" data-testid="memory-center-editor-target" :disabled="memoryIdentityLocked">
                <option value="private">私有</option>
                <option value="team">团队</option>
              </select>
            </div>
            <div class="modal-input-flex">
              <label class="settings-inline-label">类型</label>
              <select v-model="memoryEditor.form.type" class="modal-input" data-testid="memory-center-editor-type" :disabled="memoryIdentityLocked">
                <option value="feedback">偏好</option>
                <option value="project">项目</option>
              </select>
            </div>
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">名称</label>
              <input v-model="memoryEditor.form.name" class="modal-input" data-testid="memory-center-editor-name" :disabled="memoryIdentityLocked" placeholder="例如：Core dashboard owner" />
            </div>
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">描述</label>
              <input v-model="memoryEditor.form.description" class="modal-input" data-testid="memory-center-editor-description" placeholder="一句话描述为什么值得长期保留" />
            </div>
          </div>
          <div class="memory-form-helper" v-if="memoryIdentityLocked">
            现有记忆的名称和类型暂时锁定；如需改名或改类型，请删除后重建。
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">内容</label>
              <textarea v-model="memoryEditor.form.content" rows="12" class="modal-input" data-testid="memory-center-editor-content"></textarea>
            </div>
          </div>
          <div class="memory-form-helper">
            <button class="btn btn-secondary btn-xs" data-testid="memory-center-editor-template" @click="memoryEditor.fillTemplate">套用当前类型模板</button>
          </div>
          <div class="memory-editor-actions">
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
      </div>
    </section>
  `,
};
