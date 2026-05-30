import { computed, onBeforeUnmount, reactive, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { callAPI, saveTextFile } from '../services/api.js';

const SORT_PREF_KEY = 'shared-files.sort-mode';
const SORT_MODES = /** @type {const} */ (['updated-desc', 'updated-asc', 'path-asc']);
const FILE_CATEGORIES = /** @type {const} */ (['all', 'final', 'work']);

function ensureArray(value) {
  return Array.isArray(value) ? value : [];
}

function formatTimestamp(value) {
  if (!value) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return date.toLocaleString('zh-CN', { hour12: false });
}

function previewText(raw, fallback = '点击"打开"加载全文。') {
  const text = (raw || '').toString().trim();
  if (!text) return fallback;
  const lines = text.split('\n');
  const joined = lines.slice(0, 5).join('\n');
  return joined.length > 260 ? `${joined.slice(0, 260)}…` : joined;
}

function summaryText(raw, fallback = '暂无摘要') {
  const text = (raw || '').toString().trim();
  if (!text) return fallback;
  const joined = text.split('\n').map((line) => line.trim()).filter(Boolean).slice(0, 2).join(' ');
  return joined.length > 180 ? `${joined.slice(0, 180)}…` : joined;
}

function toErrorMessage(error) {
  return (
    (error && typeof error === 'object' && typeof error.message === 'string' ? error.message : '')
    || String(error || '')
  ).toString().trim();
}

function splitPath(path) {
  const normalized = (path || '').toString().trim();
  if (!normalized) return { dir: '', base: '未命名' };
  const idx = normalized.lastIndexOf('/');
  if (idx < 0) return { dir: '', base: normalized };
  return { dir: normalized.slice(0, idx + 1), base: normalized.slice(idx + 1) || normalized };
}

function exportNameFromPath(path) {
  const base = splitPath(path).base;
  return base && base !== '未命名' ? base : 'shared-file.txt';
}

function formatBytes(len) {
  if (!Number.isFinite(len) || len <= 0) return '0 B';
  if (len < 1024) return `${len} B`;
  if (len < 1024 * 1024) return `${(len / 1024).toFixed(1)} KB`;
  return `${(len / 1024 / 1024).toFixed(1)} MB`;
}

function fileUpdatedAt(file) {
  return file?.updated_at || file?.updatedAt || '';
}

function fileContent(file) {
  return (file?.content || '').toString();
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

function normalizeRetentionItems(value) {
  const rawItems = Array.isArray(value) ? value : ensureArray(value?.items);
  return rawItems.map((item) => {
    if (!item || typeof item !== 'object') return null;
    const path = (item.path || '').toString().trim();
    if (!path) return null;
    return {
      path,
      protected: Boolean(item.protected),
      cleanupCandidate: Boolean(item.cleanupCandidate),
      reason: (item.reason || '').toString().trim(),
      finalOutput: item.finalOutput || item.final_output || null,
    };
  }).filter(Boolean);
}

function useFinalOutputMarkers(props) {
  const currentFilePaths = computed(() => new Set(ensureArray(props.files).map((file) => (file?.path || '').toString().trim()).filter(Boolean)));
  const finalOutputRefByPath = computed(() => {
    const refs = normalizeFinalOutputRefs(props.finalOutputRefs);
    return new Map(refs.filter((ref) => currentFilePaths.value.has(ref.path)).map((ref) => [ref.path, ref]));
  });
  const finalOutputCount = computed(() => finalOutputRefByPath.value.size);

  function finalOutputRefFor(file) {
    const path = (file?.path || '').toString().trim();
    if (!path) return null;
    return finalOutputRefByPath.value.get(path) || null;
  }

  function isFinalOutputFile(file) {
    return Boolean(finalOutputRefFor(file));
  }

  return { finalOutputCount, finalOutputRefFor, isFinalOutputFile };
}

function useSharedFileRetention(props, finalOutput) {
  const protectedByPath = computed(() => {
    const items = normalizeRetentionItems(props.sharedFileRetention);
    return new Map(items.filter((item) => item.protected).map((item) => [item.path, item]));
  });

  function deletionProtectionFor(file) {
    const path = (file?.path || '').toString().trim();
    if (!path) return null;
    const retentionItem = protectedByPath.value.get(path);
    if (retentionItem) return retentionItem;
    const finalRef = finalOutput.finalOutputRefFor(file);
    if (finalRef) return { path, protected: true, reason: 'final_output', finalOutput: finalRef };
    return null;
  }

  function isDeletionProtected(file) {
    return Boolean(deletionProtectionFor(file));
  }

  function deletionProtectionLabel(file) {
    const protection = deletionProtectionFor(file);
    if (!protection) return '';
    if (protection.reason === 'final_output') return '最终产物由任务结果引用，不能直接删除。';
    return '该文件受保留策略保护。';
  }

  return { deletionProtectionFor, isDeletionProtected, deletionProtectionLabel };
}

function useSharedFileCategories(items, filteredItems, finalOutput, searchText) {
  const fileCategory = ref('all');
  const finalOutputItems = computed(() => filteredItems.value.filter((item) => finalOutput.isFinalOutputFile(item)));
  const workItems = computed(() => filteredItems.value.filter((item) => !finalOutput.isFinalOutputFile(item)));
  const workFileCount = computed(() => Math.max(0, items.value.length - finalOutput.finalOutputCount.value));
  const categoryTabs = computed(() => [
    { key: 'all', label: '全部', count: items.value.length },
    { key: 'final', label: '最终产物', count: finalOutput.finalOutputCount.value },
    { key: 'work', label: '工作文件', count: workFileCount.value },
  ]);
  const visibleItems = computed(() => {
    if (fileCategory.value === 'final') return finalOutputItems.value;
    if (fileCategory.value === 'work') return workItems.value;
    return filteredItems.value;
  });
  const categoryEmptyTitle = computed(() => {
    if (searchText.value.trim()) return '没有匹配的文件';
    if (fileCategory.value === 'final') return '无内容';
    if (fileCategory.value === 'work') return '当前没有工作文件';
    return '没有文件';
  });
  const categoryEmptyText = computed(() => {
    if (searchText.value.trim()) return '当前过滤没有命中任何条目，试着清空搜索或换个关键词。';
    if (fileCategory.value === 'final') return 'Agent 生成可交付结果后，会显示在这里。';
    if (fileCategory.value === 'work') return 'Agent 生成草稿、摘录或中间材料后，会显示在这里。';
    return 'Agent 生成报告、草稿或数据文件后，会显示在这里。';
  });

  function setFileCategory(value) {
    const next = (value || '').toString();
    if (FILE_CATEGORIES.includes(next)) fileCategory.value = next;
  }

  function fileRoleLabel(file) {
    return finalOutput.isFinalOutputFile(file) ? '最终产物' : '工作文件';
  }

  return {
    fileCategory,
    finalOutputItems,
    workItems,
    workFileCount,
    categoryTabs,
    visibleItems,
    categoryEmptyTitle,
    categoryEmptyText,
    setFileCategory,
    fileRoleLabel,
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

function emitStartInheritedChat(emit, file) {
  const path = (file?.path || '').toString().trim();
  if (path) emit('start-inherited-chat', { sharedFilePath: path });
}

export const SharedFilesPage = {
  name: 'SharedFilesPage',
  props: {
    files: { type: Array, default: () => [] },
    cwd: { type: String, default: '' },
    finalOutputRefs: { type: Array, default: () => [] },
    sharedFileRetention: { type: Object, default: () => ({}) },
  },
  emits: ['refresh', 'start-inherited-chat'],
  setup(props, { emit }) {
    const notice = reactive({ level: 'info', message: '' });
    const viewing = ref(false);
    const loadingDetailPath = ref('');
    const exportingPath = ref('');
    const deletingPath = ref('');
    const confirmDeletePath = ref('');
    const selectedFile = reactive({ path: '', content: '', updatedBy: '', updatedAt: '' });
    const searchText = ref('');
    const finalOutput = useFinalOutputMarkers(props);
    const retention = useSharedFileRetention(props, finalOutput);
    const sortMode = ref('updated-desc');
    const refreshing = ref(false);
    const copiedInViewer = ref(false);

    let noticeTimer = null;
    let copyTimer = null;

    loadPreference(SORT_PREF_KEY, 'updated-desc').then((value) => {
      if (SORT_MODES.includes(value)) sortMode.value = value;
    });

    const items = computed(() => ensureArray(props.files));

    const filteredItems = computed(() => {
      const needle = searchText.value.trim().toLowerCase();
      const list = items.value.filter((item) => {
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

    const categories = useSharedFileCategories(items, filteredItems, finalOutput, searchText);

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
        setNotice('error', `读取文件失败：${toErrorMessage(error)}`);
      }
    }

    function closeViewer() {
      viewing.value = false;
      copiedInViewer.value = false;
    }

    async function exportSharedFile(file) {
      const target = (file?.path || '').toString().trim();
      if (!target || exportingPath.value) return;
      exportingPath.value = target;
      try {
        const detail = await loadSharedFile(target, file?.content);
        if (!detail) return;
        const savedPath = await saveTextFile({
          defaultPath: props.cwd || '',
          defaultFilename: exportNameFromPath(detail.path),
          content: detail.content,
        });
        if (savedPath) {
          setNotice('info', `已保存到：${savedPath}`);
        } else {
          setNotice('info', '已取消保存。');
        }
      } catch (error) {
        setNotice('error', `导出失败：${toErrorMessage(error)}`);
      } finally {
        exportingPath.value = '';
      }
    }

    function askDelete(file) {
      const target = (file?.path || '').toString().trim();
      if (!target || deletingPath.value) return;
      if (retention.isDeletionProtected(file)) {
        setNotice('error', `最终产物不能直接删除：${target}`);
        return;
      }
      confirmDeletePath.value = target;
    }

    function deleteActionLabel(file) {
      if (retention.isDeletionProtected(file)) return '不可删除';
      if (deletingPath.value === file.path) return '删除中...';
      return '删除';
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
        setNotice('info', `已删除文件：${target}`);
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

    watch(() => props.files, () => { refreshing.value = false; });

    onBeforeUnmount(() => { if (noticeTimer) clearTimeout(noticeTimer); if (copyTimer) clearTimeout(copyTimer); });

    return {
      items,
      filteredItems,
      ...categories,
      notice,
      viewing,
      loadingDetailPath,
      exportingPath,
      deletingPath,
      confirmDeletePath,
      selectedFile,
      searchText,
      sortMode,
      refreshing,
      copiedInViewer,
      finalOutputCount: finalOutput.finalOutputCount,
      formatTimestamp,
      previewText,
      summaryText,
      splitPath,
      formatBytes,
      fileUpdatedAt,
      fileContent,
      finalOutputRefFor: finalOutput.finalOutputRefFor,
      isFinalOutputFile: finalOutput.isFinalOutputFile,
      isDeletionProtected: retention.isDeletionProtected,
      deletionProtectionLabel: retention.deletionProtectionLabel,
      deleteActionLabel,
      openViewer,
      closeViewer,
      exportSharedFile,
      askDelete,
      cancelDelete,
      confirmDelete,
      copyViewerContent,
      clearSearch,
      changeSort,
      handleRefresh,
      startInheritedChat: (file) => emitStartInheritedChat(emit, file),
    };
  },
  template: `
    <section id="page-shared-files" class="page active shared-files-page" data-testid="shared-files-page">
      <div class="panel-header">
        <div class="ph-bar"></div>
        <div class="ph-text"><h2>文件产物</h2></div>
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
              placeholder="搜索文件名 / 内容"
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
            <option value="path-asc">按文件名</option>
          </select>
          <div class="shared-files-category-tabs" data-testid="shared-files-category-tabs" role="tablist" aria-label="文件产物分类">
            <button
              type="button"
              class="shared-files-category-tab"
              :class="{ active: fileCategory === 'all' }"
              data-testid="shared-files-category-tab-all"
              role="tab"
              :aria-selected="fileCategory === 'all' ? 'true' : 'false'"
              @click="setFileCategory('all')"
            >
              <span>全部</span>
              <span class="shared-files-category-count">{{ items.length }}</span>
            </button>
            <button
              type="button"
              class="shared-files-category-tab"
              :class="{ active: fileCategory === 'final' }"
              data-testid="shared-files-category-tab-final"
              role="tab"
              :aria-selected="fileCategory === 'final' ? 'true' : 'false'"
              @click="setFileCategory('final')"
            >
              <span>最终产物</span>
              <span class="shared-files-category-count">{{ finalOutputCount }}</span>
            </button>
            <button
              type="button"
              class="shared-files-category-tab"
              :class="{ active: fileCategory === 'work' }"
              data-testid="shared-files-category-tab-work"
              role="tab"
              :aria-selected="fileCategory === 'work' ? 'true' : 'false'"
              @click="setFileCategory('work')"
            >
              <span>工作文件</span>
              <span class="shared-files-category-count">{{ workFileCount }}</span>
            </button>
          </div>
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
          <div class="memory-empty-title">还没有文件产物</div>
          <div class="memory-empty-text">
            Agent 生成报告、草稿或数据文件后，会显示在这里。
          </div>
        </div>

        <div v-else-if="visibleItems.length === 0" class="shared-files-text-empty" data-testid="shared-files-category-empty">
          <div class="shared-files-text-empty-title">{{ categoryEmptyTitle }}</div>
          <div v-if="fileCategory !== 'final' || searchText" class="shared-files-text-empty-body">{{ categoryEmptyText }}</div>
          <div class="memory-empty-actions">
            <button v-if="searchText" class="btn btn-secondary btn-toolbar-sm" @click="clearSearch">清空搜索</button>
          </div>
        </div>

        <div v-else class="shared-files-list" data-testid="shared-files-list">
          <article
            v-for="(item, idx) in visibleItems"
            :key="item.path || idx"
            class="data-card-vue shared-files-card"
            :class="{ 'is-final-output': isFinalOutputFile(item) }"
          >
            <div class="shared-files-card-main">
              <div class="shared-files-card-head">
                <div class="shared-files-card-title" :title="item.path">{{ splitPath(item.path).base }}</div>
                <span
                  v-if="isFinalOutputFile(item)"
                  class="jr-badge jr-badge-success"
                  data-testid="shared-files-final-badge"
                >最终产物</span>
              </div>
              <div class="shared-files-card-meta">
                <span>{{ fileRoleLabel(item) }}</span>
                <span>{{ formatTimestamp(fileUpdatedAt(item)) }}</span>
                <span>{{ formatBytes(fileContent(item).length) }}</span>
              </div>
              <div class="memory-sr-only">{{ item.path }}</div>
              <div class="shared-files-card-summary">{{ summaryText(item.content) }}</div>
            </div>
            <div class="memory-card-actions shared-files-actions">
              <button
                class="btn btn-secondary btn-xs"
                :data-testid="'shared-files-view-' + idx"
                :disabled="loadingDetailPath === item.path"
                @click="openViewer(item)"
              >{{ loadingDetailPath === item.path ? '加载中...' : '打开' }}</button>
              <button
                class="btn btn-secondary btn-xs"
                :data-testid="'shared-files-export-' + idx"
                :disabled="loadingDetailPath === item.path || !!exportingPath"
                @click="exportSharedFile(item)"
              >{{ exportingPath === item.path ? '导出中...' : '导出' }}</button>
              <button
                class="btn btn-danger btn-xs"
                :data-testid="'shared-files-delete-' + idx"
                :disabled="deletingPath === item.path || isDeletionProtected(item)"
                :title="deletionProtectionLabel(item)"
                @click="askDelete(item)"
              >{{ deleteActionLabel(item) }}</button>
              <button
                class="btn btn-ghost btn-xs"
                :data-testid="'shared-files-fork-' + idx"
                :title="'以此文件为背景新建一个继承对话'"
                @click="startInheritedChat(item)"
              >用此文件继续对话</button>
            </div>
          </article>
        </div>
      </div>

      <div v-if="viewing" class="modal-overlay" data-testid="shared-files-viewer-overlay" @click.self="closeViewer">
        <div class="modal-box memory-modal shared-files-viewer-modal" role="dialog" aria-modal="true" data-testid="shared-files-viewer">
          <div class="memory-modal-head">
            <div class="shared-files-viewer-title">
              <div class="modal-title">文件预览</div>
              <div class="memory-modal-tip" :title="selectedFile.path">{{ selectedFile.path }}</div>
            </div>
            <div class="memory-modal-head-actions">
              <button
                class="btn btn-secondary btn-xs"
                data-testid="shared-files-viewer-export"
                :disabled="!selectedFile.path || !!exportingPath"
                @click="exportSharedFile(selectedFile)"
              >{{ exportingPath === selectedFile.path ? '导出中...' : '导出' }}</button>
              <button
                class="btn btn-secondary btn-xs"
                data-testid="shared-files-viewer-copy"
                :disabled="!selectedFile.content"
                @click="copyViewerContent"
              >{{ copiedInViewer ? '已复制' : '复制内容' }}</button>
              <button class="btn btn-ghost" data-testid="shared-files-viewer-close" @click="closeViewer">关闭</button>
            </div>
          </div>
          <div class="shared-files-viewer-meta">
            <div class="shared-files-viewer-meta-item">
              <span class="shared-files-viewer-meta-label">来源</span>
              <span class="shared-files-viewer-meta-value">{{ selectedFile.updatedBy || '-' }}</span>
            </div>
            <div class="shared-files-viewer-meta-item">
              <span class="shared-files-viewer-meta-label">更新时间</span>
              <span class="shared-files-viewer-meta-value">{{ formatTimestamp(selectedFile.updatedAt) }}</span>
            </div>
          </div>
          <pre class="memory-entry-preview shared-files-content-preview">{{ selectedFile.content || '文件为空' }}</pre>
        </div>
      </div>

      <div v-if="confirmDeletePath" class="modal-overlay" data-testid="shared-files-delete-overlay" @click.self="cancelDelete">
        <div class="modal-box memory-modal" role="dialog" aria-modal="true" data-testid="shared-files-delete-modal">
          <div class="memory-modal-head">
            <div>
              <div class="modal-title">删除文件</div>
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
            文件删除后无法恢复。删除前请确认这份内容不再需要。
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

    </section>
  `,
};
