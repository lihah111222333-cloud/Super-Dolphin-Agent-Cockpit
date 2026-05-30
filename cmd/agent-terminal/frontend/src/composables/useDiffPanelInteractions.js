import { useState, useMemo, useEffect, useRef } from 'react';
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

  const [copiedPath, setCopiedPath] = useState('');
  const [collapsedFileKeys, setCollapsedFileKeys] = useState([]);
  const copyResetTimerRef = useRef(null);

  const normalizedFocusFile = useMemo(() => normalizePath(props.focusFile), [props.focusFile]);
  const normalizedFocusLine = useMemo(() => {
    const line = Number(props.focusLine);
    return Number.isFinite(line) && line > 0 ? Math.floor(line) : 0;
  }, [props.focusLine]);

  function isFileCollapsed(file, index = 0) {
    return collapsedFileKeys.includes(fileKey(file, index));
  }

  function setFileCollapsed(file, collapsed, index = 0) {
    const key = fileKey(file, index);
    setCollapsedFileKeys((prev) => {
      const next = prev.filter((item) => item !== key);
      if (collapsed) {
        next.push(key);
      }
      return next;
    });
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
    return Boolean(path) && path === copiedPath;
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
    setCopiedPath(path);
    if (copyResetTimerRef.current) clearTimeout(copyResetTimerRef.current);
    copyResetTimerRef.current = setTimeout(() => {
      setCopiedPath('');
      copyResetTimerRef.current = null;
    }, 1500);
  }

  function isFocusedFile(file) {
    const target = normalizedFocusFile;
    if (!target) return false;
    return fileMatchesTarget(file?.filename, target);
  }

  function isFocusedLine(file, line) {
    if (!isFocusedFile(file)) return false;
    const target = normalizedFocusLine;
    if (!target) return false;
    const oldNo = Number(line?.oldNo);
    const newNo = Number(line?.newNo);
    return (Number.isFinite(oldNo) && oldNo === target)
      || (Number.isFinite(newNo) && newNo === target);
  }

  useEffect(() => {
    setCollapsedFileKeys([]);
    logDebug('ui', 'diffPanel.updated', {
      text_len: (props.diffText || '').length,
      files: files.length,
      preview_only: showLargeDiffPreview,
    });
  }, [props.diffText]);

  useEffect(() => {
    if (!hasDiffPreview) return;
    const focusFile = normalizedFocusFile;
    const focusLine = normalizedFocusLine;
    if (!focusFile && !focusLine) return;

    // 1. Expand the file if it is collapsed
    const focusedIndex = files.findIndex((item) => isFocusedFile(item));
    if (focusedIndex >= 0) {
      const file = files[focusedIndex];
      const key = fileKey(file, focusedIndex);
      if (collapsedFileKeys.includes(key)) {
        setCollapsedFileKeys((prev) => prev.filter((item) => item !== key));
        return;
      }
    }

    // 2. Scroll into view
    const root = panelRef.current;
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

    const fileEl = root.querySelector('.diff-file-group.is-focused .diff-file-header');
    if (fileEl && typeof fileEl.scrollIntoView === 'function') {
      logInfo('ui', 'chat.diff.panel.focus.file_hit', {
        focus_file: focusFile,
        focus_line: focusLine,
        file_text: ((fileEl.textContent || '').toString().trim()).slice(0, 120),
      });
      fileEl.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
      return;
    }

    logDebug('ui', 'chat.diff.panel.focus.miss', {
      focus_file: focusFile,
      focus_line: focusLine,
      file_count: files.length,
      file_sample: files.slice(0, 8).map((item) => (item?.filename || '').toString()),
    });
  }, [props.focusFile, props.focusLine, props.diffText, hasDiffPreview, collapsedFileKeys, files, panelRef]);

  useEffect(() => {
    return () => {
      if (copyResetTimerRef.current) {
        clearTimeout(copyResetTimerRef.current);
      }
    };
  }, []);

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
