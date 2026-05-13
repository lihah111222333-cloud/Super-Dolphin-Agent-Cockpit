import { computed, onBeforeUnmount, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';

const GUIDE_PREF_KEY = 'shared-files.guide-collapsed';
const SORT_PREF_KEY = 'shared-files.sort-mode';
const SORT_MODES = /** @type {const} */ (['updated-desc', 'updated-asc', 'path-asc']);

function ensureArray(value) {
  return Array.isArray(value) ? value : [];
}

function formatTimestamp(value) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function previewText(raw, fallback = '点击"查看内容"加载全文。') {
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

function splitPath(path) {
  const normalized = (path || '').toString().trim();
  if (!normalized) return { dir: '', base: '未命名' };
  const idx = normalized.lastIndexOf('/');
  if (idx < 0) return { dir: '', base: normalized };
  return { dir: normalized.slice(0, idx + 1), base: normalized.slice(idx + 1) || normalized };
}

function formatBytes(len) {
  if (!Number.isFinite(len) || len <= 0) return '0 B';
  if (len < 1024) return `${len} B`;
  if (len < 1024 * 1024) return `${(len / 1024).toFixed(1)} KB`;
  return `${(len / 1024 / 1024).toFixed(1)} MB`;
}

function isTaskHandoffPath(path) {
  return (path || '').toString().trim().startsWith('handoff/tasks/');
}

function normalizeFinalOutputRefs(value) {
  if (!Array.isArray(value)) return [];
  return value.map((item) => {
    if (typeof item === 'string') return { path: item.trim() };
    if (!item || typeof item !== 'object') return null;
    const path = (item.path || item.sharedfile?.path || '').toString().trim();
    if (!path) return null;
    return {
      path,
      runKey: (item.runKey || item.run_key || '').toString().trim(),
      dagKey: (item.dagKey || item.dag_key || '').toString().trim(),
      sourceNodeKey: (item.sourceNodeKey || item.source_node_key || '').toString().trim(),
    };
  }).filter(Boolean);
}

function useFinalOutputMarkers(props) {
  const showFinalOnly = ref(false);
  const finalOutputRefByPath = computed(() => {
    const refs = normalizeFinalOutputRefs(props.finalOutputRefs);
    return new Map(refs.map((ref) => [ref.path, ref]));
  });
  const finalOutputCount = computed(() => finalOutputRefByPath.value.size);

  watch(finalOutputCount, (next) => {
    if (next === 0) showFinalOnly.value = false;
  });

  function finalOutputRefFor(file) {
    const path = (file?.path || '').toString().trim();
    if (!path) return null;
    return finalOutputRefByPath.value.get(path) || null;
  }

  function isFinalOutputFile(file) {
    return Boolean(finalOutputRefFor(file));
  }

  function toggleFinalOnly() {
    showFinalOnly.value = !showFinalOnly.value;
  }

  return { showFinalOnly, finalOutputCount, finalOutputRefFor, isFinalOutputFile, toggleFinalOnly };
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
  } catch {
    /* non-critical */
  }
}

export const SharedFilesPage = {
  name: 'SharedFilesPage',
  props: {
    files: { type: Array, default: () => [] },
    cwd: { type: String, default: '' },
    finalOutputRefs: { type: Array, default: () => [] },
  },
  emits: ['open-memory-center', 'refresh', 'start-inherited-chat'],
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
    const searchText = ref('');
    const finalOutput = useFinalOutputMarkers(props);
    const guideCollapsed = ref(false);
    const sortMode = ref('updated-desc');
    const refreshing = ref(false);
    const copiedInViewer = ref(false);

    let noticeTimer = null;
    let copyTimer = null;

    loadPreference(GUIDE_PREF_KEY, false).then((value) => {
      guideCollapsed.value = Boolean(value);
    });
    loadPreference(SORT_PREF_KEY, 'updated-desc').then((value) => {
      if (SORT_MODES.includes(value)) sortMode.value = value;
    });

    const items = computed(() => ensureArray(props.files));
    const taskHandoffCount = computed(() => items.value.filter((item) => isTaskHandoffPath(item?.path)).length);

    const filteredItems = computed(() => {
      const needle = searchText.value.trim().toLowerCase();
      const list = items.value.filter((item) => {
        if (finalOutput.showFinalOnly.value && !finalOutput.isFinalOutputFile(item)) return false;
        if (!needle) return true;
        const path = (item?.path || '').toString().toLowerCase();
        const updatedBy = (item?.updated_by || item?.updatedBy || '').toString().toLowerCase();
        const content = (item?.content || '').toString().toLowerCase();
        return path.includes(needle) || updatedBy.includes(needle) || content.includes(needle);
      });
      const mode = sortMode.value;
      const byTime = (a, b) => {
        const at = new Date(a?.updated_at || a?.updatedAt || 0).getTime();
        const bt = new Date(b?.updated_at || b?.updatedAt || 0).getTime();
        return bt - at;
      };
      if (mode === 'updated-desc') return [...list].sort(byTime);
      if (mode === 'updated-asc') return [...list].sort((a, b) => -byTime(a, b));
      if (mode === 'path-asc') {
        return [...list].sort((a, b) => (a?.path || '').localeCompare(b?.path || ''));
      }
      return list;
    });

    function setNotice(level, message) {
      notice.level = level || 'info';
      notice.message = (message || '').toString().trim();
      if (noticeTimer) { clearTimeout(noticeTimer); noticeTimer = null; }
      if (notice.message && level !== 'error') {
        noticeTimer = setTimeout(() => {
          notice.message = '';
        }, 5200);
      }
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
      copiedInViewer.value = false;
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
        setNotice('info', '已从共享文件创建记忆，建议到"记忆中心"继续维护。');
      } catch (error) {
        setNotice('error', `Promote 失败：${toErrorMessage(error)}`);
      } finally {
        saving.value = false;
      }
    }

    function askDelete(file) {
      const target = (file?.path || '').toString().trim();
      if (!target || deletingPath.value) return;
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
        if (selectedFile.path === target) viewing.value = false;
        setNotice('info', `已删除共享文件：${target}`);
        confirmDeletePath.value = '';
        emit('refresh');
      } catch (error) {
        setNotice('error', `删除失败：${toErrorMessage(error)}`);
      } finally {
        deletingPath.value = '';
      }
    }

    async function copyViewerContent() {
      const text = selectedFile.content || '';
      if (!text) return;
      try {
        if (navigator?.clipboard?.writeText) {
          await navigator.clipboard.writeText(text);
        } else {
          throw new Error('clipboard unavailable');
        }
        copiedInViewer.value = true;
        if (copyTimer) clearTimeout(copyTimer);
        copyTimer = setTimeout(() => { copiedInViewer.value = false; }, 1600);
      } catch (error) {
        setNotice('error', `复制失败：${toErrorMessage(error)}`);
      }
    }

    function toggleGuide() {
      guideCollapsed.value = !guideCollapsed.value;
      savePreference(GUIDE_PREF_KEY, guideCollapsed.value);
    }

    function clearSearch() {
      searchText.value = '';
    }

    function changeSort(event) {
      const value = (event?.target?.value || '').toString();
      if (SORT_MODES.includes(value)) {
        sortMode.value = value;
        savePreference(SORT_PREF_KEY, value);
      }
    }

    function handleRefresh() {
      refreshing.value = true;
      emit('refresh');
      setTimeout(() => { refreshing.value = false; }, 800);
    }

    watch(() => props.files, () => {
      refreshing.value = false;
    });

    onBeforeUnmount(() => {
      if (noticeTimer) clearTimeout(noticeTimer);
      if (copyTimer) clearTimeout(copyTimer);
    });

    return {
      items,
      taskHandoffCount,
      filteredItems,
      notice,
      viewing,
      promoteOpen,
      loadingDetailPath,
      saving,
      deletingPath,
      confirmDeletePath,
      selectedFile,
      promoteForm,
      searchText,
      showFinalOnly: finalOutput.showFinalOnly,
      sortMode,
      guideCollapsed,
      refreshing,
      copiedInViewer,
      finalOutputCount: finalOutput.finalOutputCount,
      formatTimestamp,
      previewText,
      splitPath,
      formatBytes,
      finalOutputRefFor: finalOutput.finalOutputRefFor,
      isFinalOutputFile: finalOutput.isFinalOutputFile,
      openViewer,
      closeViewer,
      openPromote,
      closePromote,
      savePromote,
      askDelete,
      cancelDelete,
      confirmDelete,
      copyViewerContent,
      toggleGuide,
      clearSearch,
      toggleFinalOnly: finalOutput.toggleFinalOnly,
      changeSort,
      handleRefresh,
      openMemoryCenter: () => emit('open-memory-center'),
      // Phase 2: 跨页面「用此文件新建对话」
      startInheritedChat: (file) => {
        const path = (file?.path || '').toString().trim();
        if (!path) return;
        emit('start-inherited-chat', { sharedFilePath: path });
      },
    };
  },
  template: `
    <section id="page-shared-files" class="page active shared-files-page" data-testid="shared-files-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>共享文件</h2></div>
        <div class="memory-center-toolbar" data-testid="shared-files-toolbar">
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
              data-testid="shared-files-search"
              placeholder="搜索 path / 内容 / 更新者"
            />
            <button
              v-if="searchText"
              class="memory-center-search-clear"
              data-testid="shared-files-search-clear"
              aria-label="清除"
              @click="clearSearch"
            >×</button>
          </div>
          <select
            :value="sortMode"
            class="memory-center-sort-select"
            data-testid="shared-files-sort"
            @change="changeSort"
          >
            <option value="updated-desc">最新更新</option>
            <option value="updated-asc">最早更新</option>
            <option value="path-asc">按 Path</option>
          </select>
          <button
            class="btn btn-secondary btn-toolbar-sm"
            data-testid="shared-files-final-toggle"
            :class="{ 'btn-primary': showFinalOnly }"
            :disabled="finalOutputCount === 0"
            @click="toggleFinalOnly"
          >最终产物 {{ finalOutputCount }}</button>
          <button
            class="btn btn-secondary btn-toolbar-sm"
            data-testid="shared-files-refresh"
            :disabled="refreshing"
            @click="handleRefresh"
          >
            <span v-if="refreshing" class="memory-refresh-spin" aria-hidden="true"></span>
            {{ refreshing ? '刷新中' : '刷新' }}
          </button>
        </div>
      </div>

      <div class="panel-body memory-center-body memory-center-body-has-toolbar" data-testid="shared-files-body">
        <div
          class="data-card-vue memory-center-callout"
          :class="{ 'is-collapsed': guideCollapsed }"
          data-testid="shared-files-callout"
        >
          <div class="memory-center-callout-head">
            <div>
              <div class="memory-center-callout-title">
                共享文件 · Agent 协作中转站
                <span class="jr-badge jr-badge-default">{{ items.length }} 条</span>
              </div>
              <div v-if="!guideCollapsed" class="memory-center-callout-subtitle">
                协作草稿不会自动进入长期记忆；值得保留的内容点"提升为长期记忆"。
              </div>
            </div>
            <div class="memory-center-callout-actions">
              <button
                class="btn btn-ghost btn-toolbar-sm"
                data-testid="shared-files-guide-toggle"
                @click="toggleGuide"
              >{{ guideCollapsed ? '展开指引' : '收起指引' }}</button>
              <button
                class="btn btn-secondary btn-toolbar-sm"
                data-testid="shared-files-open-memory-center"
                @click="openMemoryCenter"
              >打开记忆中心</button>
            </div>
          </div>
          <div v-if="!guideCollapsed" class="memory-center-callout-body">
            <div class="memory-center-guide-grid">
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">什么时候放这里</div>
                <div class="memory-center-guide-text">命令输出摘录、待整理笔记、handoff 清单、跨 Agent 中间结果。</div>
              </article>
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">什么时候 Promote</div>
                <div class="memory-center-guide-text">当内容已经稳定、可复用、值得跨会话保留时，再整理为长期记忆。</div>
              </article>
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">注意</div>
                <div class="memory-center-guide-text">若你选择 feedback / project 类型，内容需要补全 <code>Why:</code> 和 <code>How to apply:</code> 才能通过校验。</div>
              </article>
              <article class="memory-center-guide-card">
                <div class="memory-center-guide-title">任务接力摘要</div>
                <div class="memory-center-guide-text">
                  系统为自动化任务维护的接力摘要也会出现在这里，路径固定在 <code>handoff/tasks/</code> 下。
                  它用于短期任务连续性，不等于长期记忆。当前共 {{ taskHandoffCount }} 份。
                </div>
              </article>
            </div>
          </div>
        </div>

        <div
          v-if="notice.message"
          class="settings-prompt-notice memory-notice-fade"
          :class="'is-' + notice.level"
          data-testid="shared-files-notice"
        >{{ notice.message }}</div>

        <div v-if="items.length === 0" class="memory-empty" data-testid="shared-files-empty">
          <svg class="memory-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
            <rect x="10" y="10" width="28" height="32" rx="3" opacity="0.4"/>
            <path d="M16 18h16M16 24h16M16 30h10" stroke-linecap="round" opacity="0.6"/>
            <circle cx="36" cy="36" r="7" fill="var(--surface)" stroke="currentColor"/>
            <path d="M33 36h6M36 33v6" stroke-linecap="round"/>
          </svg>
          <div class="memory-empty-title">暂无共享文件</div>
          <div class="memory-empty-text">
            Agent 在对话里调用 <code>shared_file_write</code> 工具后，会话中间产物会出现在这里。你也可以从记忆中心打开演示用法。
          </div>
          <div class="memory-empty-text">
            任务接力摘要会由系统自动写到 <code>handoff/tasks/</code>；如果这里仍为空，说明当前还没有自动化任务产出接力摘要。
          </div>
          <div class="memory-empty-actions">
            <button class="btn btn-secondary btn-toolbar-sm" @click="openMemoryCenter">了解记忆中心</button>
          </div>
        </div>

        <div v-else-if="filteredItems.length === 0" class="memory-empty" data-testid="shared-files-filter-empty">
          <svg class="memory-empty-illustration" viewBox="0 0 48 48" fill="none" stroke="currentColor" stroke-width="1.4" aria-hidden="true">
            <circle cx="20" cy="20" r="10"/>
            <path d="M28 28l8 8" stroke-linecap="round"/>
            <path d="M15 20h10" stroke-linecap="round" opacity="0.5"/>
          </svg>
          <div class="memory-empty-title">没有匹配的共享文件</div>
          <div class="memory-empty-text">当前过滤没有命中任何条目，试着清空搜索或换个关键词。</div>
          <div class="memory-empty-actions">
            <button class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button>
          </div>
        </div>

        <div v-else class="memory-entry-grid" data-testid="shared-files-list">
          <article
            v-for="(item, idx) in filteredItems"
            :key="item.path || idx"
            class="data-card-vue memory-entry-card"
            :class="{ 'is-final-output': isFinalOutputFile(item) }"
          >
            <div class="memory-entry-head">
              <div class="shared-files-title-row">
                <div>
                  <div class="shared-files-basename" :title="item.path">{{ splitPath(item.path).base }}</div>
                  <div v-if="splitPath(item.path).dir" class="shared-files-dirname" :title="item.path">{{ splitPath(item.path).dir }}</div>
                  <span class="memory-sr-only">{{ item.path }}</span>
                </div>
                <span
                  v-if="isFinalOutputFile(item)"
                  class="jr-badge jr-badge-success"
                  data-testid="shared-files-final-badge"
                >最终产物</span>
              </div>
              <div class="memory-entry-updated">{{ formatTimestamp(item.updated_at) }}</div>
            </div>
            <div class="memory-entry-meta">
              <span>{{ item.updated_by || '-' }}</span>
              <span class="shared-files-size-hint">{{ formatBytes((item.content || '').length) }}</span>
            </div>
            <pre class="memory-entry-preview">{{ previewText(item.content) }}</pre>
            <div class="memory-card-actions shared-files-actions">
              <button
                class="btn btn-secondary btn-xs"
                :data-testid="'shared-files-view-' + idx"
                :disabled="loadingDetailPath === item.path"
                @click="openViewer(item)"
              >{{ loadingDetailPath === item.path ? '加载中...' : '查看内容' }}</button>
              <button
                class="btn btn-primary btn-xs"
                :data-testid="'shared-files-promote-' + idx"
                :disabled="loadingDetailPath === item.path"
                @click="openPromote(item)"
              >提升为长期记忆</button>
              <button
                class="btn btn-danger btn-xs"
                :data-testid="'shared-files-delete-' + idx"
                :disabled="deletingPath === item.path"
                @click="askDelete(item)"
              >{{ deletingPath === item.path ? '删除中...' : '删除' }}</button>
              <button
                class="btn btn-ghost btn-xs"
                :data-testid="'shared-files-fork-' + idx"
                :title="'以此文件为背景新建一个继承对话'"
                @click="startInheritedChat(item)"
              >用此文件新建对话</button>
            </div>
          </article>
        </div>
      </div>

      <div v-if="viewing" class="modal-overlay" data-testid="shared-files-viewer-overlay" @click.self="closeViewer">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="shared-files-viewer">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">共享文件内容</div>
              <div class="memory-modal-tip" :title="selectedFile.path">{{ selectedFile.path }}</div>
            </div>
            <div class="memory-modal-head-actions">
              <button
                class="btn btn-secondary btn-xs"
                data-testid="shared-files-viewer-copy"
                :disabled="!selectedFile.content"
                @click="copyViewerContent"
              >{{ copiedInViewer ? '已复制' : '复制内容' }}</button>
              <button class="btn btn-ghost" data-testid="shared-files-viewer-close" @click="closeViewer">关闭</button>
            </div>
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
            <button
              class="btn btn-ghost"
              data-testid="shared-files-delete-close"
              :disabled="deletingPath === confirmDeletePath"
              @click="cancelDelete"
            >关闭</button>
          </div>
          <div class="memory-form-helper">
            共享文件一旦删除无法恢复。如果还需要这份内容，先"提升为长期记忆"再删除。
          </div>
          <div class="memory-editor-actions">
            <button
              class="btn btn-ghost"
              data-testid="shared-files-delete-cancel"
              :disabled="deletingPath === confirmDeletePath"
              @click="cancelDelete"
            >取消</button>
            <button
              class="btn btn-danger"
              data-testid="shared-files-delete-confirm"
              :disabled="deletingPath === confirmDeletePath"
              @click="confirmDelete"
            >{{ deletingPath === confirmDeletePath ? '删除中...' : '确认删除' }}</button>
          </div>
        </div>
      </div>

      <div v-if="promoteOpen" class="modal-overlay" data-testid="shared-files-promote-overlay" @click.self="closePromote">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="shared-files-promote-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">提升为长期记忆</div>
              <div class="memory-modal-tip">{{ promoteForm.sharedPath }}</div>
            </div>
            <button class="btn btn-ghost" data-testid="shared-files-promote-close" @click="closePromote">关闭</button>
          </div>

          <div class="modal-input-row">
            <div class="modal-input-flex">
              <label class="settings-inline-label">目标</label>
              <select v-model="promoteForm.target" class="modal-input" data-testid="shared-files-promote-target">
                <option value="private">私有记忆</option>
                <option value="team">团队记忆</option>
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
            <button
              class="btn btn-primary"
              data-testid="shared-files-promote-save"
              :disabled="saving"
              @click="savePromote"
            >{{ saving ? '保存中...' : '创建记忆' }}</button>
          </div>
        </div>
      </div>
    </section>
  `,
};
