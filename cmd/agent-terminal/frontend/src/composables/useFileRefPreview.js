import { parseUnifiedDiff } from '../services/diff.js';
import { logInfo, logWarn } from '../services/log.js';
import { resolveCitationImagePreview } from '../utils/citation-preview-utils.js';
import {
  applyPreviewState,
  confirmFileSwitchOrAbort,
  getSelectedThreadIdValue,
  normalizeFileRefPayload,
  openSelectionFallbackPreview,
  restoreCurrentThreadSelection,
} from './useFileRefPreview.helpers.js';

/**
 * @param {object} props
 * @param {object} deps
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<string>} deps.selectedThreadId
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<Array<any>>} deps.activeTimeline
 * @param {import('../../lib/vue.esm-browser.prod.js').ComputedRef<string>} deps.activeThreadDiffText
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<string>} deps.focusedDiffPath
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<number>} deps.focusedDiffLine
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<string>} deps.fallbackDiffText
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<object|null>} deps.fallbackMediaPreview
 * @param {import('../../lib/vue.esm-browser.prod.js').Ref<object|null>} deps.fallbackMarkdownPreview
 * @param {(options: string[], meta?: { title?: string, truncated?: boolean }) => Promise<string>} [deps.requestPathChoice]
 * @param {(meta?: { threadId: string, rawPath: string, line: number, column: number }) => boolean | Promise<boolean>} [deps.confirmAbandonDirtyPreview]
 */
export function useFileRefPreview(props, deps) {
  const {
    selectedThreadId,
    activeTimeline,
    activeThreadDiffText,
    focusedDiffPath,
    focusedDiffLine,
    fallbackDiffText,
    fallbackMediaPreview,
    fallbackMarkdownPreview,
    requestPathChoice,
    confirmAbandonDirtyPreview,
  } = deps;
  let fileRefFocusRequestSeq = 0;

  async function onTimelineFileRefClick(payload) {
    const previewState = {
      fallbackDiffText,
      fallbackMediaPreview,
      fallbackMarkdownPreview,
      focusedDiffPath,
      focusedDiffLine,
    };
    const threadId = getSelectedThreadIdValue(selectedThreadId);
    if (!threadId) {
      logWarn('ui', 'chat.fileRef.handle.no_thread', { payload });
      return;
    }

    const requestSeq = ++fileRefFocusRequestSeq;
    const { rawPath, line, column } = normalizeFileRefPayload(payload);
    const abortIfStale = () => {
      const isLatestFocusRequest = requestSeq === fileRefFocusRequestSeq;
      const isStillBoundThread = getSelectedThreadIdValue(selectedThreadId) === threadId;
      if (isLatestFocusRequest && isStillBoundThread) return false;
      logInfo('ui', 'chat.diff.focus.request_stale', {
        requested_thread_id: threadId,
        active_thread_id: getSelectedThreadIdValue(selectedThreadId),
        requested_path: rawPath,
        request_seq: requestSeq,
        latest_request_seq: fileRefFocusRequestSeq,
      });
      return true;
    };
    const diffText = (activeThreadDiffText.value || '').toString();
    const diffFiles = parseUnifiedDiff(diffText);
    logInfo('ui', 'chat.fileRef.handle.received', {
      thread_id: threadId,
      path: rawPath,
      line,
      column,
      diff_len: diffText.length,
      diff_files: diffFiles.length,
      payload,
    });
    if (!rawPath) {
      logWarn('ui', 'chat.fileRef.handle.no_path', { thread_id: threadId, line, payload });
      return;
    }

    const fileSwitchConfirmation = confirmFileSwitchOrAbort({
      confirmAbandonDirtyPreview,
      threadId,
      rawPath,
      line,
      column,
      requestSeq,
    });
    const confirmed = typeof fileSwitchConfirmation?.then === 'function'
      ? await fileSwitchConfirmation
      : fileSwitchConfirmation;
    if (typeof fileSwitchConfirmation?.then === 'function' && abortIfStale()) return;
    if (!confirmed) return;

    const restored = await restoreCurrentThreadSelection({
      props,
      threadId,
      activeThreadDiffText,
      rawPath,
      requestSeq,
      abortIfStale,
    });
    if (restored.aborted) return;
    if (!restored.selection) {
      await openSelectionFallbackPreview({
        props,
        previewState,
        requestPathChoice,
        abortIfStale,
        threadId,
        rawPath,
        line,
        column,
        effectiveDiffText: restored.effectiveDiffText,
        effectiveDiffFiles: restored.effectiveDiffFiles,
      });
      return;
    }

    applyPreviewState(previewState, {
      diffText: '',
      mediaPreview: null,
      markdownPreview: null,
      path: restored.selection.filename,
      line,
    });
    logInfo('ui', 'chat.diff.focus.applied', {
      thread_id: threadId,
      requested_path: rawPath,
      resolved_path: restored.selection.filename,
      line,
      diff_len: restored.effectiveDiffText.length,
      diff_files: restored.effectiveDiffFiles.length,
    });
  }

  function onTimelineCitationClick(payload) {
    const kind = (payload?.kind || '').toString().trim();
    if (kind !== 'image') return;
    const assetPointer = (payload?.assetPointer || '').toString().trim();
    const imageSrc = (payload?.imageSrc || '').toString().trim();
    const fallbackPath = (payload?.path || assetPointer || imageSrc || '').toString().trim();
    const preview = resolveCitationImagePreview(activeTimeline?.value, assetPointer, payload?.raw || '') || (imageSrc
      ? { src: imageSrc, fullSrc: imageSrc, path: fallbackPath, mediaType: 'image/*', sizeBytes: 0 }
      : null);
    if (!preview) {
      logWarn('ui', 'chat.citation.image_preview.miss', { asset_pointer: assetPointer, image_src: imageSrc, payload });
      return;
    }
    applyPreviewState({
      fallbackDiffText,
      fallbackMediaPreview,
      fallbackMarkdownPreview,
      focusedDiffPath,
      focusedDiffLine,
    }, {
      diffText: '',
      mediaPreview: preview,
      markdownPreview: null,
      path: (preview.path || fallbackPath).toString().trim(),
      line: 0,
    });
    logInfo('ui', 'chat.citation.image_preview.applied', {
      asset_pointer: assetPointer,
      image_src: imageSrc,
      resolved_path: preview.path,
      src: preview.src,
    });
  }

  return { onTimelineFileRefClick, onTimelineCitationClick };
}
