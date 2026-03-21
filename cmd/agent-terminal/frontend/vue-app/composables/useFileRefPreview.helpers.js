import { parseUnifiedDiff } from '../services/diff.js';
import { callAPI } from '../services/api.js';
import { logInfo, logWarn } from '../services/log.js';
import {
  whitespaceTrace,
  buildFocusedDiffSelection,
} from '../utils/diff-utils.js';
import {
  buildSyntheticDiffFromCodeOpen,
  buildTextPreviewFromCodeOpen,
  buildImagePreviewFromCodeOpen,
} from '../utils/preview-utils.js';

/** @typedef {{ line?: number, text?: string }} CodeOpenSnippetLine */
/** @typedef {{ ok?: boolean, relative?: string, filePath?: string, image?: boolean, plugin?: string, mediaType?: string, previewURL?: string, thumbnailURL?: string, sizeBytes?: number, startLine?: number, endLine?: number, totalLines?: number, language?: string, snippet?: string | CodeOpenSnippetLine[] }} CodeOpenResult */

function codeOpenSnippetLength(codeOpenResult) {
  return Array.isArray(codeOpenResult?.snippet)
    ? codeOpenResult.snippet.length
    : (codeOpenResult?.snippet || '').toString().length;
}

export function applyPreviewState(previewState, {
  diffText = '',
  mediaPreview = null,
  markdownPreview = null,
  path = '',
  line = 0,
}) {
  previewState.fallbackDiffText.value = diffText;
  previewState.fallbackMediaPreview.value = mediaPreview;
  previewState.fallbackMarkdownPreview.value = markdownPreview;
  previewState.focusedDiffPath.value = path;
  previewState.focusedDiffLine.value = line;
}

export function getSelectedThreadIdValue(selectedThreadId) {
  return (selectedThreadId?.value || '').toString().trim();
}

export function normalizeFileRefPayload(payload) {
  const rawPath = (payload?.path || '').toString().trim();
  const lineRaw = Number(payload?.line);
  const line = Number.isFinite(lineRaw) && lineRaw > 0 ? Math.floor(lineRaw) : 1;
  const columnRaw = Number(payload?.column);
  const column = Number.isFinite(columnRaw) && columnRaw > 0 ? Math.floor(columnRaw) : 0;
  return { rawPath, line, column };
}

function getCodeOpenProjectContext(props) {
  return {
    activeProject: ((props.projectStore?.state?.active || '.').toString().trim()) || '.',
    projectList: Array.isArray(props.projectStore?.state?.projects)
      ? props.projectStore.state.projects.map((item) => (item || '').toString().trim()).filter(Boolean)
      : [],
  };
}

function buildCodeOpenFallbackCandidates(rawPath) {
  const candidates = [rawPath];
  if (!/[\\/]/.test(rawPath) && /\.log$/i.test(rawPath)) candidates.push(`logs/${rawPath}`);
  return candidates;
}

function normalizePathList(paths) {
  if (!Array.isArray(paths)) return [];
  return paths.map((item) => (item || '').toString().trim()).filter(Boolean);
}

function confirmDirtyPreviewAbandon(confirmAbandonDirtyPreview, meta) {
  if (typeof confirmAbandonDirtyPreview !== 'function') return true;
  return confirmAbandonDirtyPreview(meta);
}

export function confirmFileSwitchOrAbort(options) {
  const {
    confirmAbandonDirtyPreview,
    threadId,
    rawPath,
    line,
    column,
    requestSeq,
  } = options;
  const pendingDirtyConfirmation = confirmDirtyPreviewAbandon(confirmAbandonDirtyPreview, {
    threadId,
    rawPath,
    line,
    column,
  });
  if (typeof pendingDirtyConfirmation?.then === 'function') {
    return pendingDirtyConfirmation.then((confirmed) => {
      if (confirmed !== false) return true;
      logInfo('ui', 'chat.fileRef.dirty_preview.cancelled', {
        thread_id: threadId,
        requested_path: rawPath,
        line,
        column,
        request_seq: requestSeq,
      });
      return false;
    });
  }
  if (pendingDirtyConfirmation !== false) return true;
  logInfo('ui', 'chat.fileRef.dirty_preview.cancelled', {
    thread_id: threadId,
    requested_path: rawPath,
    line,
    column,
    request_seq: requestSeq,
  });
  return false;
}

