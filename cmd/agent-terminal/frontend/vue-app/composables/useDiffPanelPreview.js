import { computed, ref, watch } from '../../lib/vue.esm-browser.prod.js';
import { parseUnifiedDiff, diffStats } from '../services/diff.js';
import { logInfo } from '../services/log.js';
import { renderAssistantMarkdown } from '../utils/assistant-markdown.js';
import { isMarkdownPath, PREVIEW_KIND, normalizePreviewText } from '../utils/preview-utils.js';

const LARGE_DIFF_PREVIEW_THRESHOLD = 120000;
const LARGE_DIFF_PREVIEW_CHARS = 40000;

/**
 * @typedef {{
 *   diffText?: string,
 *   mediaPreview?: any,
 *   markdownPreview?: any,
 *   focusFile?: string,
 *   focusLine?: number,
 * }} DiffPanelPreviewProps
 */

function formatBytes(value) {
  const size = Number(value);
  if (!Number.isFinite(size) || size <= 0) return '';
  if (size >= 1024 * 1024) {
    return `${(size / (1024 * 1024)).toFixed(2)} MB`;
  }
  if (size >= 1024) {
    return `${(size / 1024).toFixed(1)} KB`;
  }
  return `${size} B`;
}

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

/**
 * @param {DiffPanelPreviewProps} props
 */
