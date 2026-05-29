import * as Vue from '../lib/vue.esm-browser.prod.js';
import { callAPI } from '../services/api.js';
import { diffStats, parseUnifiedDiff } from '../services/diff.js';
import { resolveRenderedMarkdownAction } from '../utils/assistant-markdown-click.js';
import { normalizePreviewText, PREVIEW_KIND, isMarkdownPath } from '../utils/preview-utils.js';
import { renderAssistantMarkdown } from '../utils/assistant-markdown.js';

// Helpers copied from DiffPanel.jsx for isolation:

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

const LARGE_DIFF_PREVIEW_THRESHOLD = 120000;
const LARGE_DIFF_PREVIEW_CHARS = 40000;

function normalizePreviewKind(preview) {
  const previewKind = (preview?.previewKind || '').toString().trim().toLowerCase();
  if (previewKind === PREVIEW_KIND.MARKDOWN || previewKind === PREVIEW_KIND.TEXT) return previewKind;
  const language = (preview?.language || '').toString().trim().toLowerCase();
  const path = (preview?.path || '').toString().trim();
  if (language === PREVIEW_KIND.MARKDOWN || isMarkdownPath(path)) return PREVIEW_KIND.MARKDOWN;
  return PREVIEW_KIND.TEXT;
}

function normalizePreviewLanguage(preview, previewKind) {
  const language = (preview?.language || '').toString().trim().toLowerCase();
  if (language) return language === 'text' ? 'plaintext' : language;
  if (previewKind === PREVIEW_KIND.MARKDOWN) return PREVIEW_KIND.MARKDOWN;
  const path = (preview?.path || '').toString().trim();
  if (/\.json$/i.test(path)) return 'json';
  if (/\.(yaml|yml)$/i.test(path)) return 'yaml';
  return 'plaintext';
}

function isPlainTextLanguage(language) {
  const normalized = (language || '').toString().trim().toLowerCase();
  return !normalized || normalized === 'text' || normalized === 'txt' || normalized === 'plain' || normalized === 'plaintext';
}

