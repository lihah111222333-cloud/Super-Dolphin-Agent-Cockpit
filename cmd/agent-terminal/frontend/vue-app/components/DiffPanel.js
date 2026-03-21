// @ts-nocheck
import { computed, ref, watch, onBeforeUnmount } from '../../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { diffStats } from '../services/diff.js';
import { useProjectStore } from '../stores/projects.js';
import { useDiffPanelPreview } from '../composables/useDiffPanelPreview.js';
import { useDiffPanelInteractions } from '../composables/useDiffPanelInteractions.js';
import { resolveRenderedMarkdownAction } from '../utils/assistant-markdown-click.js';
import { DIFF_PANEL_TEMPLATE } from './DiffPanel.template.js';
import { normalizePreviewText, PREVIEW_KIND } from '../utils/preview-utils.js';

const DIFF_HEADER_ICON_PATHS = {
  change: 'M4 7h16v10H4zM8 11l2 2-2 2M12 15h4',
  files: 'M8 3h7l3 3v15H6V3h2zM15 3v4h4M9 13h6M9 17h6',
};

function normalizePath(value) {
  return (value || '')
    .toString()
    .trim()
    .replace(/\\/g, '/')
    .replace(/^\.\/+/, '')
    .replace(/^(a|b)\//, '')
    .toLowerCase();
}

function countTextLines(text) {
  const normalized = normalizePreviewText(text);
  if (!normalized) return 0;
  const lineBreaks = normalized.match(/\n/g)?.length || 0;
  return normalized.endsWith('\n') ? lineBreaks : lineBreaks + 1;
}

function normalizeStringList(values) {
  if (!Array.isArray(values)) return [];
  return values
    .map((item) => (item || '').toString().trim())
    .filter(Boolean)
    .filter((item, index, list) => list.indexOf(item) === index);
}

function resolvePreviewIdentity(preview) {
  const filePath = (preview?.filePath || '').toString().trim();
  const path = (preview?.path || '').toString().trim();
  return `${filePath || path}\n${normalizePreviewText(preview?.text)}`;
}

function baseName(path) {
  const normalized = normalizePath(path);
  if (!normalized) return '';
  const segments = normalized.split('/').filter(Boolean);
  return segments[segments.length - 1] || '';
}

function fileKey(file, index = 0) {
  const normalized = normalizePath(file?.filename);
  if (normalized) return normalized;
  return `file-${index + 1}`;
}

function fileMatchesTarget(filePath, targetPath) {
  const file = normalizePath(filePath);
  const target = normalizePath(targetPath);
  if (!file || !target) return false;
  if (file === target) return true;
  if (file.endsWith(`/${target}`)) return true;
  if (target.endsWith(`/${file}`)) return true;
  return Boolean(baseName(file) && baseName(file) === baseName(target));
}

function stripCodePathPrefix(value) {
  const raw = (value || '').toString().trim();
  if (!raw) return '';
  return raw.replace(/^\.\/+/, '').replace(/^cmd\//i, '');
}

function displayFilePath(file) {
  const raw = (file?.filename || '').toString();
  const stripped = stripCodePathPrefix(raw);
  return stripped || raw;
}

function splitDisplayFilePath(file) {
  const fullPath = (displayFilePath(file) || '').toString().trim();
  if (!fullPath) return { prefix: '', suffix: '' };
  const normalized = fullPath.replace(/\\/g, '/');
  const segments = normalized.split('/').filter(Boolean);
  if (segments.length <= 1) return { prefix: '', suffix: normalized };
  const keepTailSegments = Math.max(2, Math.ceil(segments.length / 2));
  const splitIndex = Math.max(0, segments.length - keepTailSegments);
  return {
    prefix: splitIndex > 0 ? `${segments.slice(0, splitIndex).join('/')}/` : '',
    suffix: segments.slice(splitIndex).join('/'),
  };
}

function displayFilePathPrefix(file) {
  return splitDisplayFilePath(file).prefix;
}

function displayFilePathSuffix(file) {
  return splitDisplayFilePath(file).suffix;
}

function resolveProjectScope(props, projectStore) {
  const activeProject = ((props.project || projectStore?.state?.active || '.').toString().trim()) || '.';
  const projectList = normalizeStringList(props.projects);
  const fallbackProjects = normalizeStringList(projectStore?.state?.projects);
  return {
    project: activeProject,
    projects: projectList.length > 0 ? projectList : fallbackProjects,
  };
}

function buildSavedPreviewState(preview, text, saveResult) {
  const totalLinesRaw = Number(saveResult?.totalLines);
  const totalLines = Number.isFinite(totalLinesRaw) && totalLinesRaw >= 0
    ? Math.floor(totalLinesRaw)
    : countTextLines(text);
  const filePath = (saveResult?.filePath || preview?.filePath || '').toString().trim();
  const path = (saveResult?.relative || preview?.path || filePath).toString().trim();
  return {
    ...preview,
    previewKind: preview?.previewKind === PREVIEW_KIND.MARKDOWN ? PREVIEW_KIND.MARKDOWN : PREVIEW_KIND.TEXT,
    path,
    filePath,
    text,
    startLine: totalLines > 0 ? 1 : 0,
    endLine: totalLines > 0 ? totalLines : 0,
    totalLines,
    editable: Boolean(filePath) && preview?.editable !== false,
  };
}

function useDiffPanelEditing(props, emit, projectStore) {
  const isEditing = ref(false);
  const draftText = ref('');
  const saving = ref(false);
  const saveError = ref('');
  const savedPreviewOverride = ref(null);
  let disposed = false;

  const previewState = computed(() => {
    const preview = props.markdownPreview;
    if (!preview) return null;
    return savedPreviewOverride.value ? { ...preview, ...savedPreviewOverride.value } : preview;
  });
  const previewText = computed(() => normalizePreviewText(previewState.value?.text));
  const previewEditable = computed(() => {
    const filePath = (previewState.value?.filePath || '').toString().trim();
    return Boolean(previewState.value?.editable) && Boolean(filePath);
  });
  const isDirty = computed(() => isEditing.value && draftText.value !== previewText.value);

  watch(
    () => resolvePreviewIdentity(props.markdownPreview),
    (next, prev) => {
      if (next === prev) return;
      // When the underlying preview changes (e.g., file switch), discard edit state.
      // If the user has unsaved changes, emit dirty=false so the parent clears its indicator.
      if (isDirty.value) {
        emit('preview-dirty-change', false);
      }
      savedPreviewOverride.value = null;
      saveError.value = '';
      saving.value = false;
      isEditing.value = false;
    },
    { immediate: true },
  );

  watch(
    previewText,
    (nextText) => {
      if (isEditing.value) return;
      draftText.value = nextText;
    },
    { immediate: true },
  );

  watch(
    isDirty,
    (dirty) => {
      emit('preview-dirty-change', dirty);
    },
    { immediate: true },
  );

  function startEditing() {
    if (!previewEditable.value || saving.value) return;
    draftText.value = previewText.value;
    saveError.value = '';
    isEditing.value = true;
  }

  function cancelEditing() {
    draftText.value = previewText.value;
    saveError.value = '';
    saving.value = false;
    isEditing.value = false;
  }

  async function savePreviewChanges() {
    if (!previewEditable.value || saving.value || disposed) return false;
    const preview = previewState.value;
    const filePath = (preview?.filePath || '').toString().trim();
    if (!filePath) return false;

    const content = normalizePreviewText(draftText.value);
    const { project, projects } = resolveProjectScope(props, projectStore);
    saving.value = true;
    saveError.value = '';
    try {
      const saveResult = await callAPI('ui/code/save', {
        filePath,
        content,
        project,
        projects,
      });
      if (disposed) return false;
      savedPreviewOverride.value = buildSavedPreviewState(preview, content, saveResult);
      isEditing.value = false;
      return true;
    } catch (error) {
      if (disposed) return false;
      saveError.value = (error?.message || '保存失败').toString();
      return false;
    } finally {
      if (!disposed) {
        saving.value = false;
      }
    }
  }

  onBeforeUnmount(() => {
    disposed = true;
    if (isDirty.value) {
      emit('preview-dirty-change', false);
    }
  });

  return {
    previewState,
    previewEditable,
    previewText,
    isEditing,
    draftText,
    saving,
    saveError,
    isDirty,
    startEditing,
    cancelEditing,
    savePreviewChanges,
  };
}

export const DiffPanel = {
  name: 'DiffPanel',
  props: {
    diffText: { type: String, default: '' },
    mediaPreview: { type: Object, default: null },
    markdownPreview: { type: Object, default: null },
    focusFile: { type: String, default: '' },
    focusLine: { type: Number, default: 0 },
    project: { type: String, default: '' },
    projects: { type: Array, default: () => [] },
  },
  emits: ['file-ref-click', 'citation-click', 'preview-dirty-change'],
  setup(props, context) {
    const emit = typeof context?.emit === 'function' ? context.emit : () => {};
    const projectStore = useProjectStore();
    const {
      previewState,
      previewEditable,
      isEditing,
      draftText,
      saving,
      saveError,
      isDirty,
      startEditing,
      cancelEditing,
      savePreviewChanges,
    } = useDiffPanelEditing(props, emit, projectStore);
    const previewProps = {
      get diffText() { return props.diffText; },
      get mediaPreview() { return props.mediaPreview; },
      get markdownPreview() { return previewState.value; },
      get focusFile() { return props.focusFile; },
      get focusLine() { return props.focusLine; },
    };
    const panelRef = ref(null);
    const editorTextarea = ref(null);
    const {
      lightboxOpen,
      diffTextLength,
      showLargeDiffPreview,
      files,
      fileCountText,
      totals,
      hasMediaPreview,
      mediaThumbSrc,
      mediaFullSrc,
      mediaPath,
      mediaMeta,
      hasMarkdownPreview,
      previewKind,
      previewPath,
      previewMeta,
      previewLanguage,
      isMarkdownPreview,
      isTextPreview,
      isPlainTextPreview,
      isCodeTextPreview,
      markdownHtml,
      textPreviewHtml,
      textPreviewPlainText,
      hasDiffPreview,
      headerTitle,
      headerSubText,
      fileCountValue,
      largeDiffPreviewText,
      openLightbox,
      closeLightbox,
      loadFullDiff,
    } = useDiffPanelPreview(previewProps);

    const {
      isFileCollapsed,
      toggleFileCollapsed,
      fileToggleLabel,
      fileCaretSymbol,
      isCopiedFile,
      copyFilePath,
      isFocusedFile,
      isFocusedLine,
    } = useDiffPanelInteractions({
      props: previewProps,
      panelRef,
      files,
      hasDiffPreview,
      showLargeDiffPreview,
      diffTextLength,
      fileKey,
      displayFilePath,
      fileMatchesTarget,
    });

    function headerIconPath(kind) {
      const key = (kind || '').toString().trim();
      return DIFF_HEADER_ICON_PATHS[key] || DIFF_HEADER_ICON_PATHS.change;
    }

    function headerIconTooltip(kind) {
      const key = (kind || '').toString().trim();
      if (key === 'files') return headerSubText.value;
      return headerTitle.value;
    }

    function linePrefix(type) {
      if (type === 'add') return '+';
      if (type === 'del') return '-';
      if (type === 'hunk') return '@';
      if (type === 'meta') return '·';
      return ' ';
    }

    function onMarkdownPreviewClick(event) {
      if (isEditing.value) return;
      if (!isMarkdownPreview.value) return;
      const action = resolveRenderedMarkdownAction(event);
      if (!action) return;
      if (typeof event?.preventDefault === 'function') event.preventDefault();
      if (typeof event?.stopPropagation === 'function') event.stopPropagation();
      if (action.type === 'file-ref') {
        emit('file-ref-click', action.payload);
        return;
      }
      if (action.type === 'citation') {
        emit('citation-click', action.payload);
      }
    }

    function autoResizeEditor() {
      const el = editorTextarea.value;
      if (!el) return;
      el.style.height = 'auto';
      el.style.height = `${Math.max(200, el.scrollHeight + 4)}px`;
    }

    watch(isEditing, (editing) => {
      if (editing) {
        // Auto-size on next tick when textarea is rendered
        setTimeout(autoResizeEditor, 0);
      }
    });

    return {
      panelRef,
      editorTextarea,
      autoResizeEditor,
      files,
      fileCountText,
      totals,
      hasMediaPreview,
      hasMarkdownPreview,
      hasDiffPreview,
      previewKind,
      previewPath,
      previewMeta,
      previewLanguage,
      previewEditable,
      isMarkdownPreview,
      isTextPreview,
      isPlainTextPreview,
      isCodeTextPreview,
      markdownHtml,
      textPreviewHtml,
      textPreviewPlainText,
      isEditing,
      draftText,
      saving,
      saveError,
      isDirty,
      startEditing,
      cancelEditing,
      savePreviewChanges,
      headerTitle,
      headerSubText,
      fileCountValue,
      showLargeDiffPreview,
      largeDiffPreviewText,
      headerIconPath,
      headerIconTooltip,
      displayFilePath,
      displayFilePathPrefix,
      displayFilePathSuffix,
      fileKey,
      isFileCollapsed,
      toggleFileCollapsed,
      fileToggleLabel,
      fileCaretSymbol,
      isCopiedFile,
      copyFilePath,
      mediaThumbSrc,
      mediaFullSrc,
      mediaPath,
      mediaMeta,
      lightboxOpen,
      diffStats,
      linePrefix,
      isFocusedFile,
      isFocusedLine,
      onMarkdownPreviewClick,
      openLightbox,
      closeLightbox,
      loadFullDiff,
    };
  },
  template: DIFF_PANEL_TEMPLATE,
};
