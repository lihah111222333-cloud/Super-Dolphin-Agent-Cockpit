import {
  computed,
  nextTick,
  onBeforeUnmount,
  ref,
  watch,
} from '../../lib/vue.esm-browser.prod.js';
import { logDebug, logInfo, logWarn } from '../services/log.js';

function normalizePath(value) {
  return (value || '')
    .toString()
    .trim()
    .replace(/\\/g, '/')
    .replace(/^\.\/+/, '')
    .replace(/^(a|b)\//, '')
    .toLowerCase();
}

/**
 * @typedef {{
 *   diffText?: string,
 *   focusFile?: string,
 *   focusLine?: number,
 * }} DiffPanelInteractionProps
 */

/**
 * @typedef {{ value: any }} RefLike
 */

/**
 * @typedef {{
 *   props: DiffPanelInteractionProps,
 *   panelRef: RefLike,
 *   files: RefLike,
 *   hasDiffPreview: { value: boolean },
 *   showLargeDiffPreview: { value: boolean },
 *   showLargeDiffPreview: { value: boolean },
 *   fileKey: (file: any, index?: number) => string,
 *   displayFilePath: (file: any) => string,
 *   fileMatchesTarget: (filePath: string, targetPath: string) => boolean,
 * }} DiffPanelInteractionOptions
 */

/**
 * @param {DiffPanelInteractionOptions} opts
 */
export function useDiffPanelInteractions(opts) {
  const {
    props,
    panelRef,
    files,
    hasDiffPreview,
    showLargeDiffPreview,
    fileKey,
    displayFilePath,
    fileMatchesTarget,
  } = opts;

  const copiedPath = ref('');
  const collapsedFileKeys = ref([]);
  /** @type {ReturnType<typeof setTimeout> | null} */
  let copyResetTimer = null;


  const normalizedFocusFile = computed(() => normalizePath(props.focusFile));
  const normalizedFocusLine = computed(() => {
    const line = Number(props.focusLine);
    return Number.isFinite(line) && line > 0 ? Math.floor(line) : 0;
  });

  function isFileCollapsed(file, index = 0) {
    return collapsedFileKeys.value.includes(fileKey(file, index));
  }

  function setFileCollapsed(file, collapsed, index = 0) {
    const key = fileKey(file, index);
    const next = collapsedFileKeys.value.filter((item) => item !== key);
    if (collapsed) next.push(key);
    collapsedFileKeys.value = next;
  }

  function toggleFileCollapsed(file, index = 0) {
    setFileCollapsed(file, !isFileCollapsed(file, index), index);
  }

  function fileToggleLabel(file, index = 0) {
    const action = isFileCollapsed(file, index) ? '展开' : '折叠';
    const path = displayFilePath(file) || `文件 ${index + 1}`;
    return `${action} ${path} 的变更`;
  }

  function fileCaretSymbol(file, index = 0) {
    return isFileCollapsed(file, index) ? '▸' : '▾';
  }

  function isCopiedFile(file) {
    const path = displayFilePath(file);
    return Boolean(path) && path === copiedPath.value;
  }

  async function copyFilePath(file) {
    const path = displayFilePath(file);
    if (!path) return;
    let copied = false;

    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(path);
        copied = true;
      } catch (_) {
        copied = false;
      }
    }

    if (!copied && typeof document !== 'undefined' && document.body) {
      const textarea = document.createElement('textarea');
      textarea.value = path;
      textarea.setAttribute('readonly', 'readonly');
      textarea.style.position = 'fixed';
      textarea.style.opacity = '0';
      textarea.style.left = '-9999px';
      textarea.style.top = '0';
      document.body.appendChild(textarea);
      textarea.focus();
      textarea.select();
      try {
        copied = document.execCommand('copy');
      } catch (_) {
        copied = false;
      } finally {
        document.body.removeChild(textarea);
      }
    }

    if (!copied) return;
    copiedPath.value = path;
    if (copyResetTimer) clearTimeout(copyResetTimer);
    copyResetTimer = setTimeout(() => {
      copiedPath.value = '';
      copyResetTimer = null;
    }, 1500);
  }

  function isFocusedFile(file) {
    const target = normalizedFocusFile.value;
    if (!target) return false;
    return fileMatchesTarget(file?.filename, target);
  }

  function isFocusedLine(file, line) {
    if (!isFocusedFile(file)) return false;
    const target = normalizedFocusLine.value;
    if (!target) return false;
    const oldNo = Number(line?.oldNo);
    const newNo = Number(line?.newNo);
    return (Number.isFinite(oldNo) && oldNo === target)
      || (Number.isFinite(newNo) && newNo === target);
  }

  async function syncFocus() {
    if (!hasDiffPreview.value) return;
    const focusFile = normalizedFocusFile.value;
    const focusLine = normalizedFocusLine.value;
    if (!focusFile && !focusLine) return;
    const focusedIndex = files.value.findIndex((item) => isFocusedFile(item));
    if (focusedIndex >= 0 && isFileCollapsed(files.value[focusedIndex], focusedIndex)) {
      setFileCollapsed(files.value[focusedIndex], false, focusedIndex);
    }
    await nextTick();

    const root = panelRef.value;
    if (!root || typeof root.querySelector !== 'function') {
      logWarn('ui', 'chat.diff.panel.focus.no_panel', {
        focus_file: focusFile,
        focus_line: focusLine,
      });
      return;
    }

    const line = root.querySelector('.diff-line.is-focused-line');
    if (line && typeof line.scrollIntoView === 'function') {
      logInfo('ui', 'chat.diff.panel.focus.line_hit', {
        focus_file: focusFile,
        focus_line: focusLine,
        line_text: ((line.textContent || '').toString().trim()).slice(0, 120),
      });
      line.scrollIntoView({ behavior: 'smooth', block: 'center' });
      return;
    }

    const file = root.querySelector('.diff-file-group.is-focused .diff-file-header');
    if (file && typeof file.scrollIntoView === 'function') {
      logInfo('ui', 'chat.diff.panel.focus.file_hit', {
        focus_file: focusFile,
        focus_line: focusLine,
        file_text: ((file.textContent || '').toString().trim()).slice(0, 120),
      });
      file.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      return;
    }

    logDebug('ui', 'chat.diff.panel.focus.miss', {
      focus_file: focusFile,
      focus_line: focusLine,
      file_count: files.value.length,
      file_sample: files.value.slice(0, 8).map((item) => (item?.filename || '').toString()),
    });
  }

  watch(
    () => props.diffText,
    (next, prev) => {
      if (next === prev) return;
      collapsedFileKeys.value = [];
      logDebug('ui', 'diffPanel.updated', {
        text_len: (next || '').length,
        files: files.value.length,
        preview_only: showLargeDiffPreview.value,
      });
    },
    { immediate: true },
  );

  watch(
    () => [props.focusFile, props.focusLine, props.diffText, hasDiffPreview.value],
    () => {
      if (!hasDiffPreview.value) return;
      const requestedPath = (props.focusFile || '').toString().trim();
      const requestedLine = Number(props.focusLine);
      const hasRequestedFocus = Boolean(requestedPath) || (Number.isFinite(requestedLine) && requestedLine > 0);
      if (!hasRequestedFocus) return;
      logInfo('ui', 'chat.diff.panel.focus.request', {
        focus_file: requestedPath,
        focus_line: requestedLine,
        diff_len: (props.diffText || '').length,
        file_count: files.value.length,
      });
      syncFocus().catch(() => {});
    },
    { immediate: true },
  );

  onBeforeUnmount(() => {
    if (copyResetTimer) {
      clearTimeout(copyResetTimer);
      copyResetTimer = null;
    }
  });

  return {
    isFileCollapsed,
    toggleFileCollapsed,
    fileToggleLabel,
    fileCaretSymbol,
    isCopiedFile,
    copyFilePath,
    isFocusedFile,
    isFocusedLine,
  };
}