function buildCodeFence(text, language) {
  const source = normalizePreviewText(text);
  const maxRun = Math.max(3, ...(source.match(/`+/g) || []).map((item) => item.length + 1));
  const fence = '`'.repeat(maxRun);
  const lang = (language || 'text').toString().trim() || 'text';
  return `${fence}${lang}\n${source}\n${fence}`;
}

export function setupDiffPanelVue(props, { emit } = {}) {
  const lightboxOpen = Vue.ref(false);
  const forceFullDiff = Vue.ref(false);
  const isEditing = Vue.ref(false);
  const draftText = Vue.ref('');
  const saving = Vue.ref(false);
  const saveError = Vue.ref('');
  const savedPreviewOverride = Vue.ref(null);
  const panelRef = Vue.ref(null);
  const collapsedFileKeys = Vue.ref([]);

  const previewState = Vue.computed(() => {
    const preview = props.markdownPreview;
    if (!preview) return null;
    return savedPreviewOverride.value ? { ...preview, ...savedPreviewOverride.value } : preview;
  });

  const previewText = Vue.computed(() => normalizePreviewText(previewState.value?.text));
  
  const previewEditable = Vue.computed(() => {
    const filePath = (previewState.value?.filePath || '').toString().trim();
    return Boolean(previewState.value?.editable) && Boolean(filePath);
  });

  const isDirty = Vue.computed(() => isEditing.value && draftText.value !== previewText.value);

  // Watch markdownPreview to reset editing state
  Vue.watch(() => props.markdownPreview ? resolvePreviewIdentity(props.markdownPreview) : null, () => {
    if (isDirty.value) {
      emit?.('preview-dirty-change', false);
    }
    savedPreviewOverride.value = null;
    saveError.value = '';
    saving.value = false;
    isEditing.value = false;
  });

  // Watch isDirty to emit dirty state changes
  Vue.watch(isDirty, (newVal) => {
    emit?.('preview-dirty-change', newVal);
  });

  const startEditing = () => {
    if (!previewEditable.value || saving.value) return;
    draftText.value = previewText.value || '';
    saveError.value = '';
    isEditing.value = true;
  };

  const cancelEditing = () => {
    draftText.value = previewText.value || '';
    saveError.value = '';
    saving.value = false;
    isEditing.value = false;
  };

  const savePreviewChanges = async () => {
    if (!previewEditable.value || saving.value) return false;
    const preview = previewState.value;
    const filePath = (preview?.filePath || '').toString().trim();
    if (!filePath) return false;

    const content = normalizePreviewText(draftText.value);
    
    const activeProject = (props.project || '/repo').toString().trim();
    const projectList = normalizeStringList(props.projects).length > 0 ? normalizeStringList(props.projects) : ['/repo', '/repo-2'];

    saving.value = true;
    saveError.value = '';
    try {
      const saveResult = await callAPI('ui/code/save', {
        filePath,
        content,
        project: activeProject,
        projects: projectList,
      });
      savedPreviewOverride.value = buildSavedPreviewState(preview, content, saveResult);
      isEditing.value = false;
      return true;
    } catch (error) {
      saveError.value = (error?.message || '保存失败').toString();
      return false;
    } finally {
      saving.value = false;
    }
  };

  const diffTextLength = Vue.computed(() => (props.diffText || '').length);
  const hasRequestedFocus = Vue.computed(() => Boolean((props.focusFile || '').toString().trim()) || Number(props.focusLine) > 0);
  
  const showLargeDiffPreview = Vue.computed(() => diffTextLength.value > LARGE_DIFF_PREVIEW_THRESHOLD && !forceFullDiff.value && !hasRequestedFocus.value);
  const diffTextForDisplay = Vue.computed(() => (showLargeDiffPreview.value ? (props.diffText || '').slice(0, LARGE_DIFF_PREVIEW_CHARS) : (props.diffText || '')));
  
  const files = Vue.computed(() => parseUnifiedDiff(diffTextForDisplay.value));
  const fileCountText = Vue.computed(() => `${files.value.length} file${files.value.length === 1 ? '' : 's'}`);
  
  const hasDiffPreview = Vue.computed(() => {
    const hasMedia = Boolean(props.mediaPreview?.src || props.mediaPreview?.fullSrc);
    const hasMarkdown = Boolean(props.markdownPreview);
    return !hasMedia && !hasMarkdown;
  });

  const previewKind = Vue.computed(() => {
    const preview = previewState.value;
    if (!preview) return '';
    return normalizePreviewKind(preview);
  });

  const previewLanguage = Vue.computed(() => {
    const preview = previewState.value;
    if (!preview) return '';
    return normalizePreviewLanguage(preview, previewKind.value);
  });

  const isMarkdownPreview = Vue.computed(() => previewKind.value === 'markdown');
  const isTextPreview = Vue.computed(() => previewKind.value === 'text');
  const isPlainTextPreview = Vue.computed(() => isTextPreview.value && isPlainTextLanguage(previewLanguage.value));
  const isCodeTextPreview = Vue.computed(() => isTextPreview.value && !isPlainTextLanguage(previewLanguage.value));
  
  const markdownHtml = Vue.computed(() => {
    if (!isMarkdownPreview.value) return '';
    return renderAssistantMarkdown(previewState.value?.text || '');
  });
  const textPreviewHtml = Vue.computed(() => {
    if (!isCodeTextPreview.value) return '';
    return renderAssistantMarkdown(buildCodeFence(previewState.value?.text || '', previewLanguage.value));
  });
  const textPreviewPlainText = Vue.computed(() => isPlainTextPreview.value ? normalizePreviewText(previewState.value?.text || '') : '');

  const fileKeyLocal = (file, index = 0) => fileKey(file, index);

  const isFileCollapsed = (file, index = 0) => {
    return collapsedFileKeys.value.includes(fileKeyLocal(file, index));
  };

  const toggleFileCollapsed = (file, index = 0) => {
    const key = fileKeyLocal(file, index);
    if (collapsedFileKeys.value.includes(key)) {
      collapsedFileKeys.value = collapsedFileKeys.value.filter(k => k !== key);
    } else {
      collapsedFileKeys.value = [...collapsedFileKeys.value, key];
    }
  };

  const fileCaretSymbol = (file, index = 0) => {
    return isFileCollapsed(file, index) ? '▸' : '▾';
  };

  const isFocusedFile = (file) => {
    const focus = normalizePath(props.focusFile);
    if (!focus) return false;
    return fileMatchesTarget(file?.filename, focus);
  };

  const isFocusedLine = (file, line) => {
    const target = Number(props.focusLine);
    if (!target) return false;
    if (!isFocusedFile(file)) return false;
    const oldNo = Number(line?.oldNo);
    const newNo = Number(line?.newNo);
    return (Number.isFinite(oldNo) && oldNo === target)
      || (Number.isFinite(newNo) && newNo === target);
  };

  Vue.watch(() => [props.focusFile, props.focusLine], async ([focusFileVal, focusLineVal]) => {
    const focus = normalizePath(focusFileVal);
    if (!focus) return;
    files.value.forEach((file, index) => {
      if (fileMatchesTarget(file?.filename, focus)) {
        const key = fileKeyLocal(file, index);
        collapsedFileKeys.value = collapsedFileKeys.value.filter(k => k !== key);
      }
    });

    await Vue.nextTick();

    const root = panelRef.value;
    if (!root || typeof root.querySelector !== 'function') return;

    const line = root.querySelector('.diff-line.is-focused-line');
    if (line && typeof line.scrollIntoView === 'function') {
      line.scrollIntoView({ behavior: 'smooth', block: 'center' });
      return;
    }

    const file = root.querySelector('.diff-file-group.is-focused .diff-file-header');
    if (file && typeof file.scrollIntoView === 'function') {
      file.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    }
  });

  const onMarkdownPreviewClick = (event) => {
    if (isEditing.value) return;
    if (!isMarkdownPreview.value) return;
    const action = resolveRenderedMarkdownAction(event);
    if (!action) return;
    if (typeof event?.preventDefault === 'function') event.preventDefault();
    if (typeof event?.stopPropagation === 'function') event.stopPropagation();
    if (action.type === 'file-ref') {
      emit?.('file-ref-click', action.payload);
      return;
    }
    if (action.type === 'citation') {
      emit?.('citation-click', action.payload);
    }
  };

  const openLightbox = () => { lightboxOpen.value = true; };
  const closeLightbox = () => { lightboxOpen.value = false; };
  const loadFullDiff = () => { forceFullDiff.value = true; };
  const largeDiffPreviewText = Vue.computed(() => {
    return `当前 diff 约 ${(diffTextLength.value / 1024).toFixed(0)} KB，默认仅展示前 ${(LARGE_DIFF_PREVIEW_CHARS / 1024).toFixed(0)} 字符。`;
  });

  const hasMarkdownPreview = Vue.computed(() => Boolean(previewState.value));

  return {
    files,
    displayFilePath,
    fileCaretSymbol,
    toggleFileCollapsed,
    isFileCollapsed,
    fileCountText,
    hasDiffPreview,
    showLargeDiffPreview,
    largeDiffPreviewText,
    loadFullDiff,
    panelRef,
    openLightbox,
    closeLightbox,
    lightboxOpen,
    isMarkdownPreview,
    markdownHtml,
    isCodeTextPreview,
    textPreviewHtml,
    isPlainTextPreview,
    textPreviewPlainText,
    onMarkdownPreviewClick,
    startEditing,
    isEditing,
    draftText,
    savePreviewChanges,
    cancelEditing,
    saving,
    saveError,
    hasMarkdownPreview,
    isFocusedFile,
    isFocusedLine,
  };
}
