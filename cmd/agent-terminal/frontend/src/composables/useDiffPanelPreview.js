import { useState, useMemo, useEffect } from 'react';
import { parseUnifiedDiff, diffStats } from '../services/diff.js';
import { logInfo } from '../services/log.js';
import { renderAssistantMarkdown } from '../utils/assistant-markdown.js';
import { isMarkdownPath, PREVIEW_KIND, normalizePreviewText } from '../utils/preview-utils.js';

const LARGE_DIFF_PREVIEW_THRESHOLD = 120000;
const LARGE_DIFF_PREVIEW_CHARS = 40000;

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

export function useDiffPanelPreview(props) {
  const [lightboxOpen, setLightboxOpen] = useState(false);
  const [forceFullDiff, setForceFullDiff] = useState(false);

  const diffTextLength = useMemo(() => (props.diffText || '').length, [props.diffText]);
  const hasRequestedFocus = useMemo(() => Boolean((props.focusFile || '').toString().trim()) || Number(props.focusLine) > 0, [props.focusFile, props.focusLine]);
  const showLargeDiffPreview = useMemo(() => diffTextLength > LARGE_DIFF_PREVIEW_THRESHOLD && !forceFullDiff && !hasRequestedFocus, [diffTextLength, forceFullDiff, hasRequestedFocus]);
  const diffTextForDisplay = useMemo(() => (showLargeDiffPreview ? (props.diffText || '').slice(0, LARGE_DIFF_PREVIEW_CHARS) : (props.diffText || '')), [showLargeDiffPreview, props.diffText]);
  const files = useMemo(() => parseUnifiedDiff(diffTextForDisplay), [diffTextForDisplay]);
  const fileCountText = useMemo(() => `${files.length} file${files.length === 1 ? '' : 's'}`, [files]);
  const totals = useMemo(() => files.reduce(
    (acc, file) => {
      const stats = diffStats(file);
      acc.add += stats.add;
      acc.del += stats.del;
      return acc;
    },
    { add: 0, del: 0 },
  ), [files]);

  const hasMediaPreview = useMemo(() => {
    const src = (props.mediaPreview?.src || '').toString().trim();
    const fullSrc = (props.mediaPreview?.fullSrc || '').toString().trim();
    return Boolean(src || fullSrc);
  }, [props.mediaPreview]);
  const mediaThumbSrc = useMemo(() => {
    const src = (props.mediaPreview?.src || '').toString().trim();
    return src || (props.mediaPreview?.fullSrc || '').toString().trim();
  }, [props.mediaPreview]);
  const mediaFullSrc = useMemo(() => {
    const full = (props.mediaPreview?.fullSrc || '').toString().trim();
    return full || (props.mediaPreview?.src || '').toString().trim();
  }, [props.mediaPreview]);
  const mediaPath = useMemo(() => (props.mediaPreview?.path || '').toString().trim(), [props.mediaPreview]);
  const mediaType = useMemo(() => (props.mediaPreview?.mediaType || '').toString().trim(), [props.mediaPreview]);
  const mediaBytes = useMemo(() => {
    const size = Number(props.mediaPreview?.sizeBytes);
    return Number.isFinite(size) && size > 0 ? Math.floor(size) : 0;
  }, [props.mediaPreview]);
  const mediaMeta = useMemo(() => {
    const typeLabel = mediaType ? `${mediaType}` : '';
    const sizeLabel = mediaBytes > 0 ? `${formatBytes(mediaBytes)}` : '';
    return [typeLabel, sizeLabel].filter(Boolean).join(' · ');
  }, [mediaType, mediaBytes]);

  const textPreview = useMemo(() => props.markdownPreview || null, [props.markdownPreview]);
  const hasMarkdownPreview = useMemo(() => {
    const text = normalizePreviewText(textPreview?.text);
    return Boolean(text.trim());
  }, [textPreview]);
  const previewKind = useMemo(() => normalizePreviewKind(textPreview), [textPreview]);
  const isMarkdownPreview = useMemo(() => hasMarkdownPreview && previewKind === 'markdown', [hasMarkdownPreview, previewKind]);
  const isTextPreview = useMemo(() => hasMarkdownPreview && previewKind === 'text', [hasMarkdownPreview, previewKind]);
  const previewPath = useMemo(() => (textPreview?.path || '').toString().trim(), [textPreview]);
  const previewFilePath = useMemo(() => (textPreview?.filePath || '').toString().trim(), [textPreview]);
  const previewText = useMemo(() => normalizePreviewText(textPreview?.text), [textPreview]);
  const previewLanguage = useMemo(() => normalizePreviewLanguage(textPreview, previewKind), [textPreview, previewKind]);
  const previewMeta = useMemo(() => {
    if (!hasMarkdownPreview) return '';
    const startLineRaw = Number(textPreview?.startLine);
    const endLineRaw = Number(textPreview?.endLine);
    const totalLinesRaw = Number(textPreview?.totalLines);
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
  }, [hasMarkdownPreview, textPreview]);
  const markdownHtml = useMemo(() => {
    if (!isMarkdownPreview) return '';
    return renderAssistantMarkdown(previewText);
  }, [isMarkdownPreview, previewText]);
  const isPlainTextPreview = useMemo(() => isTextPreview && isPlainTextLanguage(previewLanguage), [isTextPreview, previewLanguage]);
  const isCodeTextPreview = useMemo(() => isTextPreview && !isPlainTextPreview, [isTextPreview, isPlainTextPreview]);
  const textPreviewHtml = useMemo(() => {
    if (!isCodeTextPreview) return '';
    return renderAssistantMarkdown(buildCodeFence(previewText, previewLanguage));
  }, [isCodeTextPreview, previewText, previewLanguage]);
  const textPreviewPlainText = useMemo(() => (isPlainTextPreview ? previewText : ''), [isPlainTextPreview, previewText]);

  const hasDiffPreview = useMemo(() => !hasMediaPreview && !hasMarkdownPreview, [hasMediaPreview, hasMarkdownPreview]);
  const headerTitle = useMemo(() => (hasMarkdownPreview ? '文档预览' : '代码变更'), [hasMarkdownPreview]);
  const largeDiffPreviewHint = useMemo(() => (showLargeDiffPreview ? '大变更预览' : ''), [showLargeDiffPreview]);
  const headerSubText = useMemo(() => (
    hasMarkdownPreview
      ? (previewPath || 'preview')
      : [fileCountText, largeDiffPreviewHint].filter(Boolean).join(' · ')
  ), [hasMarkdownPreview, previewPath, fileCountText, largeDiffPreviewHint]);
  const fileCountValue = useMemo(() => files.length, [files]);
  const largeDiffPreviewText = useMemo(() => (
    showLargeDiffPreview
      ? `当前 diff 约 ${Math.max(1, Math.round(diffTextLength / 1024))} KB，已先显示前 ${Math.round(LARGE_DIFF_PREVIEW_CHARS / 1024)} KB 预览。`
      : ''
  ), [showLargeDiffPreview, diffTextLength]);

  useEffect(() => {
    setForceFullDiff(false);
  }, [props.diffText]);

  useEffect(() => {
    setLightboxOpen(false);
  }, [props.mediaPreview]);

  function openLightbox() {
    if (!mediaFullSrc) return;
    setLightboxOpen(true);
  }

  function closeLightbox() {
    setLightboxOpen(false);
  }

  function loadFullDiff() {
    if (!showLargeDiffPreview) return;
    setForceFullDiff(true);
    logInfo('ui', 'chat.diff.panel.preview.expand', {
      diff_len: diffTextLength,
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
