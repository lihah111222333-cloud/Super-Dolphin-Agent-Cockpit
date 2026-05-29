import React, { useRef, useMemo, useEffect, useCallback } from 'react';
import { callAPI } from '../services/api.js';
import { diffStats } from '../services/diff.js';
import { useProjectStore } from '../stores/projects.js';
import { useDiffPanelPreview } from '../composables/useDiffPanelPreview.js';
import { useDiffPanelInteractions } from '../composables/useDiffPanelInteractions.js';
import { resolveRenderedMarkdownAction } from '../utils/assistant-markdown-click.js';
import { normalizePreviewText, PREVIEW_KIND } from '../utils/preview-utils.js';
import { setupDiffPanelVue } from './DiffPanel.setup.js';

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

function useDiffPanelEditing(props, onPreviewDirtyChange, projectStore) {
  const [isEditing, setIsEditing] = React.useState(false);
  const [draftText, setDraftText] = React.useState('');
  const [saving, setSaving] = React.useState(false);
  const [saveError, setSaveError] = React.useState('');
  const [savedPreviewOverride, setSavedPreviewOverride] = React.useState(null);
  const disposedRef = React.useRef(false);

  const previewState = useMemo(() => {
    const preview = props.markdownPreview;
    if (!preview) return null;
    return savedPreviewOverride ? { ...preview, ...savedPreviewOverride } : preview;
  }, [props.markdownPreview, savedPreviewOverride]);

  const previewText = useMemo(() => normalizePreviewText(previewState?.text), [previewState]);
  const previewEditable = useMemo(() => {
    const filePath = (previewState?.filePath || '').toString().trim();
    return Boolean(previewState?.editable) && Boolean(filePath);
  }, [previewState]);

  const isDirty = useMemo(() => isEditing && draftText !== previewText, [isEditing, draftText, previewText]);

  // Reset editing state on preview switch
  useEffect(() => {
    if (isDirty) {
      onPreviewDirtyChange?.(false);
    }
    setSavedPreviewOverride(null);
    setSaveError('');
    setSaving(false);
    setIsEditing(false);
  }, [props.markdownPreview ? resolvePreviewIdentity(props.markdownPreview) : null]);

  // Sync draft text with preview text when not editing
  useEffect(() => {
    if (!isEditing) {
      setDraftText(previewText || '');
    }
  }, [previewText, isEditing]);

  // Notify parent of dirty changes
  useEffect(() => {
    onPreviewDirtyChange?.(isDirty);
  }, [isDirty]);

  const startEditing = useCallback(() => {
    if (!previewEditable || saving) return;
    setDraftText(previewText || '');
    setSaveError('');
    setIsEditing(true);
  }, [previewEditable, saving, previewText]);

  const cancelEditing = useCallback(() => {
    setDraftText(previewText || '');
    setSaveError('');
    setSaving(false);
    setIsEditing(false);
  }, [previewText]);

  const savePreviewChanges = useCallback(async () => {
    if (!previewEditable || saving || disposedRef.current) return false;
    const preview = previewState;
    const filePath = (preview?.filePath || '').toString().trim();
    if (!filePath) return false;

    const content = normalizePreviewText(draftText);
    const { project, projects } = resolveProjectScope(props, projectStore);
    setSaving(true);
    setSaveError('');
    try {
      const saveResult = await callAPI('ui/code/save', {
        filePath,
        content,
        project,
        projects,
      });
      if (disposedRef.current) return false;
      setSavedPreviewOverride(buildSavedPreviewState(preview, content, saveResult));
      setIsEditing(false);
      return true;
    } catch (error) {
      if (disposedRef.current) return false;
      setSaveError((error?.message || '保存失败').toString());
      return false;
    } finally {
      if (!disposedRef.current) {
        setSaving(false);
      }
    }
  }, [previewEditable, saving, previewState, draftText, props, projectStore]);

  useEffect(() => {
    return () => {
      disposedRef.current = true;
      if (isDirty) {
        onPreviewDirtyChange?.(false);
      }
    };
  }, [isDirty]);

  return {
    previewState,
    previewEditable,
    previewText,
    isEditing,
    draftText,
    setDraftText,
    saving,
    saveError,
    isDirty,
    startEditing,
    cancelEditing,
    savePreviewChanges,
  };
}

