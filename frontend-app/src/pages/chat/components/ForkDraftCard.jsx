import React from 'react';
import { FileText, X } from 'lucide-react';
import { runUIAction } from './chatUiActions.js';
import { requiredMarkdownArray } from './markdownMessageModel.js';

function ForkDraftCard({ store }) {
  const draft = store.forkDraft;
  if (!draft?.open) return null;
  const selected = new Set(requiredMarkdownArray(draft.sharedFilePaths, 'forkDraft.sharedFilePaths'));
  const files = Array.isArray(draft.availableSharedFiles) ? draft.availableSharedFiles : [];
  return (
    <section className="fork-draft-card" data-testid="fork-draft-card" aria-label="继承对话草稿">
      <header>
        <div>
          <p>继承对话</p>
          <strong>{draft.sourceTitle || '继承自当前会话'}</strong>
        </div>
        <button
          type="button"
          className="fork-draft-close"
          aria-label="关闭继承对话草稿"
          disabled={draft.submitting}
          onClick={() => runUIAction(() => store.closeForkDraft?.())}
        >
          <X size={14} />
        </button>
      </header>
      {draft.error ? <div className="fork-draft-error" role="alert">{draft.error}</div> : null}
      <div className="fork-draft-files" aria-live="polite">
        {draft.loadingSharedFiles ? <span className="fork-draft-muted">正在加载共享文件...</span> : null}
        {!draft.loadingSharedFiles && files.length === 0 ? <span className="fork-draft-muted">暂无可选共享文件</span> : null}
        {files.map((file) => (
          <label key={file.path} className="fork-draft-file">
            <input
              type="checkbox"
              aria-label={`选择共享文件 ${file.path}`}
              checked={selected.has(file.path)}
              disabled={draft.submitting}
              onChange={() => runUIAction(() => store.toggleForkDraftSharedFile?.(file.path))}
            />
            <FileText size={14} />
            <span>{file.path}</span>
          </label>
        ))}
      </div>
      <div className="fork-draft-actions">
        <button type="button" disabled={draft.submitting} onClick={() => runUIAction(() => store.closeForkDraft?.())}>取消</button>
        <button type="button" className="fork-draft-submit" disabled={draft.submitting} onClick={() => runUIAction(() => store.submitForkThread?.())}>
          {draft.submitting ? '创建中...' : '创建继承对话'}
        </button>
      </div>
    </section>
  );
}

export { ForkDraftCard };
