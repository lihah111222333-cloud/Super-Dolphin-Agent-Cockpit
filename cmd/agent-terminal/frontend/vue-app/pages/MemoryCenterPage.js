import { computed, onBeforeUnmount, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import {
  useDurableMemoryEditor,
  useAgentMemoryEditor,
  useInlineDeleteConfirm,
} from '../composables/useMemoryEditors.js';

const GUIDE_PREF_KEY = 'memory-center.guide-collapsed';

const TYPE_BADGE_CLASS = Object.freeze({
  project: 'jr-badge-primary',
  feedback: 'jr-badge-warning',
  reference: 'jr-badge-success',
  user: 'jr-badge-default',
});

const SCOPE_BADGE_CLASS = Object.freeze({
  project: 'jr-badge-primary',
  user: 'jr-badge-default',
  local: 'jr-badge-warning',
});

function ensureArray(value) {
  return Array.isArray(value) ? value : [];
}

function ensureObject(value) {
  return value && typeof value === 'object' ? value : {};
}

function formatTimestamp(value) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function countLabel(items, singular) {
  const count = ensureArray(items).length;
  return `${count} ${singular}`;
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

function scopeBadgeClass(scope) {
  return SCOPE_BADGE_CLASS[(scope || '').toString()] || 'jr-badge-default';
}

function scopeTitle(scope) {
  switch ((scope || '').toString()) {
    case 'user':
      return 'User scope';
    case 'local':
      return 'Local scope';
    default:
      return 'Project scope';
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

function filterAgentScopes(scopes, needle) {
  const term = (needle || '').toString().trim().toLowerCase();
  if (!term) return scopes;
  return scopes.map((scope) => {
    const entries = ensureArray(scope?.entries).filter((entry) => {
      const hay = [entry?.agentType, entry?.path, entry?.preview]
        .map((v) => (v || '').toString().toLowerCase()).join(' \u0001 ');
      return hay.includes(term);
    });
    return { ...scope, entries };
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
    const expandedEmptyScopes = reactive({});

    let noticeTimer = null;

    loadPreference(GUIDE_PREF_KEY, false).then((value) => {
      guideCollapsed.value = Boolean(value);
    });

    const overview = computed(() => ensureObject(props.model?.overview));
    const privateMemory = computed(() => ensureObject(props.model?.private));
    const teamMemory = computed(() => ensureObject(props.model?.team));
    const agentScopes = computed(() => ensureArray(props.model?.agentScopes));
    const currentCwd = computed(() => firstNonEmpty(overview.value.projectRoot));
    const systemDisabled = computed(() => overview.value.enabled === false);
    const showAllScopes = ref(false);

    const privateEntries = computed(() => ensureArray(privateMemory.value?.entries));
    const teamEntries = computed(() => ensureArray(teamMemory.value?.entries));
    const filteredPrivateEntries = computed(() => filterEntries(privateEntries.value, searchText.value));
    const filteredTeamEntries = computed(() => filterEntries(teamEntries.value, searchText.value));
    const filteredAgentScopes = computed(() => filterAgentScopes(agentScopes.value, searchText.value));
    const totalEntries = computed(() => privateEntries.value.length + teamEntries.value.length);
    const nonEmptyAgentScopes = computed(
      () => filteredAgentScopes.value.filter((scope) => ensureArray(scope?.entries).length > 0),
    );
    const hiddenEmptyScopeCount = computed(
      () => filteredAgentScopes.value.length - nonEmptyAgentScopes.value.length,
    );
    const visibleAgentScopes = computed(() => (
      showAllScopes.value ? filteredAgentScopes.value : nonEmptyAgentScopes.value
    ));

    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
      if (noticeTimer) { clearTimeout(noticeTimer); noticeTimer = null; }
      if (notice.message && level !== 'error') {
        noticeTimer = setTimeout(() => { notice.message = ''; }, 5200);
      }
    }

    function setBusy(path) { busyPath.value = path || ''; }

    const memoryEditor = useDurableMemoryEditor({ currentCwd, setNotice, setBusy, emit });
    const agentEditor = useAgentMemoryEditor({ currentCwd, setNotice, setBusy, emit });
    const inlineDelete = useInlineDeleteConfirm({ currentCwd, setNotice, emit });

    const memoryIdentityLocked = computed(
      // memoryEditor is a reactive() wrapper — property access auto-unwraps the
      // mode ref, so .mode gives the current value directly (no .value needed).
      () => memoryEditor.mode === 'edit' && Boolean(memoryEditor.form.existingPath),
    );

    function clearSearch() { searchText.value = ''; }
    function toggleAllScopes() { showAllScopes.value = !showAllScopes.value; }

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

    function toggleEmptyScope(scope) {
      const key = (scope || '').toString();
      expandedEmptyScopes[key] = !expandedEmptyScopes[key];
    }

    function isScopeExpanded(scope) {
      return Boolean(expandedEmptyScopes[(scope || '').toString()]);
    }

    watch(() => props.model, () => { refreshing.value = false; });
    onBeforeUnmount(() => { if (noticeTimer) clearTimeout(noticeTimer); });

    return {
      notice,
      busyPath,
      overview,
      privateMemory,
      teamMemory,
      agentScopes,
      currentCwd,
      memoryEditor,
      agentEditor,
      inlineDelete,
      memoryIdentityLocked,
      privateEntries,
      teamEntries,
      filteredPrivateEntries,
      filteredTeamEntries,
      filteredAgentScopes,
      totalEntries,
      searchText,
      guideCollapsed,
      refreshing,
      systemDisabled,
      showAllScopes,
      visibleAgentScopes,
      hiddenEmptyScopeCount,
      toggleAllScopes,
      formatTimestamp,
      countLabel,
      statusLabel,
      scopeTitle,
      typeBadgeClass,
      scopeBadgeClass,
      clearSearch,
      toggleGuide,
      handleRefresh,
      toggleEmptyScope,
      isScopeExpanded,
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
                长期记忆与 Agent 专属记忆
                <span class="jr-badge jr-badge-default">{{ totalEntries }} 条</span>
                <span
                  class="jr-badge"
                  :class="systemDisabled ? 'jr-badge-error' : 'jr-badge-success'"
                  data-testid="memory-center-system-status"
                >Memory 系统 · {{ systemDisabled ? '未启用' : '已启用' }}</span>
              </div>
              <div v-if="!guideCollapsed" class="memory-center-callout-subtitle">
                仅保存值得跨会话复用的稳定内容；临时草稿请放到"共享文件"。
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
                <div class="memory-center-guide-title">长期记忆</div>
                <div class="memory-center-guide-text">保存稳定事实、偏好、项目长期上下文。真正写盘的是 topic files，<code>MEMORY.md</code> 只是索引入口。</div>
              </article>
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">Agent 记忆</div>
                <div class="memory-center-guide-text">只在子 Agent 启动时注入，适合保存某类 Agent 的工作习惯与专属上下文，不会自动回流到项目长期记忆。</div>
              </article>
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">推荐用法</div>
                <div class="memory-center-guide-text">临时协作内容先放共享文件；确认值得长期保留后，再整理成 durable memory，避免把计划、过程状态和噪音写进长期记忆。</div>
              </article>
            </div>
          </div>
        </div>

        <div v-if="notice.message" class="settings-prompt-notice memory-notice-fade" :class="'is-' + notice.level" data-testid="memory-center-notice">{{ notice.message }}</div>
        <div v-if="model.error" class="settings-prompt-notice is-error" data-testid="memory-center-error">{{ model.error }}</div>



        <div class="memory-center-section memory-center-section--private">
          <span class="memory-center-section-title">Private 长期记忆</span>
          <span class="memory-center-section-count">{{ filteredPrivateEntries.length }}/{{ privateEntries.length }}</span>
          <div class="memory-center-section-tail">
            <button class="btn btn-primary btn-xs" data-testid="memory-center-private-create" @click="memoryEditor.openCreate('private')">新建</button>
          </div>
        </div>
        <div v-if="privateMemory.rootPath || privateMemory.notice" class="memory-scope-summary" data-testid="memory-center-private-summary">
          <span v-if="privateMemory.rootPath" class="memory-scope-summary-path" :title="privateMemory.rootPath">{{ privateMemory.rootPath }}</span>
          <span v-if="privateMemory.notice" class="memory-scope-note">{{ privateMemory.notice }}</span>
        </div>
        <div v-if="privateEntries.length === 0" class="memory-empty memory-scope-empty" data-testid="memory-center-private-empty">
          <svg class="memory-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
            <path d="M10 14h28v26H10z" opacity="0.35"/>
            <path d="M14 20h20M14 26h20M14 32h14" stroke-linecap="round" opacity="0.6"/>
            <circle cx="34" cy="14" r="5" fill="var(--surface)" stroke="currentColor"/>
            <path d="M32 14h4M34 12v4" stroke-linecap="round"/>
          </svg>
          <div class="memory-empty-title">暂无 Private durable memory</div>
          <div class="memory-empty-text">保存稳定的项目事实或偏好到这里，下次会话会自动带上。点击右上角"新建 durable memory"开始。</div>
        </div>
        <div v-else-if="filteredPrivateEntries.length === 0" class="memory-empty" data-testid="memory-center-private-filter-empty">
          <div class="memory-empty-title">当前搜索没有匹配的 Private 条目</div>
          <div class="memory-empty-actions">
            <button class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button>
          </div>
        </div>
        <div v-else class="memory-entry-grid" data-testid="memory-center-private-list">
          <article v-for="(entry, idx) in filteredPrivateEntries" :key="entry.path || entry.name || idx" class="data-card-vue memory-entry-card">
            <div class="memory-entry-head">
              <div class="memory-entry-title-row">
                <div class="memory-entry-title" :title="entry.name || '未命名条目'">{{ entry.name || '未命名条目' }}</div>
                <span class="jr-badge" :class="typeBadgeClass(entry.type)">{{ entry.type || 'unknown' }}</span>
              </div>
              <div class="memory-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</div>
            </div>
            <div class="memory-entry-meta">
              <span class="memory-entry-meta-path" :title="entry.path">{{ entry.path || '-' }}</span>
            </div>
            <div v-if="entry.description" class="memory-entry-description">{{ entry.description }}</div>
            <pre class="memory-entry-preview">{{ entry.preview || '暂无预览' }}</pre>
            <div class="memory-card-actions">
              <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-private-edit-' + idx" :disabled="busyPath === 'private:' + entry.path" @click="memoryEditor.openEdit('private', entry)">
                {{ busyPath === 'private:' + entry.path ? '加载中...' : '编辑' }}
              </button>
              <button class="btn btn-danger btn-xs" :data-testid="'memory-center-private-delete-' + idx" @click="inlineDelete.ask('private', entry)">删除</button>
            </div>
          </article>
        </div>

        <div class="memory-center-section memory-center-section--team">
          <span class="memory-center-section-title">Team 长期记忆</span>
          <span class="memory-center-section-count">{{ filteredTeamEntries.length }}/{{ teamEntries.length }}</span>
          <div class="memory-center-section-tail">
            <button class="btn btn-primary btn-xs" data-testid="memory-center-team-create" :disabled="!teamMemory.rootPath" @click="memoryEditor.openCreate('team')">新建</button>
          </div>
        </div>
        <div v-if="teamMemory.rootPath || teamMemory.notice" class="memory-scope-summary" data-testid="memory-center-team-summary">
          <span v-if="teamMemory.rootPath" class="memory-scope-summary-path" :title="teamMemory.rootPath">{{ teamMemory.rootPath }}</span>
          <span v-if="teamMemory.notice" class="memory-scope-note">{{ teamMemory.notice }}</span>
        </div>
        <div v-if="teamEntries.length === 0" class="memory-empty memory-scope-empty" data-testid="memory-center-team-empty">
          <svg class="memory-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
            <circle cx="18" cy="22" r="6"/>
            <circle cx="32" cy="22" r="6"/>
            <path d="M8 40c0-6 6-10 10-10s10 4 10 10" opacity="0.6"/>
            <path d="M22 40c0-6 6-10 10-10s10 4 10 10" opacity="0.6"/>
          </svg>
          <div class="memory-empty-title">暂无 Team durable memory</div>
          <div class="memory-empty-text">Team memory 跨项目共享，适合保存团队级约定、代码规范、共享链接等。</div>
        </div>
        <div v-else-if="filteredTeamEntries.length === 0" class="memory-empty" data-testid="memory-center-team-filter-empty">
          <div class="memory-empty-title">当前搜索没有匹配的 Team 条目</div>
          <div class="memory-empty-actions"><button class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button></div>
        </div>
        <div v-else class="memory-entry-grid" data-testid="memory-center-team-list">
          <article v-for="(entry, idx) in filteredTeamEntries" :key="entry.path || entry.name || idx" class="data-card-vue memory-entry-card">
            <div class="memory-entry-head">
              <div class="memory-entry-title-row">
                <div class="memory-entry-title" :title="entry.name || '未命名条目'">{{ entry.name || '未命名条目' }}</div>
                <span class="jr-badge" :class="typeBadgeClass(entry.type)">{{ entry.type || 'unknown' }}</span>
              </div>
              <div class="memory-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</div>
            </div>
            <div class="memory-entry-meta">
              <span class="memory-entry-meta-path" :title="entry.path">{{ entry.path || '-' }}</span>
            </div>
            <div v-if="entry.description" class="memory-entry-description">{{ entry.description }}</div>
            <pre class="memory-entry-preview">{{ entry.preview || '暂无预览' }}</pre>
            <div class="memory-card-actions">
              <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-team-edit-' + idx" :disabled="busyPath === 'team:' + entry.path" @click="memoryEditor.openEdit('team', entry)">
                {{ busyPath === 'team:' + entry.path ? '加载中...' : '编辑' }}
              </button>
              <button class="btn btn-danger btn-xs" :data-testid="'memory-center-team-delete-' + idx" @click="inlineDelete.ask('team', entry)">删除</button>
            </div>
          </article>
        </div>

        <div class="memory-center-section memory-center-section--agent">
          <span class="memory-center-section-title">Agent 记忆</span>
          <span class="memory-center-section-count">{{ visibleAgentScopes.length }}/{{ agentScopes.length }} scope</span>
          <div v-if="hiddenEmptyScopeCount > 0" class="memory-center-section-tail">
            <button class="btn btn-ghost btn-toolbar-sm" data-testid="memory-center-agent-show-all" @click="toggleAllScopes">
              {{ showAllScopes ? '隐藏空 scope' : '显示空 scope (' + hiddenEmptyScopeCount + ')' }}
            </button>
          </div>
        </div>
        <div class="memory-agent-scope-list" data-testid="memory-center-agent-scopes">
          <article v-for="scope in visibleAgentScopes" :key="scope.scope" class="data-card-vue memory-agent-scope-card">
            <div class="memory-agent-scope-head">
              <div>
                <div class="memory-agent-scope-title">
                  {{ scopeTitle(scope.scope) }}
                  <span class="jr-badge" :class="scopeBadgeClass(scope.scope)">{{ scope.scope }}</span>
                </div>
                <div class="memory-agent-scope-count">{{ countLabel(scope.entries, 'agents') }}</div>
              </div>
              <button class="btn btn-primary btn-xs" :data-testid="'memory-center-agent-create-' + scope.scope" @click="agentEditor.openCreate(scope.scope)">新建 / 编辑</button>
            </div>
            <div class="data-row-vue"><strong>目录</strong><span>{{ scope.rootPath || '-' }}</span></div>
            <div v-if="scope.notice" class="memory-scope-note">{{ scope.notice }}</div>
            <template v-if="!scope.entries || scope.entries.length === 0">
              <div class="memory-agent-empty">
                当前 scope 下还没有可读的 <code>MEMORY.md</code>。
                <button class="memory-agent-collapsed-toggle" :data-testid="'memory-center-agent-expand-' + scope.scope" @click="toggleEmptyScope(scope.scope)">
                  {{ isScopeExpanded(scope.scope) ? '收起' : '展开帮助' }}
                </button>
              </div>
              <div v-if="isScopeExpanded(scope.scope)" class="memory-center-guide-text">
                在上方点击"新建 / 编辑"可以为 {{ scopeTitle(scope.scope) }} 的某个 Agent Type 创建 MEMORY.md，例如 Writer / Reviewer 等。
              </div>
            </template>
            <div v-else class="memory-agent-entry-list">
              <article v-for="entry in scope.entries" :key="scope.scope + ':' + entry.agentType" class="memory-agent-entry">
                <div class="memory-agent-entry-head">
                  <div class="memory-agent-entry-title">{{ entry.agentType }}</div>
                  <div class="memory-agent-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</div>
                </div>
                <div class="memory-agent-entry-path" :title="entry.path">{{ entry.path || '-' }}</div>
                <pre class="memory-entry-preview">{{ entry.preview || 'MEMORY.md 为空' }}</pre>
                <div class="memory-card-actions">
                  <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-agent-edit-' + scope.scope + '-' + entry.agentType" :disabled="busyPath === 'agent:' + scope.scope + ':' + entry.agentType" @click="agentEditor.openEdit(scope.scope, entry)">
                    {{ busyPath === 'agent:' + scope.scope + ':' + entry.agentType ? '加载中...' : '编辑' }}
                  </button>
                </div>
              </article>
            </div>
          </article>
        </div>

        <div v-if="visibleAgentScopes.length === 0 && agentScopes.length > 0" class="memory-empty" data-testid="memory-center-agent-all-empty">
          <div class="memory-empty-title">所有 scope 都还没有 Agent MEMORY.md</div>
          <div class="memory-empty-text">点击上方"显示空 scope"可展开并创建。</div>
        </div>
      </div>

      <div v-if="inlineDelete.target" class="modal-overlay" data-testid="memory-center-inline-delete-overlay" @click.self="inlineDelete.cancel">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-inline-delete-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">删除 durable memory</div>
              <div class="memory-modal-tip">{{ inlineDelete.target.name }} · {{ inlineDelete.target.target }}</div>
            </div>
            <button class="btn btn-ghost" :disabled="inlineDelete.deleting" @click="inlineDelete.cancel">关闭</button>
          </div>
          <div class="memory-form-helper">删除后对应的 topic file 也会被移除，无法恢复。如果后续可能重用，建议先"编辑"备份内容。</div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" :disabled="inlineDelete.deleting" @click="inlineDelete.cancel">取消</button>
            <button class="btn btn-danger" data-testid="memory-center-inline-delete-confirm" :disabled="inlineDelete.deleting" @click="inlineDelete.confirm">
              {{ inlineDelete.deleting ? '删除中...' : '确认删除' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="memoryEditor.open" class="modal-overlay" data-testid="memory-center-editor-overlay" @click.self="memoryEditor.close">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-editor">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">{{ memoryEditor.mode === 'edit' ? '编辑 durable memory' : '新建 durable memory' }}</div>
              <div class="memory-modal-tip">{{ memoryEditor.form.target === 'team' ? 'Team durable memory' : 'Private durable memory' }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="memory-center-editor-close" @click="memoryEditor.close">关闭</button>
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">目标</label>
              <select v-model="memoryEditor.form.target" class="modal-input" data-testid="memory-center-editor-target" :disabled="memoryIdentityLocked">
                <option value="private">Private</option>
                <option value="team">Team</option>
              </select>
            </div>
            <div class="modal-input-flex">
              <label class="settings-inline-label">类型</label>
              <select v-model="memoryEditor.form.type" class="modal-input" data-testid="memory-center-editor-type" :disabled="memoryIdentityLocked">
                <option value="project">project</option>
                <option value="feedback">feedback</option>
                <option value="reference">reference</option>
                <option value="user">user</option>
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
            现有 durable memory 的名称和类型暂时锁定；如需改名或改类型，请删除后重建。
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">内容</label>
              <textarea v-model="memoryEditor.form.content" rows="12" class="modal-input" data-testid="memory-center-editor-content"></textarea>
            </div>
          </div>
          <div class="memory-form-helper">
            <button class="btn btn-secondary btn-xs" data-testid="memory-center-editor-template" @click="memoryEditor.fillTemplate">套用当前类型模板</button>
            <span>feedback / project 类型需要包含 <code>Why:</code> 和 <code>How to apply:</code>。</span>
          </div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="memory-center-editor-cancel" @click="memoryEditor.close">取消</button>
            <button v-if="memoryEditor.form.existingPath" class="btn btn-danger" data-testid="memory-center-editor-delete" :disabled="memoryEditor.deleting" @click="memoryEditor.remove">
              {{ memoryEditor.deleting ? '删除中...' : '删除' }}
            </button>
            <button class="btn btn-primary" data-testid="memory-center-editor-save" :disabled="memoryEditor.saving" @click="memoryEditor.save">
              {{ memoryEditor.saving ? '保存中...' : '保存' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="agentEditor.open" class="modal-overlay" data-testid="memory-center-agent-editor-overlay" @click.self="agentEditor.close">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-agent-editor">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">编辑 Agent 记忆</div>
              <div class="memory-modal-tip">{{ scopeTitle(agentEditor.form.scope) }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="memory-center-agent-editor-close" @click="agentEditor.close">关闭</button>
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">Scope</label>
              <select v-model="agentEditor.form.scope" class="modal-input" data-testid="memory-center-agent-scope">
                <option value="project">project</option>
                <option value="user">user</option>
                <option value="local">local</option>
              </select>
            </div>
            <div class="modal-input-flex">
              <label class="settings-inline-label">Agent Type</label>
              <input v-model="agentEditor.form.agentType" class="modal-input" data-testid="memory-center-agent-type" placeholder="例如：Writer" />
            </div>
          </div>
          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">MEMORY.md</label>
              <textarea v-model="agentEditor.form.content" rows="12" class="modal-input" data-testid="memory-center-agent-content" placeholder="保存该 Agent 专属的长期偏好、检查清单或角色上下文"></textarea>
            </div>
          </div>
          <div class="memory-form-helper">
            Agent 记忆只在子 Agent 启动时注入。清空内容后保存即可把该 Agent 的 <code>MEMORY.md</code> 重置为空。
          </div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="memory-center-agent-cancel" @click="agentEditor.close">取消</button>
            <button class="btn btn-primary" data-testid="memory-center-agent-save" :disabled="agentEditor.saving" @click="agentEditor.save">
              {{ agentEditor.saving ? '保存中...' : '保存 Agent 记忆' }}
            </button>
          </div>
        </div>
      </div>
    </section>
  `,
};