async function resolveCodeOpenCandidates(options) {
  const {
    props,
    requestPathChoice,
    abortIfStale,
    threadId,
    rawPath,
    line,
    column,
  } = options;
  const projectContext = getCodeOpenProjectContext(props);
  const fallbackCandidates = buildCodeOpenFallbackCandidates(rawPath);
  const locateResult = await callAPI('ui/code/locate', {
    filePath: rawPath,
    project: projectContext.activeProject,
    projects: projectContext.projectList,
  }).catch((err) => {
    logWarn('ui', 'chat.fileRef.code_locate.error', { error: String(err), filePath: rawPath });
    return null;
  });
  if (abortIfStale()) {
    return { aborted: true, cancelled: false, candidates: [], ...projectContext };
  }

  const locatedPaths = normalizePathList(locateResult?.paths);
  const truncated = Boolean(locateResult?.truncated);
  logInfo('ui', 'chat.fileRef.code_locate.result', {
    thread_id: threadId,
    requested_path: rawPath,
    path_count: locatedPaths.length,
    truncated,
    line,
    column,
  });
  if (!locatedPaths.length) {
    return { aborted: false, cancelled: false, candidates: fallbackCandidates, ...projectContext };
  }
  if (locatedPaths.length === 1 || typeof requestPathChoice !== 'function') {
    return { aborted: false, cancelled: false, candidates: [locatedPaths[0]], ...projectContext };
  }

  const selectedPath = await requestPathChoice(locatedPaths, {
    title: `选择 ${rawPath} 的匹配路径`,
    truncated,
  });
  if (abortIfStale()) {
    return { aborted: true, cancelled: false, candidates: [], ...projectContext };
  }
  if (!selectedPath) {
    logInfo('ui', 'chat.fileRef.code_locate.cancelled', {
      thread_id: threadId,
      requested_path: rawPath,
      path_count: locatedPaths.length,
    });
    return { aborted: false, cancelled: true, candidates: [], ...projectContext };
  }

  return {
    aborted: false,
    cancelled: false,
    candidates: [(selectedPath || '').toString().trim()],
    ...projectContext,
  };
}

async function tryCodeOpenCandidates(candidates, callCodeOpen, abortIfStale, context = {}) {
  const {
    threadId = '',
    rawPath = '',
    line = 1,
    column = 0,
  } = context;
  let codeOpenResult = /** @type {CodeOpenResult | null} */ (null);
  let codeOpenInputPath = '';
  let codeOpenError = null;

  for (let index = 0; index < candidates.length; index += 1) {
    const candidatePath = candidates[index];
    logInfo('ui', 'chat.fileRef.code_open.attempt', {
      thread_id: threadId,
      requested_path: rawPath,
      requested_path_meta: whitespaceTrace(rawPath),
      candidate_path: candidatePath,
      candidate_path_meta: whitespaceTrace(candidatePath),
      line,
      column,
      attempt: index + 1,
      total: candidates.length,
    });
    try {
      const result = /** @type {CodeOpenResult | null} */ (await callCodeOpen(candidatePath));
      if (abortIfStale()) {
        return { aborted: true, codeOpenResult: null, codeOpenInputPath: '', codeOpenError: null };
      }
      if (result?.ok) {
        codeOpenResult = result;
        codeOpenInputPath = candidatePath;
        break;
      }
    } catch (error) {
      if (abortIfStale()) {
        return { aborted: true, codeOpenResult: null, codeOpenInputPath: '', codeOpenError: null };
      }
      codeOpenError = error;
      logWarn('ui', 'chat.fileRef.code_open.attempt_failed', {
        thread_id: threadId,
        requested_path: rawPath,
        candidate_path: candidatePath,
        candidate_path_meta: whitespaceTrace(candidatePath),
        line,
        column,
        attempt: index + 1,
        total: candidates.length,
        error,
      });
    }
  }

  return { aborted: false, codeOpenResult, codeOpenInputPath, codeOpenError };
}

export async function restoreCurrentThreadSelection(options) {
  const {
    props,
    threadId,
    activeThreadDiffText,
    rawPath,
    requestSeq,
    abortIfStale,
  } = options;
  let effectiveDiffText = (activeThreadDiffText.value || '').toString();
  let effectiveDiffFiles = parseUnifiedDiff(effectiveDiffText);
  let selection = buildFocusedDiffSelection(effectiveDiffText, rawPath);
  if (selection) {
    return { aborted: false, effectiveDiffText, effectiveDiffFiles, selection };
  }

  const restoreReason = effectiveDiffText.trim() ? 'selection_miss' : 'empty_diff';
  if (typeof props.threadStore?.syncThreadState === 'function') {
    await props.threadStore.syncThreadState(threadId).catch(() => null);
    if (abortIfStale()) {
      return { aborted: true, effectiveDiffText, effectiveDiffFiles, selection: null };
    }
  }
  if (typeof props.threadStore?.syncThreadDiffState === 'function') {
    await props.threadStore.syncThreadDiffState(threadId, { force: true }).catch(() => null);
    if (abortIfStale()) {
      return { aborted: true, effectiveDiffText, effectiveDiffFiles, selection: null };
    }
  }

  effectiveDiffText = (props.threadStore?.getThreadDiff?.(threadId) || activeThreadDiffText.value || '').toString();
  effectiveDiffFiles = parseUnifiedDiff(effectiveDiffText);
  selection = buildFocusedDiffSelection(effectiveDiffText, rawPath);
  logInfo('ui', 'chat.diff.focus.current_thread_restored', {
    thread_id: threadId,
    requested_path: rawPath,
    restore_reason: restoreReason,
    request_seq: requestSeq,
    diff_len: effectiveDiffText.length,
    diff_files: effectiveDiffFiles.length,
    selection_found: Boolean(selection),
  });
  return { aborted: false, effectiveDiffText, effectiveDiffFiles, selection };
}

