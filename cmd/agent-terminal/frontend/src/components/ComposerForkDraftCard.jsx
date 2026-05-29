import React, { useState, useRef, useEffect, useMemo, useCallback } from 'react';

export function ComposerForkDraftCard({
  forkDraft = { active: false, sharedFilePaths: [] },
  submitting = false,
  error = '',
  sourceThreadName = '',
  contextUsedPercent = 0,
  availableSharedFiles = null,
  onClose,
  onSubmit,
  onAddSharedFile,
  onRemoveSharedFile,
}) {
  const [pickerOpen, setPickerOpen] = useState(false);
  const [pickerQuery, setPickerQuery] = useState('');
  const cardRef = useRef(null);

  useEffect(() => {
    if (forkDraft?.active) {
      cardRef.current?.focus({ preventScroll: true });
    }
  }, [forkDraft?.active]);

  const isLoadingShared = useMemo(() => {
    if (!pickerOpen) return false;
    return !Array.isArray(availableSharedFiles);
  }, [pickerOpen, availableSharedFiles]);

  const filteredAvailableFiles = useMemo(() => {
    const all = Array.isArray(availableSharedFiles) ? availableSharedFiles : [];
    const mounted = new Set(forkDraft?.sharedFilePaths || []);
    const q = pickerQuery.trim().toLowerCase();
    return all
      .map((file) => ({
        path: (file?.path || '').toString(),
        updatedBy: (file?.updated_by || file?.updatedBy || '').toString(),
      }))
      .filter((file) => file.path && !mounted.has(file.path))
      .filter((file) => !q || file.path.toLowerCase().includes(q) || file.updatedBy.toLowerCase().includes(q))
      .slice(0, 30);
  }, [availableSharedFiles, forkDraft?.sharedFilePaths, pickerQuery]);

  const openPicker = useCallback(() => {
    setPickerQuery('');
    setPickerOpen(true);
  }, []);

  const closePicker = useCallback(() => {
    setPickerOpen(false);
    setPickerQuery('');
  }, []);

  const pickFile = useCallback((path) => {
    const value = (path || '').toString().trim();
    if (!value) return;
    onAddSharedFile?.(value);
    closePicker();
  }, [onAddSharedFile, closePicker]);

  const onCardKeydown = useCallback((event) => {
    if (event.key !== 'Escape') return;
    if (submitting) return;
    if (pickerOpen) {
      closePicker();
      event.stopPropagation();
      return;
    }
    onClose?.();
    event.stopPropagation();
  }, [submitting, pickerOpen, closePicker, onClose]);

  if (!forkDraft?.active) {
    return null;
  }

  return (
    <div
      ref={cardRef}
      className="composer-fork-draft-card"
      data-testid="composer-fork-draft-card"
      role="region"
      aria-label="新建继承对话草稿"
      tabIndex={0}
      onKeyDown={onCardKeydown}
    >
      <div className="composer-fork-draft-head">
        <span className="composer-fork-draft-title">新建继承对话</span>
        {sourceThreadName && (
          <span className="composer-fork-draft-source" title={sourceThreadName}>
            继承自：{sourceThreadName}
          </span>
        )}
        {contextUsedPercent > 0 && (
          <span className="composer-fork-draft-pct">
            当前 {Math.round(contextUsedPercent)}%
          </span>
        )}
        <button
          type="button"
          className="btn btn-ghost btn-xs"
          onClick={onClose}
          disabled={submitting}
          aria-label="关闭草稿（Esc）"
          title="关闭（Esc）"
        >
          ×
        </button>
      </div>

      <div className="composer-fork-draft-body">
        <div className="composer-fork-draft-row">
          <span className="composer-fork-draft-label">摘要来源：</span>
          <span className="composer-fork-draft-value">当前对话历史（截断 ≤ 2400 字）</span>
        </div>
        <div className="composer-fork-draft-row">
          <span className="composer-fork-draft-label">挂载共享文件：</span>
          <button
            type="button"
            className="btn btn-ghost btn-xs"
            data-testid="composer-fork-draft-add-shared"
            onClick={() => pickerOpen ? closePicker() : openPicker()}
            disabled={submitting}
          >
            {pickerOpen ? '收起' : '+ 选择文件'}
          </button>
        </div>

        {pickerOpen && (
          <div className="composer-fork-draft-picker" data-testid="composer-fork-draft-picker">
            <input
              type="text"
              className="composer-fork-draft-picker-input"
              placeholder="搜索路径或维护人..."
              value={pickerQuery}
              disabled={submitting}
              onChange={(e) => setPickerQuery(e.target.value)}
              data-testid="composer-fork-draft-picker-input"
            />
            {isLoadingShared ? (
              <div className="composer-fork-draft-picker-empty" data-testid="composer-fork-draft-picker-loading">
                加载中…
              </div>
            ) : filteredAvailableFiles.length > 0 ? (
              <ul className="composer-fork-draft-picker-list">
                {filteredAvailableFiles.map((file) => (
                  <li key={file.path}>
                    <button
                      type="button"
                      className="composer-fork-draft-picker-item"
                      onClick={() => pickFile(file.path)}
                      disabled={submitting}
                      title={file.path}
                    >
                      <span className="composer-fork-draft-picker-path">{file.path}</span>
                      {file.updatedBy && (
                        <span className="composer-fork-draft-picker-meta">{file.updatedBy}</span>
                      )}
                    </button>
                  </li>
                ))}
              </ul>
            ) : (
              <div className="composer-fork-draft-picker-empty">
                {pickerQuery ? '没有匹配的共享文件' : '暂无可挂载的共享文件（去「共享文件」页创建）'}
              </div>
            )}
          </div>
        )}

        {forkDraft.sharedFilePaths && forkDraft.sharedFilePaths.length > 0 ? (
          <ul className="composer-fork-draft-files">
            {forkDraft.sharedFilePaths.map((path) => (
              <li key={path}>
                <span className="composer-fork-draft-file-path" title={path}>{path}</span>
                <button
                  type="button"
                  className="btn btn-ghost btn-xs"
                  onClick={() => onRemoveSharedFile?.(path)}
                  disabled={submitting}
                  aria-label="移除挂载"
                >
                  ×
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <div className="composer-fork-draft-empty">未挂载共享文件（仅用对话摘要新建）</div>
        )}
      </div>

      {error && <div className="composer-fork-draft-error" role="alert">{error}</div>}

      <div className="composer-fork-draft-actions">
        <button type="button" className="btn btn-ghost btn-xs" onClick={onClose} disabled={submitting}>
          取消
        </button>
        <button
          type="button"
          className="btn btn-primary btn-xs"
          data-testid="composer-fork-draft-submit"
          onClick={onSubmit}
          disabled={submitting}
        >
          {submitting ? '创建中…' : '创建并继续'}
        </button>
      </div>
    </div>
  );
}
