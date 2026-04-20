import { computed, reactive, ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

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

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

function memoryTemplateForType(type) {
  switch ((type || '').toString()) {
    case 'feedback':
      return 'rule\nWhy: \nHow to apply: ';
    case 'project':
      return 'fact\nWhy: \nHow to apply: ';
    case 'reference':
      return 'Pointer: \nWhy it matters: ';
    default:
      return 'User preference: ';
  }
}

function resetMemoryForm(form, target = 'private') {
  Object.assign(form, {
    target,
    existingPath: '',
    name: '',
    description: '',
    type: 'project',
    content: memoryTemplateForType('project'),
  });
}

function resetAgentForm(form, scope = 'project') {
  Object.assign(form, {
    scope,
    agentType: '',
    path: '',
    content: '',
  });
}

function firstNonEmpty(...values) {
  for (const value of values) {
    const text = (value || '').toString().trim();
    if (text) return text;
  }
  return '';
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
    const memoryEditorOpen = ref(false);
    const memoryEditorMode = ref('create');
    const memorySaving = ref(false);
    const memoryDeleting = ref(false);
    const agentEditorOpen = ref(false);
    const agentSaving = ref(false);
    const memoryForm = reactive({});
    const agentForm = reactive({});

    resetMemoryForm(memoryForm);
    resetAgentForm(agentForm);

    const overview = computed(() => ensureObject(props.model?.overview));
    const privateMemory = computed(() => ensureObject(props.model?.private));
    const teamMemory = computed(() => ensureObject(props.model?.team));
    const agentScopes = computed(() => ensureArray(props.model?.agentScopes));
    const currentCwd = computed(() => firstNonEmpty(overview.value.projectRoot));
    const memoryIdentityLocked = computed(() => memoryEditorMode.value === 'edit' && Boolean(memoryForm.existingPath));

    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
    }

    function statusLabel(enabled) {
      return enabled ? '已启用' : '未启用';
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

    function openCreateMemory(target) {
      resetMemoryForm(memoryForm, target || 'private');
      memoryEditorMode.value = 'create';
      memoryEditorOpen.value = true;
    }

    async function openEditMemory(target, entry) {
      const path = (entry?.path || '').toString().trim();
      if (!path) return;
      busyPath.value = `${target}:${path}`;
      try {
        const detail = await callAPI('ui/memory/entry/get', {
          cwd: currentCwd.value,
          target,
          path,
        });
        Object.assign(memoryForm, {
          target,
          existingPath: detail?.path || path,
          name: detail?.name || '',
          description: detail?.description || '',
          type: detail?.type || 'project',
          content: detail?.content || '',
        });
        memoryEditorMode.value = 'edit';
        memoryEditorOpen.value = true;
      } catch (error) {
        setNotice('error', `加载 durable memory 失败：${toErrorMessage(error)}`);
      } finally {
        busyPath.value = '';
      }
    }

    function closeMemoryEditor() {
      memoryEditorOpen.value = false;
      resetMemoryForm(memoryForm, memoryForm.target || 'private');
    }

    async function saveMemory() {
      if (memorySaving.value) return;
      memorySaving.value = true;
      try {
        await callAPI('ui/memory/entry/upsert', {
          cwd: currentCwd.value,
          target: memoryForm.target,
          existingPath: memoryForm.existingPath,
          name: memoryForm.name,
          description: memoryForm.description,
          type: memoryForm.type,
          content: memoryForm.content,
        });
        closeMemoryEditor();
        setNotice('info', 'durable memory 已保存。');
        emit('refresh');
      } catch (error) {
        setNotice('error', `保存 durable memory 失败：${toErrorMessage(error)}`);
      } finally {
        memorySaving.value = false;
      }
    }

    async function deleteMemory() {
      if (!memoryForm.existingPath || memoryDeleting.value) return;
      memoryDeleting.value = true;
      try {
        await callAPI('ui/memory/entry/delete', {
          cwd: currentCwd.value,
          target: memoryForm.target,
          path: memoryForm.existingPath,
        });
        closeMemoryEditor();
        setNotice('info', 'durable memory 已删除。');
        emit('refresh');
      } catch (error) {
        setNotice('error', `删除 durable memory 失败：${toErrorMessage(error)}`);
      } finally {
        memoryDeleting.value = false;
      }
    }

    function fillTemplate() {
      memoryForm.content = memoryTemplateForType(memoryForm.type);
    }

    function openCreateAgent(scope) {
      resetAgentForm(agentForm, scope || 'project');
      agentEditorOpen.value = true;
    }

    async function openEditAgent(scope, entry) {
      const agentType = (entry?.agentType || '').toString().trim();
      if (!agentType) return;
      busyPath.value = `agent:${scope}:${agentType}`;
      try {
        const detail = await callAPI('ui/memory/agent/get', {
          cwd: currentCwd.value,
          scope,
          agentType,
        });
        Object.assign(agentForm, {
          scope,
          agentType: detail?.agentType || agentType,
          path: detail?.path || '',
          content: detail?.content || '',
        });
        agentEditorOpen.value = true;
      } catch (error) {
        setNotice('error', `加载 Agent 记忆失败：${toErrorMessage(error)}`);
      } finally {
        busyPath.value = '';
      }
    }

    function closeAgentEditor() {
      agentEditorOpen.value = false;
      resetAgentForm(agentForm, agentForm.scope || 'project');
    }

    async function saveAgentMemory() {
      if (agentSaving.value) return;
      agentSaving.value = true;
      try {
        await callAPI('ui/memory/agent/save', {
          cwd: currentCwd.value,
          scope: agentForm.scope,
          agentType: agentForm.agentType,
          content: agentForm.content,
        });
        closeAgentEditor();
        setNotice('info', 'Agent 记忆已保存。清空内容后保存即可重置。');
        emit('refresh');
      } catch (error) {
        setNotice('error', `保存 Agent 记忆失败：${toErrorMessage(error)}`);
      } finally {
        agentSaving.value = false;
      }
    }

    return {
      notice,
      busyPath,
      overview,
      privateMemory,
      teamMemory,
      agentScopes,
      currentCwd,
      memoryEditorOpen,
      memoryEditorMode,
      memorySaving,
      memoryDeleting,
      agentEditorOpen,
      agentSaving,
      memoryForm,
      agentForm,
      memoryIdentityLocked,
      formatTimestamp,
      countLabel,
      statusLabel,
      scopeTitle,
      openCreateMemory,
      openEditMemory,
      closeMemoryEditor,
      saveMemory,
      deleteMemory,
      fillTemplate,
      openCreateAgent,
      openEditAgent,
      closeAgentEditor,
      saveAgentMemory,
      openSharedFiles: () => emit('open-shared-files'),
      emitRefresh: () => emit('refresh'),
    };
  },
  template: `
    <section id="page-memory-center" class="page active memory-center-page" data-testid="memory-center-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>记忆中心</h2></div>
      </div>

      <div class="panel-body memory-center-body" data-testid="memory-center-body">
        <div class="data-card-vue memory-center-callout" data-testid="memory-center-callout">
          <div class="memory-center-callout-head">
            <div>
              <div class="memory-center-callout-title">这里展示真正的长期记忆与 Agent 专属记忆</div>
              <div class="memory-center-callout-subtitle">
                Shared Files 仍然保留在“共享文件”页，用于草稿、中间结果和协作交接，不会自动沉淀成长期记忆。
              </div>
            </div>
            <div class="memory-center-callout-actions">
              <button class="btn btn-secondary btn-toolbar-sm" data-testid="memory-center-open-shared-files" @click="openSharedFiles">查看共享文件</button>
              <button class="btn btn-primary btn-toolbar-sm" data-testid="memory-center-refresh" @click="emitRefresh" :disabled="model.loading">刷新</button>
            </div>
          </div>
          <div class="memory-center-guide-grid">
            <article class="memory-center-guide-card">
              <div class="memory-center-guide-title">长期记忆</div>
              <div class="memory-center-guide-text">用于保存稳定事实、偏好、项目长期上下文。真正写盘的是 topic files，<code>MEMORY.md</code> 只是索引入口。</div>
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

        <div v-if="notice.message" class="settings-prompt-notice" :class="'is-' + notice.level" data-testid="memory-center-notice">
          {{ notice.message }}
        </div>

        <div class="section-header">当前状态</div>
        <div class="data-card-vue memory-center-overview-card" data-testid="memory-center-overview">
          <div class="data-row-vue"><strong>Memory 系统</strong><span>{{ statusLabel(overview.enabled) }}</span></div>
          <div class="data-row-vue"><strong>Memory 工具</strong><span>{{ statusLabel(overview.toolsEnabled) }}</span></div>
          <div class="data-row-vue"><strong>项目根</strong><span>{{ overview.projectRoot || '-' }}</span></div>
          <div class="data-row-vue"><strong>Private root</strong><span>{{ overview.privateRoot || '-' }}</span></div>
          <div class="data-row-vue"><strong>Team memory</strong><span>{{ overview.teamFeatureEnabled ? '已配置' : '未配置' }}</span></div>
        </div>

        <div v-if="model.error" class="settings-prompt-notice is-error" data-testid="memory-center-error">
          {{ model.error }}
        </div>

        <div class="section-header">长期记忆 · Private</div>
        <div class="data-card-vue memory-scope-card" data-testid="memory-center-private-summary">
          <div class="memory-scope-head">
            <div class="memory-scope-head-main">
              <div class="data-row-vue"><strong>索引文件</strong><span>{{ privateMemory.indexPath || '-' }}</span></div>
              <div class="data-row-vue"><strong>目录</strong><span>{{ privateMemory.rootPath || '-' }}</span></div>
              <div class="data-row-vue"><strong>条目数</strong><span>{{ countLabel(privateMemory.entries, 'entries') }}</span></div>
            </div>
            <button class="btn btn-primary btn-xs" data-testid="memory-center-private-create" @click="openCreateMemory('private')">新建 durable memory</button>
          </div>
          <div v-if="privateMemory.notice" class="memory-scope-note">{{ privateMemory.notice }}</div>
        </div>
        <div v-if="!privateMemory.entries || privateMemory.entries.length === 0" class="empty-state memory-scope-empty" data-testid="memory-center-private-empty">
          <div class="es-icon">M</div>
          <h3>暂无 Private durable memory</h3>
        </div>
        <div v-else class="memory-entry-grid" data-testid="memory-center-private-list">
          <article v-for="(entry, idx) in privateMemory.entries" :key="entry.path || entry.name || idx" class="data-card-vue memory-entry-card">
            <div class="memory-entry-head">
              <div>
                <div class="memory-entry-title">{{ entry.name || '未命名条目' }}</div>
                <div class="memory-entry-meta">{{ entry.type || 'unknown' }} · {{ entry.path || '-' }}</div>
              </div>
              <div class="memory-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</div>
            </div>
            <div v-if="entry.description" class="memory-entry-description">{{ entry.description }}</div>
            <pre class="memory-entry-preview">{{ entry.preview || '暂无预览' }}</pre>
            <div class="memory-card-actions">
              <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-private-edit-' + idx" :disabled="busyPath === 'private:' + entry.path" @click="openEditMemory('private', entry)">
                {{ busyPath === 'private:' + entry.path ? '加载中...' : '编辑' }}
              </button>
            </div>
          </article>
        </div>

        <div class="section-header">长期记忆 · Team</div>
        <div class="data-card-vue memory-scope-card" data-testid="memory-center-team-summary">
          <div class="memory-scope-head">
            <div class="memory-scope-head-main">
              <div class="data-row-vue"><strong>索引文件</strong><span>{{ teamMemory.indexPath || '-' }}</span></div>
              <div class="data-row-vue"><strong>目录</strong><span>{{ teamMemory.rootPath || '-' }}</span></div>
              <div class="data-row-vue"><strong>条目数</strong><span>{{ countLabel(teamMemory.entries, 'entries') }}</span></div>
            </div>
            <button class="btn btn-primary btn-xs" data-testid="memory-center-team-create" :disabled="!teamMemory.rootPath" @click="openCreateMemory('team')">新建 team memory</button>
          </div>
          <div v-if="teamMemory.notice" class="memory-scope-note">{{ teamMemory.notice }}</div>
        </div>
        <div v-if="!teamMemory.entries || teamMemory.entries.length === 0" class="empty-state memory-scope-empty" data-testid="memory-center-team-empty">
          <div class="es-icon">T</div>
          <h3>暂无 Team durable memory</h3>
        </div>
        <div v-else class="memory-entry-grid" data-testid="memory-center-team-list">
          <article v-for="(entry, idx) in teamMemory.entries" :key="entry.path || entry.name || idx" class="data-card-vue memory-entry-card">
            <div class="memory-entry-head">
              <div>
                <div class="memory-entry-title">{{ entry.name || '未命名条目' }}</div>
                <div class="memory-entry-meta">{{ entry.type || 'unknown' }} · {{ entry.path || '-' }}</div>
              </div>
              <div class="memory-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</div>
            </div>
            <div v-if="entry.description" class="memory-entry-description">{{ entry.description }}</div>
            <pre class="memory-entry-preview">{{ entry.preview || '暂无预览' }}</pre>
            <div class="memory-card-actions">
              <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-team-edit-' + idx" :disabled="busyPath === 'team:' + entry.path" @click="openEditMemory('team', entry)">
                {{ busyPath === 'team:' + entry.path ? '加载中...' : '编辑' }}
              </button>
            </div>
          </article>
        </div>

        <div class="section-header">Agent 记忆</div>
        <div class="memory-agent-scope-list" data-testid="memory-center-agent-scopes">
          <article v-for="scope in agentScopes" :key="scope.scope" class="data-card-vue memory-agent-scope-card">
            <div class="memory-agent-scope-head">
              <div>
                <div class="memory-agent-scope-title">{{ scopeTitle(scope.scope) }}</div>
                <div class="memory-agent-scope-count">{{ countLabel(scope.entries, 'agents') }}</div>
              </div>
              <button class="btn btn-primary btn-xs" :data-testid="'memory-center-agent-create-' + scope.scope" @click="openCreateAgent(scope.scope)">新建 / 编辑</button>
            </div>
            <div class="data-row-vue"><strong>目录</strong><span>{{ scope.rootPath || '-' }}</span></div>
            <div v-if="scope.notice" class="memory-scope-note">{{ scope.notice }}</div>
            <div v-if="!scope.entries || scope.entries.length === 0" class="memory-agent-empty">当前 scope 下还没有可读的 <code>MEMORY.md</code>。</div>
            <div v-else class="memory-agent-entry-list">
              <article v-for="entry in scope.entries" :key="scope.scope + ':' + entry.agentType" class="memory-agent-entry">
                <div class="memory-agent-entry-head">
                  <div class="memory-agent-entry-title">{{ entry.agentType }}</div>
                  <div class="memory-agent-entry-updated">{{ formatTimestamp(entry.updatedAt) }}</div>
                </div>
                <div class="memory-agent-entry-path">{{ entry.path || '-' }}</div>
                <pre class="memory-entry-preview">{{ entry.preview || 'MEMORY.md 为空' }}</pre>
                <div class="memory-card-actions">
                  <button class="btn btn-secondary btn-xs" :data-testid="'memory-center-agent-edit-' + scope.scope + '-' + entry.agentType" :disabled="busyPath === 'agent:' + scope.scope + ':' + entry.agentType" @click="openEditAgent(scope.scope, entry)">
                    {{ busyPath === 'agent:' + scope.scope + ':' + entry.agentType ? '加载中...' : '编辑' }}
                  </button>
                </div>
              </article>
            </div>
          </article>
        </div>
        <div class="memory-center-footer-note">
          推荐工作流：先在“共享文件”沉淀协作草稿，再把确认值得长期保留的内容整理为 durable memory 或特定 Agent 的 MEMORY.md。
        </div>
      </div>

      <div v-if="memoryEditorOpen" class="modal-overlay" data-testid="memory-center-editor-overlay" @click.self="closeMemoryEditor">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-editor">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">{{ memoryEditorMode === 'edit' ? '编辑 durable memory' : '新建 durable memory' }}</div>
              <div class="memory-modal-tip">{{ memoryForm.target === 'team' ? 'Team durable memory' : 'Private durable memory' }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="memory-center-editor-close" @click="closeMemoryEditor">关闭</button>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">目标</label>
              <select v-model="memoryForm.target" class="modal-input" data-testid="memory-center-editor-target" :disabled="memoryIdentityLocked">
                <option value="private">Private</option>
                <option value="team">Team</option>
              </select>
            </div>
            <div class="modal-input-flex">
              <label class="settings-inline-label">类型</label>
              <select v-model="memoryForm.type" class="modal-input" data-testid="memory-center-editor-type" :disabled="memoryIdentityLocked">
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
              <input v-model="memoryForm.name" class="modal-input" data-testid="memory-center-editor-name" :disabled="memoryIdentityLocked" placeholder="例如：Core dashboard owner" />
            </div>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">描述</label>
              <input v-model="memoryForm.description" class="modal-input" data-testid="memory-center-editor-description" placeholder="一句话描述为什么值得长期保留" />
            </div>
          </div>

          <div class="memory-form-helper" v-if="memoryIdentityLocked">
            现有 durable memory 的名称和类型暂时锁定；如需改名或改类型，请删除后重建。
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">内容</label>
              <textarea v-model="memoryForm.content" rows="12" class="modal-input" data-testid="memory-center-editor-content"></textarea>
            </div>
          </div>

          <div class="memory-form-helper">
            <button class="btn btn-secondary btn-xs" data-testid="memory-center-editor-template" @click="fillTemplate">套用当前类型模板</button>
            <span>feedback / project 类型需要包含 <code>Why:</code> 和 <code>How to apply:</code>。</span>
          </div>

          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="memory-center-editor-cancel" @click="closeMemoryEditor">取消</button>
            <button v-if="memoryForm.existingPath" class="btn btn-ghost btn-warning" data-testid="memory-center-editor-delete" :disabled="memoryDeleting" @click="deleteMemory">
              {{ memoryDeleting ? '删除中...' : '删除' }}
            </button>
            <button class="btn btn-primary" data-testid="memory-center-editor-save" :disabled="memorySaving" @click="saveMemory">
              {{ memorySaving ? '保存中...' : '保存' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="agentEditorOpen" class="modal-overlay" data-testid="memory-center-agent-editor-overlay" @click.self="closeAgentEditor">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="memory-center-agent-editor">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">编辑 Agent 记忆</div>
              <div class="memory-modal-tip">{{ scopeTitle(agentForm.scope) }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="memory-center-agent-editor-close" @click="closeAgentEditor">关闭</button>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">Scope</label>
              <select v-model="agentForm.scope" class="modal-input" data-testid="memory-center-agent-scope">
                <option value="project">project</option>
                <option value="user">user</option>
                <option value="local">local</option>
              </select>
            </div>
            <div class="modal-input-flex">
              <label class="settings-inline-label">Agent Type</label>
              <input v-model="agentForm.agentType" class="modal-input" data-testid="memory-center-agent-type" placeholder="例如：Writer" />
            </div>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">MEMORY.md</label>
              <textarea v-model="agentForm.content" rows="12" class="modal-input" data-testid="memory-center-agent-content" placeholder="保存该 Agent 专属的长期偏好、检查清单或角色上下文"></textarea>
            </div>
          </div>

          <div class="memory-form-helper">
            Agent 记忆只在子 Agent 启动时注入。清空内容后保存即可把该 Agent 的 <code>MEMORY.md</code> 重置为空。
          </div>

          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="memory-center-agent-cancel" @click="closeAgentEditor">取消</button>
            <button class="btn btn-primary" data-testid="memory-center-agent-save" :disabled="agentSaving" @click="saveAgentMemory">
              {{ agentSaving ? '保存中...' : '保存 Agent 记忆' }}
            </button>
          </div>
        </div>
      </div>
    </section>
  `,
};
