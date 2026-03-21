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

// ── Helpers (outside setup to keep size-guard happy) ──────────

function resolveProjectCwd(projectStore) {
  return (projectStore?.state?.active || '').toString().trim();
}

function withCwd(cwd, payload) {
  if (!cwd) return payload;
  return { ...payload, cwd };
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

/** Normalize a prompt item from API response. */
function normalizePromptItem(raw, agentType) {
  return {
    id: (raw?.id || generateId()).toString(),
    name: (raw?.name || raw?.title || '').toString(),
    content: (raw?.content || raw?.hint || '').toString(),
    description: (raw?.description || '').toString(),
    agentType: (raw?.agentType || agentType || 'main').toString(),
    isDefault: Boolean(raw?.isDefault),
    createdAt: (raw?.createdAt || raw?.created_at || '').toString(),
  };
}

export const SystemPromptPage = {
  name: 'SystemPromptPage',
  props: {
    projectStore: { type: Object, default: null },
    windowCwd: { type: String, default: '' },
  },
  setup(props) {
    // ── State ────────────────────────────────────────
    const activeTab = ref('main'); // 'main' | 'sub'
    const currentScopeCwd = ref('');
    const promptCards = ref([]); // all prompt cards
    const loading = ref(false);
    const notice = reactive({ level: 'info', message: '' });

    // Editor state
    const editorOpen = ref(false);
    const editorMode = ref('edit'); // 'edit' | 'create'
    const saving = ref(false);
    const deletingId = ref('');
    const form = reactive({
      id: '',
      name: '',
      content: '',
      description: '',
    });

    // ── Computed ─────────────────────────────────────
    const filteredCards = computed(() =>
      promptCards.value.filter(c => c.agentType === activeTab.value)
    );

    const cwdDisplay = computed(() =>
      currentScopeCwd.value || props.windowCwd || '未知'
    );

    // ── Helpers ─────────────────────────────────────
    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
    }

    function getCwd() {
      return resolveProjectCwd(props.projectStore);
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
        const list = Array.isArray(res?.prompts) ? res.prompts : [];
        promptCards.value = list.map(item => normalizePromptItem(item));
        setNotice('info', '');
      } catch (error) {
        logWarn('system-prompt', 'load.failed', { error });
        setNotice('error', `加载失败：${error?.message || error}`);
      } finally {
        loading.value = false;
      }
    }

    async function savePrompt() {
      if (saving.value) return;
      const name = (form.name || '').trim();
      if (!name) {
        setNotice('error', '请填写提示词名称');
        return;
      }
      saving.value = true;
      try {
        const payload = {
          id: form.id || '',
          name,
          content: form.content || '',
          description: form.description || '',
          agentType: activeTab.value,
        };
        await callAPI('prompts/write', withCwd(getCwd(), payload));
        await loadPrompts();
        editorOpen.value = false;
        setNotice('info', `提示词已保存：${name}`);
      } catch (error) {
        logWarn('system-prompt', 'save.failed', { error });
        setNotice('error', `保存失败：${error?.message || error}`);
      } finally {
        saving.value = false;
      }
    }

    async function deletePrompt(item) {
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
        setNotice('error', `删除失败：${error?.message || error}`);
      } finally {
        deletingId.value = '';
      }
    }

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
        setNotice('error', `复制失败：${error?.message || error}`);
      }
    }

    // ── Editor ──────────────────────────────────────
    function openCreate() {
      form.id = '';
      form.name = '';
      form.content = '';
      form.description = '';
      editorMode.value = 'create';
      editorOpen.value = true;
      setNotice('info', '');
      logDebug('system-prompt', 'editor.create');
    }

    function openEdit(item) {
      form.id = item.id || '';
      form.name = item.name || '';
      form.content = item.content || '';
      form.description = item.description || '';
      editorMode.value = 'edit';
      editorOpen.value = true;
      setNotice('info', '');
      logDebug('system-prompt', 'editor.edit', { id: item.id });
    }

    function closeEditor() {
      editorOpen.value = false;
      setNotice('info', '');
    }

    // ── CWD ─────────────────────────────────────────
    async function loadCurrentScopeCwd() {
      try {
        const cfg = await callAPI('config/read', {});
        currentScopeCwd.value = (cfg?.cwd || '').toString().trim();
      } catch { currentScopeCwd.value = ''; }
    }

    // ── Lifecycle ───────────────────────────────────
    onMounted(() => {
      logInfo('system-prompt', 'page.mounted');
      loadCurrentScopeCwd();
      loadPrompts();
    });

    watch(
      () => resolveProjectCwd(props.projectStore),
      (next, prev) => {
        if (next === prev) return;
        loadCurrentScopeCwd();
        loadPrompts();
      },
    );

    return {
      activeTab, promptCards, filteredCards, loading,
      notice, editorOpen, editorMode, saving, deletingId,
      form, cwdDisplay,
      switchTab, loadPrompts, savePrompt, deletePrompt,
      copyPromptContent, openCreate, openEdit, closeEditor,
      truncate, countStats,
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

      <!-- Tabs -->
      <div class="sub-tabs" data-testid="sp-tabs">
        <button class="sub-tab" :class="{ active: activeTab === 'main' }" data-testid="sp-tab-main" @click="switchTab('main')">主 Agent</button>
        <button class="sub-tab" :class="{ active: activeTab === 'sub' }" data-testid="sp-tab-sub" @click="switchTab('sub')">子 Agent</button>
      </div>

      <!-- Card list -->
      <div class="section-header">
        提示词列表
        <span v-if="filteredCards.length > 0" class="sp-count-tip">{{ filteredCards.length }} 条</span>
      </div>

      <div class="panel-body sp-list-panel" data-testid="sp-body">

        <!-- Toolbar -->
        <div class="sp-toolbar" data-testid="sp-toolbar">
          <button class="btn btn-secondary" data-testid="sp-create-btn" @click="openCreate">+ 新建提示词</button>
          <button class="btn btn-ghost" data-testid="sp-refresh-btn" :disabled="loading" @click="loadPrompts">
            {{ loading ? '加载中...' : '刷新' }}
          </button>
        </div>

        <!-- Empty state -->
        <div v-if="!loading && filteredCards.length === 0" class="empty-state" data-testid="sp-empty">
          <div class="es-icon">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" style="width:24px;height:24px">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
              <polyline points="14 2 14 8 20 8"/>
            </svg>
          </div>
          <h3>暂无{{ activeTab === 'main' ? '主 Agent' : '子 Agent' }}提示词</h3>
          <p>点击"新建提示词"开始创建</p>
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
              </div>
            </div>
            <div v-if="item.description" class="sp-card-desc">{{ item.description }}</div>
            <div class="sp-card-preview">{{ truncate(item.content) }}</div>
            <div class="sp-card-meta">{{ countStats(item.content).lines }} 行 · {{ countStats(item.content).chars }} 字符</div>
            <div class="sp-card-actions">
              <button class="btn btn-secondary btn-xs" :data-testid="'sp-edit-btn-' + idx" @click="openEdit(item)">编辑</button>
              <button class="btn btn-ghost btn-xs" :data-testid="'sp-copy-btn-' + idx" @click="copyPromptContent(item)">复制</button>
              <button class="btn btn-ghost btn-xs btn-warning" :data-testid="'sp-delete-btn-' + idx" :disabled="Boolean(deletingId)" @click="deletePrompt(item)">
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
              <div class="modal-title">{{ editorMode === 'create' ? '新建提示词' : '编辑提示词' }}</div>
              <div class="sp-editor-tip">{{ activeTab === 'main' ? '主 Agent' : '子 Agent' }} · 作用域 {{ cwdDisplay }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="sp-editor-close-btn" @click="closeEditor">关闭</button>
          </div>

          <div class="sp-editor-body">
            <div class="sp-field">
              <label>名称</label>
              <input class="modal-input" data-testid="sp-name-input" v-model="form.name" placeholder="例如：代码审查专家" :disabled="saving" />
            </div>

            <div class="sp-field">
              <label>描述（可选）</label>
              <input class="modal-input" data-testid="sp-desc-input" v-model="form.description" placeholder="一句话描述用途" :disabled="saving" />
            </div>

            <div class="sp-field">
              <label>提示词内容</label>
              <textarea
                class="sp-textarea"
                data-testid="sp-content-input"
                rows="12"
                v-model="form.content"
                placeholder="输入 System Prompt 内容..."
                :disabled="saving"
              ></textarea>
              <div class="sp-field-meta">{{ countStats(form.content).lines }} 行 · {{ countStats(form.content).chars }} 字符</div>
            </div>

            <!-- Notice -->
            <div v-if="notice.message" class="sp-notice" :class="'is-' + notice.level" data-testid="sp-editor-notice">
              {{ notice.message }}
            </div>

            <!-- Actions -->
            <div class="sp-editor-actions" data-testid="sp-editor-actions">
              <button class="btn btn-ghost" @click="closeEditor">取消</button>
              <button class="btn btn-primary sp-save-btn" data-testid="sp-save-btn" :disabled="saving" @click="savePrompt">
                {{ saving ? '保存中...' : '保存' }}
              </button>
            </div>
          </div>
        </div>
      </div>
    </section>
  `,
};
