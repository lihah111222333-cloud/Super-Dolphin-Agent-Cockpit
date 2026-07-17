import React, { useMemo, useRef } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import { ChevronDown, File, Search } from 'lucide-react';

const DIFF_LINE_ESTIMATE_PX = 20;
const DIFF_LINE_OVERSCAN = 8;
const DIFF_LINES_VIEWPORT_PX = 420;

/**
 * @typedef {import('@tanstack/react-virtual').VirtualItem} VirtualItem
 * @typedef {{
 *   filename: string,
 *   additions: number,
 *   deletions: number,
 *   text: string,
 * }} RuntimeDiffFileModel
 * @typedef {{
 *   key: string,
 *   type: string,
 *   oldNo: string | number,
 *   newNo: string | number,
 *   prefix: string,
 *   content: string,
 * }} RuntimeDiffLineModel
 */

/** @param {unknown} _instance @param {(rect: { width: number, height: number }) => void} callback */
function observeDiffLineViewport(_instance, callback) {
  callback({ width: 0, height: DIFF_LINES_VIEWPORT_PX });
  return undefined;
}

/** @param {HTMLElement} element */
function measureDiffLineElement(element) {
  const height = Math.ceil(element.getBoundingClientRect().height || 0);
  return Math.max(DIFF_LINE_ESTIMATE_PX, height);
}

/**
 * @param {{
 *   diffText?: string,
 *   diffSummary: { files: RuntimeDiffFileModel[] },
 *   collapsedFiles: Set<string>,
 *   actionNotice?: string,
 *   onLocateFile: (file: RuntimeDiffFileModel) => void,
 *   onOpenFile: (file: RuntimeDiffFileModel) => void,
 *   onToggleFile: (key: string) => void,
 *   parseLineEntries: (text: string) => RuntimeDiffLineModel[],
 * }} props
 */
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

/**
 * @param {{
 *   file: RuntimeDiffFileModel,
 *   index: number,
 *   collapsed: boolean,
 *   onLocate: () => void,
 *   onOpen: () => void,
 *   onToggle: () => void,
 *   parseLineEntries: (text: string) => RuntimeDiffLineModel[],
 * }} props
 */
function RuntimeDiffFile({
  file,
  index,
  collapsed,
  onLocate,
  onOpen,
  onToggle,
  parseLineEntries,
}) {
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

/**
 * @param {{
 *   file: RuntimeDiffFileModel,
 *   index: number,
 *   parseLineEntries: (text: string) => RuntimeDiffLineModel[],
 * }} props
 */
function RuntimeDiffLines({ file, index, parseLineEntries }) {
  const lines = useMemo(() => parseLineEntries(file.text), [file.text, parseLineEntries]);
  const scrollRef = useRef(null);
  // TanStack Virtual exposes imperative methods; keep the waiver local to this hook call.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: lines.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => DIFF_LINE_ESTIMATE_PX,
    overscan: DIFF_LINE_OVERSCAN,
    initialRect: { width: 0, height: DIFF_LINES_VIEWPORT_PX },
    observeElementRect: observeDiffLineViewport,
    measureElement: measureDiffLineElement,
  });

  return (
    <div className="diff-file-lines" id={`runtime-diff-file-${index}`} ref={scrollRef}>
      <div className="diff-file-lines-virtual" style={{ height: `${virtualizer.getTotalSize()}px` }}>
        {virtualizer.getVirtualItems().map((virtualRow) => (
          <RuntimeDiffLine
            key={lines[virtualRow.index].key}
            line={lines[virtualRow.index]}
            measureElement={virtualizer.measureElement}
            virtualRow={virtualRow}
          />
        ))}
      </div>
    </div>
  );
}

/**
 * @param {{
 *   line: RuntimeDiffLineModel,
 *   measureElement: (node: HTMLDivElement | null) => void,
 *   virtualRow: VirtualItem,
 * }} props
 */
function RuntimeDiffLine({ line, measureElement, virtualRow }) {
  return (
    <div
      className={`diff-line ${line.type}`}
      data-index={virtualRow.index}
      ref={measureElement}
      style={{ transform: `translateY(${virtualRow.start}px)` }}
    >
      <span className="diff-line-num diff-line-old">{line.oldNo}</span>
      <span className="diff-line-num diff-line-new">{line.newNo}</span>
      <span className="diff-line-prefix">{line.prefix}</span>
      <span className="diff-line-content">{line.content}</span>
    </div>
  );
}

export { RuntimeDiffView };