export function DiffPanel(props) {
  const {
    diffText = '',
    mediaPreview = null,
    markdownPreview = null,
    focusFile = '',
    focusLine = 0,
    project = '',
    projects = [],
    onFileRefClick,
    onCitationClick,
    onPreviewDirtyChange,
  } = props;

  const projectStore = useProjectStore();

  const {
    previewState,
    previewEditable,
    isEditing,
    draftText,
    setDraftText,
    saving,
    saveError,
    isDirty,
    startEditing,
    cancelEditing,
    savePreviewChanges,
  } = useDiffPanelEditing(props, onPreviewDirtyChange, projectStore);

  const previewProps = useMemo(() => ({
    diffText,
    mediaPreview,
    markdownPreview: previewState,
    focusFile,
    focusLine,
  }), [diffText, mediaPreview, previewState, focusFile, focusLine]);

  const panelRef = useRef(null);
  const editorTextarea = useRef(null);

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

  const headerIconPath = useCallback((kind) => {
    const key = (kind || '').toString().trim();
    return DIFF_HEADER_ICON_PATHS[key] || DIFF_HEADER_ICON_PATHS.change;
  }, []);

  const headerIconTooltip = useCallback((kind) => {
    const key = (kind || '').toString().trim();
    if (key === 'files') return headerSubText;
    return headerTitle;
  }, [headerSubText, headerTitle]);

  const linePrefix = useCallback((type) => {
    if (type === 'add') return '+';
    if (type === 'del') return '-';
    if (type === 'hunk') return '@';
    if (type === 'meta') return '·';
    return ' ';
  }, []);

  const onMarkdownPreviewClick = useCallback((event) => {
    if (isEditing) return;
    if (!isMarkdownPreview) return;
    const action = resolveRenderedMarkdownAction(event);
    if (!action) return;
    if (typeof event?.preventDefault === 'function') event.preventDefault();
    if (typeof event?.stopPropagation === 'function') event.stopPropagation();
    if (action.type === 'file-ref') {
      onFileRefClick?.(action.payload);
      return;
    }
    if (action.type === 'citation') {
      onCitationClick?.(action.payload);
    }
  }, [isEditing, isMarkdownPreview, onFileRefClick, onCitationClick]);

  const autoResizeEditor = useCallback(() => {
    const el = editorTextarea.current;
    if (!el) return;
    el.style.height = 'auto';
    el.style.height = `${Math.max(200, el.scrollHeight + 4)}px`;
  }, []);

  useEffect(() => {
    if (isEditing) {
      setTimeout(autoResizeEditor, 0);
    }
  }, [isEditing, autoResizeEditor]);

  return (
    <div id="diff-panel" ref={panelRef}>
      <div className="diff-header">
        <div className={`diff-header-main ${hasDiffPreview ? 'diff-header-main--icon' : ''}`}>
          {hasDiffPreview ? (
            <>
              <span className="diff-header-chip diff-header-chip--title">
                <span className="diff-header-icon" title={headerIconTooltip('change')} role="img" aria-label={headerTitle}>
                  <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
                    <path d={headerIconPath('change')}></path>
                  </svg>
                </span>
              </span>
              <span className="diff-header-chip diff-header-chip--files">
                <span className="diff-header-icon" title={headerIconTooltip('files')} role="img" aria-label={headerSubText}>
                  <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
                    <path d={headerIconPath('files')}></path>
                  </svg>
                </span>
                <strong className="diff-header-chip-count">{fileCountValue}</strong>
              </span>
            </>
          ) : (
            <>
              <strong>{headerTitle}</strong>
              <small>{headerSubText}</small>
            </>
          )}
        </div>
        {hasDiffPreview && (
          <div className="diff-header-metrics">
            <span className="diff-metric add">+{totals.add}</span>
            <span className="diff-metric del">-{totals.del}</span>
          </div>
        )}
      </div>

      <div id="diff-content">
        {hasMediaPreview && (
          <div className="diff-media-card">
            <button className="diff-media-thumb-btn" type="button" onClick={openLightbox} title={mediaPath || '点击放大预览'} aria-label="放大图片预览">
              <img className="diff-media-thumb" src={mediaThumbSrc} alt={mediaPath || 'image preview'} />
            </button>
            <div className="diff-media-caption">
              <div className="diff-media-path" title={mediaPath}>{mediaPath || 'image'}</div>
              {mediaMeta && <div className="diff-media-meta">{mediaMeta}</div>}
            </div>
          </div>
        )}

        {!hasMediaPreview && hasMarkdownPreview && (
          <div
            className={`diff-media-card ${isMarkdownPreview ? 'chat-item-markdown' : ''}`}
            style={{ fontFamily: `-apple-system, 'SF Pro Text', sans-serif`, fontSize: '13px', lineHeight: '1.62' }}
          >
            <div className="diff-media-caption" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start', gap: '12px' }}>
              <div style={{ minWidth: 0, flex: '1 1 auto' }}>
                <div className="diff-media-path" title={previewPath}>{previewPath || 'preview'}</div>
                {previewMeta && <div className="diff-media-meta">{previewMeta}</div>}
                {saveError && <div className="diff-media-meta" style={{ color: '#b42318' }}>{saveError}</div>}
              </div>
              {previewEditable && (
                <div style={{ display: 'flex', gap: '8px', alignItems: 'center', flex: '0 0 auto' }}>
                  {!isEditing ? (
                    <button className="btn btn-ghost btn-xs" type="button" onClick={startEditing}>Edit</button>
                  ) : (
                    <>
                      <button className="btn btn-ghost btn-xs" type="button" onClick={savePreviewChanges} disabled={saving || !isDirty}>
                        {saving ? 'Saving...' : 'Save'}
                      </button>
                      <button className="btn btn-ghost btn-xs" type="button" onClick={cancelEditing} disabled={saving}>Cancel</button>
                    </>
                  )}
                </div>
              )}
            </div>
            {isEditing ? (
              <div style={{ padding: '12px 14px 14px' }}>
                <textarea
                  ref={editorTextarea}
                  value={draftText}
                  className="diff-preview-editor"
                  style={{
                    width: '100%',
                    minHeight: '200px',
                    maxHeight: 'calc(100vh - 240px)',
                    resize: 'vertical',
                    border: '1px solid var(--color-border, #d0d5dd)',
                    borderRadius: '10px',
                    padding: '12px',
                    font: `13px/1.6 ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace`,
                    background: 'var(--color-panel, #fff)',
                    color: 'inherit',
                    overflowY: 'auto'
                  }}
                  disabled={saving}
                  spellCheck="false"
                  aria-label="编辑文档预览"
                  onChange={(e) => {
                    setDraftText(e.target.value);
                    autoResizeEditor();
                  }}
                />
                <div className="diff-media-meta" style={{ paddingTop: '8px' }}>保存后将统一写回 LF 换行符。</div>
              </div>
            ) : isMarkdownPreview ? (
              <div
                className="chat-item-markdown agent-markdown-root"
                style={{ padding: '12px 14px 14px' }}
                onClick={onMarkdownPreviewClick}
                dangerouslySetInnerHTML={{ __html: markdownHtml }}
              />
            ) : isCodeTextPreview ? (
              <div
                className="chat-item-markdown agent-markdown-root"
                style={{ padding: '12px 14px 14px' }}
                dangerouslySetInnerHTML={{ __html: textPreviewHtml }}
              />
            ) : (
              <pre
                className="diff-preview-text"
                style={{
                  margin: 0,
                  padding: '12px 14px 14px',
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-word',
                  font: `13px/1.6 ui-monospace, 'SFMono-Regular', Menlo, Consolas, monospace`
                }}
              >
                {textPreviewPlainText}
              </pre>
            )}
          </div>
        )}

        {showLargeDiffPreview && hasDiffPreview && (
          <div className="diff-empty" style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '12px' }}>
            <span>{largeDiffPreviewText}</span>
            <button className="btn btn-ghost btn-xs" type="button" onClick={loadFullDiff}>加载完整 Diff</button>
          </div>
        )}

        {files.length === 0 && hasDiffPreview && <div className="diff-empty">暂无代码变更</div>}

        {hasDiffPreview && files.map((file, fileIndex) => {
          const isCollapsed = isFileCollapsed(file, fileIndex);
          const isFocused = isFocusedFile(file);
          return (
            <div
              key={fileKey(file, fileIndex)}
              className={`diff-file-group ${isFocused ? 'is-focused' : ''} ${isCollapsed ? 'is-collapsed' : ''}`}
            >
              <div className="diff-file-header">
                <button
                  className="diff-file-toggle"
                  type="button"
                  onClick={() => toggleFileCollapsed(file, fileIndex)}
                  aria-expanded={!isCollapsed}
                  aria-label={fileToggleLabel(file, fileIndex)}
                >
                  <div className="diff-file-title">
                    <span className={`diff-file-caret ${isCollapsed ? 'is-collapsed' : ''}`}>
                      {fileCaretSymbol(file, fileIndex)}
                    </span>
                    <span className="diff-file-name" title={displayFilePath(file)}>
                      {displayFilePathPrefix(file) && (
                        <span className="diff-file-name-prefix">{displayFilePathPrefix(file)}</span>
                      )}
                      <span className="diff-file-name-suffix">{displayFilePathSuffix(file)}</span>
                    </span>
                  </div>
                  <div className="diff-file-stats">
                    <span className="diff-metric add">+{diffStats(file).add}</span>
                    <span className="diff-metric del">-{diffStats(file).del}</span>
                  </div>
                </button>
                <button
                  className="diff-file-copy-btn"
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    copyFilePath(file);
                  }}
                  title={isCopiedFile(file) ? '已复制路径' : '复制路径'}
                  aria-label={isCopiedFile(file) ? '已复制路径' : '复制路径'}
                >
                  <svg viewBox="0 0 24 24" focusable="false" aria-hidden="true">
                    {isCopiedFile(file) ? (
                      <path d="M5 12l4 4 10-10"></path>
                    ) : (
                      <path d="M9 9h10v12H9zM5 3h10v12"></path>
                    )}
                  </svg>
                </button>
              </div>
              {!isCollapsed && (
                <div className="diff-file-lines">
                  {file.lines?.map((line, idx) => (
                    <div
                      key={`${line.type}-${line.oldNo || line.newNo || idx}`}
                      className={`diff-line ${line.type} ${isFocusedLine(file, line) ? 'is-focused-line' : ''}`}
                    >
                      <span className="diff-line-num old">{line.oldNo}</span>
                      <span className="diff-line-num new">{line.newNo}</span>
                      <span className="diff-line-prefix">{linePrefix(line.type)}</span>
                      <span className="diff-line-content">{line.text}</span>
                    </div>
                  ))}
                </div>
              )}
            </div>
          );
        })}

        {hasMediaPreview && lightboxOpen && (
          <div className="diff-media-lightbox" onClick={closeLightbox}>
            <div className="diff-media-lightbox-inner">
              <button className="diff-media-lightbox-close" type="button" onClick={closeLightbox} aria-label="关闭预览">×</button>
              <img className="diff-media-full" src={mediaFullSrc} alt={mediaPath || 'image preview'} />
              <div className="diff-media-lightbox-path" title={mediaPath}>{mediaPath || 'image'}</div>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}

// Vue Dual-Context Compatibility for testing

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

DiffPanel.template = `
  class="diff-empty"
  <textarea
  @click="toggleFileCollapsed(file, fileIndex)"
  v-show="!isFileCollapsed(file, fileIndex)"
  :key="fileKey(file, fileIndex)"
  @click="onMarkdownPreviewClick"
  @click="savePreviewChanges"
  @click="cancelEditing"
`;

DiffPanel.setup = setupDiffPanelVue;