export function useDiffPanelPreview(props) {
  const lightboxOpen = ref(false);
  const forceFullDiff = ref(false);

  const diffTextLength = computed(() => (props.diffText || '').length);
  const hasRequestedFocus = computed(() => Boolean((props.focusFile || '').toString().trim()) || Number(props.focusLine) > 0);
  const showLargeDiffPreview = computed(() => diffTextLength.value > LARGE_DIFF_PREVIEW_THRESHOLD && !forceFullDiff.value && !hasRequestedFocus.value);
  const diffTextForDisplay = computed(() => (showLargeDiffPreview.value ? (props.diffText || '').slice(0, LARGE_DIFF_PREVIEW_CHARS) : (props.diffText || '')));
  const files = computed(() => parseUnifiedDiff(diffTextForDisplay.value));
  const fileCountText = computed(() => `${files.value.length} file${files.value.length === 1 ? '' : 's'}`);
  const totals = computed(() => files.value.reduce(
    (acc, file) => {
      const stats = diffStats(file);
      acc.add += stats.add;
      acc.del += stats.del;
      return acc;
    },
    { add: 0, del: 0 },
  ));

  const hasMediaPreview = computed(() => {
    const src = (props.mediaPreview?.src || '').toString().trim();
    const fullSrc = (props.mediaPreview?.fullSrc || '').toString().trim();
    return Boolean(src || fullSrc);
  });
  const mediaThumbSrc = computed(() => {
    const src = (props.mediaPreview?.src || '').toString().trim();
    return src || (props.mediaPreview?.fullSrc || '').toString().trim();
  });
  const mediaFullSrc = computed(() => {
    const full = (props.mediaPreview?.fullSrc || '').toString().trim();
    return full || (props.mediaPreview?.src || '').toString().trim();
  });
  const mediaPath = computed(() => (props.mediaPreview?.path || '').toString().trim());
  const mediaType = computed(() => (props.mediaPreview?.mediaType || '').toString().trim());
  const mediaBytes = computed(() => {
    const size = Number(props.mediaPreview?.sizeBytes);
    return Number.isFinite(size) && size > 0 ? Math.floor(size) : 0;
  });
  const mediaMeta = computed(() => {
    const typeLabel = mediaType.value ? `${mediaType.value}` : '';
    const sizeLabel = mediaBytes.value > 0 ? `${formatBytes(mediaBytes.value)}` : '';
    return [typeLabel, sizeLabel].filter(Boolean).join(' · ');
  });

  const textPreview = computed(() => props.markdownPreview || null);
  const hasMarkdownPreview = computed(() => {
    const text = normalizePreviewText(textPreview.value?.text);
    return Boolean(text.trim());
  });
  const previewKind = computed(() => normalizePreviewKind(textPreview.value));
  const isMarkdownPreview = computed(() => hasMarkdownPreview.value && previewKind.value === 'markdown');
  const isTextPreview = computed(() => hasMarkdownPreview.value && previewKind.value === 'text');
  const previewPath = computed(() => (textPreview.value?.path || '').toString().trim());
  const previewFilePath = computed(() => (textPreview.value?.filePath || '').toString().trim());
  const previewText = computed(() => normalizePreviewText(textPreview.value?.text));
  const previewLanguage = computed(() => normalizePreviewLanguage(textPreview.value, previewKind.value));
  const previewMeta = computed(() => {
    if (!hasMarkdownPreview.value) return '';
    const startLineRaw = Number(textPreview.value?.startLine);
    const endLineRaw = Number(textPreview.value?.endLine);
    const totalLinesRaw = Number(textPreview.value?.totalLines);
    const startLine = Number.isFinite(startLineRaw) && startLineRaw > 0 ? Math.floor(startLineRaw) : 0;
    const endLine = Number.isFinite(endLineRaw) && endLineRaw >= startLine ? Math.floor(endLineRaw) : 0;
    const totalLines = Number.isFinite(totalLinesRaw) && totalLinesRaw > 0 ? Math.floor(totalLinesRaw) : 0;
    if (startLine <= 0 || endLine < startLine) return '';
    const range = startLine === endLine
      ? `第 ${startLine} 行`
      : `第 ${startLine}-${endLine} 行`;
    if (totalLines > 0 && endLine < totalLines) {
      return `${range}（片段，共 ${totalLines} 行）`;
    }
    if (totalLines > 0) {
      return `${range}（共 ${totalLines} 行）`;
    }
    return range;
  });
  const markdownHtml = computed(() => {
    if (!isMarkdownPreview.value) return '';
    return renderAssistantMarkdown(previewText.value);
  });
  const isPlainTextPreview = computed(() => isTextPreview.value && isPlainTextLanguage(previewLanguage.value));
  const isCodeTextPreview = computed(() => isTextPreview.value && !isPlainTextPreview.value);
  const textPreviewHtml = computed(() => {
    if (!isCodeTextPreview.value) return '';
    return renderAssistantMarkdown(buildCodeFence(previewText.value, previewLanguage.value));
  });
  const textPreviewPlainText = computed(() => (isPlainTextPreview.value ? previewText.value : ''));

  const hasDiffPreview = computed(() => !hasMediaPreview.value && !hasMarkdownPreview.value);
  const headerTitle = computed(() => (hasMarkdownPreview.value ? '文档预览' : '代码变更'));
  const largeDiffPreviewHint = computed(() => (showLargeDiffPreview.value ? '大变更预览' : ''));
  const headerSubText = computed(() => (
    hasMarkdownPreview.value
      ? (previewPath.value || 'preview')
      : [fileCountText.value, largeDiffPreviewHint.value].filter(Boolean).join(' · ')
  ));
  const fileCountValue = computed(() => files.value.length);
  const largeDiffPreviewText = computed(() => (
    showLargeDiffPreview.value
      ? `当前 diff 约 ${Math.max(1, Math.round(diffTextLength.value / 1024))} KB，已先显示前 ${Math.round(LARGE_DIFF_PREVIEW_CHARS / 1024)} KB 预览。`
      : ''
  ));

  watch(
    () => props.diffText,
    (next, prev) => {
      if (next === prev) return;
      forceFullDiff.value = false;
    },
    { immediate: true },
  );

  watch(
    () => props.mediaPreview,
    () => {
      lightboxOpen.value = false;
    },
    { deep: true },
  );

  function openLightbox() {
    if (!mediaFullSrc.value) return;
    lightboxOpen.value = true;
  }

  function closeLightbox() {
    lightboxOpen.value = false;
  }

  function loadFullDiff() {
    if (!showLargeDiffPreview.value) return;
    forceFullDiff.value = true;
    logInfo('ui', 'chat.diff.panel.preview.expand', {
      diff_len: diffTextLength.value,
    });
  }

  return {
    lightboxOpen,
    forceFullDiff,
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
    previewFilePath,
    previewText,
    previewLanguage,
    isMarkdownPreview,
    isTextPreview,
    isPlainTextPreview,
    isCodeTextPreview,
    previewMeta,
    markdownHtml,
    textPreviewHtml,
    textPreviewPlainText,
    markdownPath: previewPath,
    markdownMeta: previewMeta,
    hasDiffPreview,
    headerTitle,
    headerSubText,
    fileCountValue,
    largeDiffPreviewText,
    openLightbox,
    closeLightbox,
    loadFullDiff,
  };
}
