import React from 'react';
import { ChevronDown, File, Search } from 'lucide-react';

function RuntimeDiffView({
  diffText,
  diffSummary,
  collapsedFiles,
  actionNotice,
  onLocateFile,
  onOpenFile,
  onToggleFile,
  parseLineEntries,
}) {
  if (!diffText) return <div className="diff-empty">暂无代码变更</div>;
  return (
    <div className="diff-empty">
      {actionNotice ? <output className="diff-action-notice">{actionNotice}</output> : null}
      <div className="diff-view" data-testid="diff-view">
        {diffSummary.files.map((file, index) => (
          <RuntimeDiffFile
            key={`${file.filename}:${index}`}
            file={file}
            index={index}
            collapsed={collapsedFiles.has(`${file.filename}:${index}`)}
            onLocate={() => onLocateFile(file)}
            onOpen={() => onOpenFile(file)}
            onToggle={() => onToggleFile(`${file.filename}:${index}`)}
            parseLineEntries={parseLineEntries}
          />
        ))}
      </div>
    </div>
  );
}

function RuntimeDiffFile({ file, index, collapsed, onLocate, onOpen, onToggle, parseLineEntries }) {
  const toggleLabel = `${collapsed ? '展开' : '折叠'} ${file.filename}`;
  return (
    <section className={`diff-file-group${collapsed ? ' is-collapsed' : ''}`}>
      <div className="diff-file-header">
        <button type="button" className="diff-file-toggle" aria-label={toggleLabel} aria-expanded={!collapsed} aria-controls={`runtime-diff-file-${index}`} onClick={onToggle}>
          <span className="diff-file-title">
            <ChevronDown className="diff-file-caret" size={14} aria-hidden="true" />
            <span className="diff-file-name">{file.filename}</span>
          </span>
          <span className="diff-file-stats" aria-hidden="true"><b className="good">+{file.additions}</b><b className="bad">-{file.deletions}</b></span>
        </button>
        <div className="diff-file-actions">
          <button type="button" className="diff-file-action" aria-label={`定位 ${file.filename}`} title={`定位 ${file.filename}`} onClick={onLocate}>
            <Search size={13} aria-hidden="true" />
          </button>
          <button type="button" className="diff-file-action" aria-label={`打开 ${file.filename}`} title={`打开 ${file.filename}`} onClick={onOpen}>
            <File size={13} aria-hidden="true" />
          </button>
        </div>
      </div>
      {!collapsed ? <RuntimeDiffLines file={file} index={index} parseLineEntries={parseLineEntries} /> : null}
    </section>
  );
}

function RuntimeDiffLines({ file, index, parseLineEntries }) {
  return (
    <div className="diff-file-lines" id={`runtime-diff-file-${index}`}>
      {parseLineEntries(file.text).map((line) => (
        <div className={`diff-line ${line.type}`} key={line.key}>
          <span className="diff-line-num diff-line-old">{line.oldNo}</span>
          <span className="diff-line-num diff-line-new">{line.newNo}</span>
          <span className="diff-line-prefix">{line.prefix}</span>
          <span className="diff-line-content">{line.content}</span>
        </div>
      ))}
    </div>
  );
}

export { RuntimeDiffView };
