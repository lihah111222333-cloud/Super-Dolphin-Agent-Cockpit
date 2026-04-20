import { computed, reactive, ref } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

function ensureArray(value) {
  return Array.isArray(value) ? value : [];
}

function formatTimestamp(value) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function previewText(raw, fallback = '点击“查看内容”加载全文。') {
  const text = (raw || '').toString().trim();
  if (!text) return fallback;
  const lines = text.split('\n');
  const joined = lines.slice(0, 5).join('\n');
  return joined.length > 260 ? `${joined.slice(0, 260)}…` : joined;
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

function titleFromPath(path) {
  const normalized = (path || '').toString().trim().split('/').pop() || 'shared-file';
  const base = normalized.replace(/\.[^.]+$/, '').replace(/[-_]+/g, ' ').trim();
  if (!base) return 'Shared file memory';
  return base.replace(/\b\w/g, (ch) => ch.toUpperCase());
}

function descriptionFromContent(content, path) {
  const firstLine = (content || '').toString().split(/\r?\n/).map((line) => line.trim()).find(Boolean);
  if (firstLine) {
    return firstLine.length > 100 ? `${firstLine.slice(0, 100)}…` : firstLine;
  }
  return `From shared file ${path || '-'}`;
}

function emptyPromoteForm() {
  return {
    sharedPath: '',
    target: 'private',
    type: 'reference',
    name: '',
    description: '',
    content: '',
  };
}

export const SharedFilesPage = {
  name: 'SharedFilesPage',
  props: {
    files: { type: Array, default: () => [] },
    cwd: { type: String, default: '' },
  },
  emits: ['open-memory-center', 'refresh'],
  setup(props, { emit }) {
    const notice = reactive({ level: 'info', message: '' });
    const viewing = ref(false);
    const promoteOpen = ref(false);
    const loadingDetailPath = ref('');
    const saving = ref(false);
    const deletingPath = ref('');
    const confirmDeletePath = ref('');
    const selectedFile = reactive({ path: '', content: '', updatedBy: '', updatedAt: '' });
    const promoteForm = reactive(emptyPromoteForm());

    const items = computed(() => ensureArray(props.files));

    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
    }

    async function loadSharedFile(path, fallbackContent = '') {
      const target = (path || '').toString().trim();
      if (!target) return null;
      loadingDetailPath.value = target;
      try {
        const detail = await callAPI('ui/memory/shared-file/get', { path: target });
        return {
          path: detail?.path || target,
          content: (detail?.content || fallbackContent || '').toString(),
          updatedBy: (detail?.updatedBy || '').toString(),
          updatedAt: detail?.updatedAt || '',
        };
      } finally {
        loadingDetailPath.value = '';
      }
    }

    async function openViewer(file) {
      try {
        const detail = await loadSharedFile(file?.path, file?.content);
        if (!detail) return;
        selectedFile.path = detail.path;
        selectedFile.content = detail.content;
        selectedFile.updatedBy = detail.updatedBy;
        selectedFile.updatedAt = detail.updatedAt;
        viewing.value = true;
      } catch (error) {
        setNotice('error', `读取共享文件失败：${toErrorMessage(error)}`);
      }
    }

    function closeViewer() {
      viewing.value = false;
    }

    async function openPromote(file) {
      try {
        const detail = await loadSharedFile(file?.path, file?.content);
        if (!detail) return;
        Object.assign(promoteForm, emptyPromoteForm(), {
          sharedPath: detail.path,
          name: titleFromPath(detail.path),
          description: descriptionFromContent(detail.content, detail.path),
          content: detail.content,
        });
        promoteOpen.value = true;
      } catch (error) {
        setNotice('error', `加载 Promote 表单失败：${toErrorMessage(error)}`);
      }
    }

    function closePromote() {
      promoteOpen.value = false;
      Object.assign(promoteForm, emptyPromoteForm());
    }

    async function savePromote() {
      if (saving.value) return;
      saving.value = true;
      try {
        await callAPI('ui/memory/shared-file/promote', {
          cwd: props.cwd || '',
          sharedPath: promoteForm.sharedPath,
          target: promoteForm.target,
          name: promoteForm.name,
          description: promoteForm.description,
          type: promoteForm.type,
          content: promoteForm.content,
        });
        closePromote();
        setNotice('info', '已从共享文件创建 durable memory，建议到“记忆中心”继续维护。');
      } catch (error) {
        setNotice('error', `Promote 失败：${toErrorMessage(error)}`);
      } finally {
        saving.value = false;
      }
    }

    function askDelete(file) {
      const target = (file?.path || '').toString().trim();
      if (!target) return;
      if (deletingPath.value) return;
      confirmDeletePath.value = target;
    }

    function cancelDelete() {
      if (deletingPath.value) return;
      confirmDeletePath.value = '';
    }

    async function confirmDelete() {
      const target = confirmDeletePath.value;
      if (!target || deletingPath.value) return;
      deletingPath.value = target;
      try {
        await callAPI('ui/memory/shared-file/delete', { path: target });
        if (selectedFile.path === target) {
          viewing.value = false;
        }
        setNotice('info', `已删除共享文件：${target}`);
        confirmDeletePath.value = '';
        emit('refresh');
      } catch (error) {
        setNotice('error', `删除失败：${toErrorMessage(error)}`);
      } finally {
        deletingPath.value = '';
      }
    }

    return {
      items,
      notice,
      viewing,
      promoteOpen,
      loadingDetailPath,
      saving,
      deletingPath,
      confirmDeletePath,
      selectedFile,
      promoteForm,
      formatTimestamp,
      previewText,
      openViewer,
      closeViewer,
      openPromote,
      closePromote,
      savePromote,
      askDelete,
      cancelDelete,
      confirmDelete,
      openMemoryCenter: () => emit('open-memory-center'),
    };
  },
  template: `
    <section id="page-shared-files" class="page active shared-files-page" data-testid="shared-files-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>共享文件</h2></div>
      </div>

      <div class="panel-body memory-center-body" data-testid="shared-files-body">
        <div class="data-card-vue memory-center-callout" data-testid="shared-files-callout">
          <div class="memory-center-callout-head">
            <div>
              <div class="memory-center-callout-title">共享文件适合协作草稿、中间结果和交接上下文</div>
              <div class="memory-center-callout-subtitle">
                这些内容不会自动变成长期记忆。确认值得长期保留后，再人工 Promote 成 durable memory，避免把计划、过程状态或噪音写入长期知识库。
              </div>
            </div>
            <div class="memory-center-callout-actions">
              <button class="btn btn-secondary btn-toolbar-sm" data-testid="shared-files-open-memory-center" @click="openMemoryCenter">打开记忆中心</button>
            </div>
          </div>
          <div class="memory-center-guide-grid">
            <article class="memory-center-guide-card">
              <div class="memory-center-guide-title">什么时候放这里</div>
              <div class="memory-center-guide-text">命令输出摘录、待整理笔记、handoff 清单、跨 Agent 中间结果。</div>
            </article>
            <article class="memory-center-guide-card">
              <div class="memory-center-guide-title">什么时候 Promote</div>
              <div class="memory-center-guide-text">当内容已经稳定、可复用、值得跨会话保留时，再整理为 durable memory 或 Agent 专属 MEMORY.md。</div>
            </article>
            <article class="memory-center-guide-card">
              <div class="memory-center-guide-title">注意</div>
              <div class="memory-center-guide-text">若你选择 feedback / project 类型，内容需要补全 <code>Why:</code> 和 <code>How to apply:</code> 才能通过校验。</div>
            </article>
          </div>
        </div>

        <div v-if="notice.message" class="settings-prompt-notice" :class="'is-' + notice.level" data-testid="shared-files-notice">
          {{ notice.message }}
        </div>

        <div v-if="items.length === 0" class="empty-state" data-testid="shared-files-empty">
          <div class="es-icon">F</div>
          <h3>暂无共享文件</h3>
        </div>

        <div v-else class="memory-entry-grid" data-testid="shared-files-list">
          <article v-for="(item, idx) in items" :key="item.path || idx" class="data-card-vue memory-entry-card">
            <div class="memory-entry-head">
              <div>
                <div class="memory-entry-title">{{ item.path || '未命名共享文件' }}</div>
                <div class="memory-entry-meta">更新者 {{ item.updated_by || '-' }}</div>
              </div>
              <div class="memory-entry-updated">{{ formatTimestamp(item.updated_at) }}</div>
            </div>
            <pre class="memory-entry-preview">{{ previewText(item.content) }}</pre>
            <div class="memory-card-actions">
              <button class="btn btn-secondary btn-xs" :data-testid="'shared-files-view-' + idx" :disabled="loadingDetailPath === item.path" @click="openViewer(item)">
                {{ loadingDetailPath === item.path ? '加载中...' : '查看内容' }}
              </button>
              <button class="btn btn-primary btn-xs" :data-testid="'shared-files-promote-' + idx" :disabled="loadingDetailPath === item.path" @click="openPromote(item)">
                提升为长期记忆
              </button>
              <button class="btn btn-ghost btn-warning btn-xs" :data-testid="'shared-files-delete-' + idx" :disabled="deletingPath === item.path" @click="askDelete(item)">
                {{ deletingPath === item.path ? '删除中...' : '删除' }}
              </button>
            </div>
          </article>
        </div>
      </div>

      <div v-if="viewing" class="modal-overlay" data-testid="shared-files-viewer-overlay" @click.self="closeViewer">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="shared-files-viewer">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">共享文件内容</div>
              <div class="memory-modal-tip">{{ selectedFile.path }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="shared-files-viewer-close" @click="closeViewer">关闭</button>
          </div>
          <div class="data-row-vue"><strong>更新者</strong><span>{{ selectedFile.updatedBy || '-' }}</span></div>
          <div class="data-row-vue"><strong>更新时间</strong><span>{{ formatTimestamp(selectedFile.updatedAt) }}</span></div>
          <pre class="memory-entry-preview shared-files-content-preview">{{ selectedFile.content || '文件为空' }}</pre>
        </div>
      </div>

      <div v-if="confirmDeletePath" class="modal-overlay" data-testid="shared-files-delete-overlay" @click.self="cancelDelete">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="shared-files-delete-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">删除共享文件</div>
              <div class="memory-modal-tip">{{ confirmDeletePath }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="shared-files-delete-close" :disabled="deletingPath === confirmDeletePath" @click="cancelDelete">关闭</button>
          </div>
          <div class="memory-form-helper">
            共享文件一旦删除无法恢复。如果还需要这份内容，先“提升为长期记忆”再删除。
          </div>
          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="shared-files-delete-cancel" :disabled="deletingPath === confirmDeletePath" @click="cancelDelete">取消</button>
            <button class="btn btn-ghost btn-warning" data-testid="shared-files-delete-confirm" :disabled="deletingPath === confirmDeletePath" @click="confirmDelete">
              {{ deletingPath === confirmDeletePath ? '删除中...' : '确认删除' }}
            </button>
          </div>
        </div>
      </div>

      <div v-if="promoteOpen" class="modal-overlay" data-testid="shared-files-promote-overlay" @click.self="closePromote">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="shared-files-promote-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">提升为 durable memory</div>
              <div class="memory-modal-tip">{{ promoteForm.sharedPath }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="shared-files-promote-close" @click="closePromote">关闭</button>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">目标</label>
              <select v-model="promoteForm.target" class="modal-input" data-testid="shared-files-promote-target">
                <option value="private">Private durable memory</option>
                <option value="team">Team durable memory</option>
              </select>
            </div>
            <div class="modal-input-flex">
              <label class="settings-inline-label">类型</label>
              <select v-model="promoteForm.type" class="modal-input" data-testid="shared-files-promote-type">
                <option value="reference">reference</option>
                <option value="project">project</option>
                <option value="feedback">feedback</option>
                <option value="user">user</option>
              </select>
            </div>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">名称</label>
              <input v-model="promoteForm.name" class="modal-input" data-testid="shared-files-promote-name" placeholder="例如：Core dashboard location" />
            </div>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">描述</label>
              <input v-model="promoteForm.description" class="modal-input" data-testid="shared-files-promote-description" placeholder="一句话说明为什么值得长期保留" />
            </div>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">内容</label>
              <textarea v-model="promoteForm.content" rows="10" class="modal-input" data-testid="shared-files-promote-content"></textarea>
            </div>
          </div>

          <div class="memory-form-helper">
            如果你切换成 <code>feedback</code> 或 <code>project</code>，请补全 <code>Why:</code> 与 <code>How to apply:</code> 两段。
          </div>

          <div class="memory-editor-actions">
            <button class="btn btn-ghost" data-testid="shared-files-promote-cancel" @click="closePromote">取消</button>
            <button class="btn btn-primary" data-testid="shared-files-promote-save" :disabled="saving" @click="savePromote">
              {{ saving ? '保存中...' : '创建 durable memory' }}
            </button>
          </div>
        </div>
      </div>
    </section>
  `,
};