function dispatchPreviewResult(codeOpenResult, options) {
  const {
    previewState,
    threadId,
    rawPath,
    codeOpenInputPath,
    line,
    column,
  } = options;
  const imagePreview = buildImagePreviewFromCodeOpen(codeOpenResult);
  if (imagePreview) {
    applyPreviewState(previewState, {
      diffText: '',
      mediaPreview: imagePreview,
      markdownPreview: null,
      path: imagePreview.path || rawPath,
      line: 0,
    });
    logInfo('ui', 'chat.diff.focus.image_preview_applied', {
      thread_id: threadId,
      requested_path: rawPath,
      open_input_path: codeOpenInputPath,
      resolved_path: imagePreview.path || rawPath,
      media_type: imagePreview.mediaType,
      size_bytes: imagePreview.sizeBytes,
    });
    return true;
  }

  const markdownPreview = buildTextPreviewFromCodeOpen(codeOpenResult);
  if (markdownPreview) {
    applyPreviewState(previewState, {
      diffText: '',
      mediaPreview: null,
      markdownPreview,
      path: markdownPreview.path || rawPath,
      line,
    });
    logInfo('ui', 'chat.diff.focus.markdown_preview_applied', {
      thread_id: threadId,
      requested_path: rawPath,
      open_input_path: codeOpenInputPath,
      resolved_path: markdownPreview.path || rawPath,
      start_line: markdownPreview.startLine,
      end_line: markdownPreview.endLine,
      total_lines: markdownPreview.totalLines,
    });
    return true;
  }

  const syntheticDiff = buildSyntheticDiffFromCodeOpen(codeOpenResult);
  const resolvedPath = (codeOpenResult?.relative || codeOpenResult?.filePath || codeOpenInputPath || rawPath).toString().trim();
  if (syntheticDiff && resolvedPath) {
    applyPreviewState(previewState, {
      diffText: syntheticDiff,
      mediaPreview: null,
      markdownPreview: null,
      path: resolvedPath,
      line,
    });
    logInfo('ui', 'chat.diff.focus.code_open_applied', {
      thread_id: threadId,
      requested_path: rawPath,
      open_input_path: codeOpenInputPath,
      resolved_path: resolvedPath,
      line,
      column,
      snippet_start: Number(codeOpenResult?.startLine) || 0,
      snippet_end: Number(codeOpenResult?.endLine) || 0,
      snippet_len: codeOpenSnippetLength(codeOpenResult),
    });
    return true;
  }

  return false;
}

export async function openSelectionFallbackPreview(options) {
  const {
    props,
    previewState,
    requestPathChoice,
    abortIfStale,
    threadId,
    rawPath,
    line,
    column,
    effectiveDiffText,
    effectiveDiffFiles,
  } = options;
  const {
    aborted: locateAborted,
    cancelled,
    candidates: codeOpenCandidates,
    activeProject,
    projectList,
  } = await resolveCodeOpenCandidates({
    props,
    requestPathChoice,
    abortIfStale,
    threadId,
    rawPath,
    line,
    column,
  });
  if (locateAborted || cancelled) return;

  logInfo('ui', 'chat.fileRef.code_open.candidates', {
    thread_id: threadId,
    requested_path: rawPath,
    candidates: codeOpenCandidates.map((item) => whitespaceTrace(item)),
    line,
    column,
  });

  const {
    aborted,
    codeOpenResult,
    codeOpenInputPath,
    codeOpenError,
  } = await tryCodeOpenCandidates(
    codeOpenCandidates,
    (candidatePath) => callAPI('ui/code/open', {
      filePath: candidatePath,
      line,
      column,
      project: activeProject,
      projects: projectList,
    }),
    abortIfStale,
    { threadId, rawPath, line, column },
  );
  if (aborted) return;

  if (codeOpenResult?.ok && dispatchPreviewResult(codeOpenResult, {
    previewState,
    threadId,
    rawPath,
    codeOpenInputPath,
    line,
    column,
  })) {
    return;
  }
  if (!codeOpenResult?.ok && codeOpenError) {
    logWarn('ui', 'chat.diff.focus.code_open.failed', {
      thread_id: threadId,
      requested_path: rawPath,
      requested_path_meta: whitespaceTrace(rawPath),
      line,
      column,
      tried_paths: codeOpenCandidates,
      tried_paths_meta: codeOpenCandidates.map((item) => whitespaceTrace(item)),
      error: codeOpenError,
    });
  }

  logWarn('ui', 'chat.diff.focus.miss', {
    thread_id: threadId,
    requested_path: rawPath,
    line,
    diff_len: effectiveDiffText.length,
    diff_files: effectiveDiffFiles.length,
  });
  applyPreviewState(previewState, {
    diffText: '',
    mediaPreview: null,
    markdownPreview: null,
    path: rawPath,
    line,
  });
  logInfo('ui', 'chat.diff.focus.fallback_applied', { thread_id: threadId, path: rawPath, line });
}
