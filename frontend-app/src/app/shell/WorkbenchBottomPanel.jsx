import React, { useEffect, useState } from 'react';
import { ChevronDown, ChevronUp, CircleDot, TerminalSquare } from 'lucide-react';

const MIN_HEIGHT = 132;
const MAX_HEIGHT = 360;

export function WorkbenchBottomPanel({ activePage, onHeightChange, projectPath, rightPanelOpen }) {
  const [open, setOpen] = useState(false);
  const [height, setHeight] = useState(188);
  const [tab, setTab] = useState('activity');
  const effectiveHeight = open ? height : 36;
  useEffect(() => onHeightChange?.(activePage === 'chat' ? effectiveHeight : 0), [activePage, effectiveHeight, onHeightChange]);
  if (activePage !== 'chat') return null;

  return (
    <section
      className={`workbench-bottom-panel${open ? ' is-open' : ' is-collapsed'}`}
      aria-label="底部工作台"
      style={{ '--workbench-bottom-height': `${effectiveHeight}px` }}
    >
      <div className="workbench-bottom-resize">
        <label>
          <span className="sr-only">调整底部面板高度</span>
          <input
            type="range"
            min={MIN_HEIGHT}
            max={MAX_HEIGHT}
            value={height}
            disabled={!open}
            onChange={(event) => setHeight(Number(event.target.value))}
          />
        </label>
      </div>
      <header className="workbench-bottom-header">
        <div className="workbench-bottom-tabs" role="tablist" aria-label="底部面板标签">
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'activity'}
            className={tab === 'activity' ? 'is-selected' : ''}
            onClick={() => setTab('activity')}
          >
            <CircleDot size={13} aria-hidden="true" /> 活动
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={tab === 'terminal'}
            className={tab === 'terminal' ? 'is-selected' : ''}
            onClick={() => setTab('terminal')}
          >
            <TerminalSquare size={13} aria-hidden="true" /> Terminal
            <span className="workbench-demo-badge">Demo · 只读</span>
          </button>
        </div>
        <button
          type="button"
          className="workbench-bottom-toggle"
          aria-label={open ? '收起底部面板' : '展开底部面板'}
          aria-expanded={open}
          onClick={() => setOpen((value) => !value)}
        >
          {open ? <ChevronDown size={15} aria-hidden="true" /> : <ChevronUp size={15} aria-hidden="true" />}
        </button>
      </header>
      {open ? (
        <div className="workbench-bottom-content">
          {tab === 'activity' ? (
            <dl className="workbench-activity-summary">
              <div><dt>Workspace</dt><dd>{projectPath}</dd></div>
              <div><dt>Surface</dt><dd>{activePage}</dd></div>
              <div><dt>Auxiliary</dt><dd>{rightPanelOpen ? 'Visible' : 'Hidden'}</dd></div>
            </dl>
          ) : (
            <div className="workbench-terminal-demo" role="note">
              <code>$ terminal integration is not available</code>
              <p>这是本地界面演示，不会执行命令，也不会连接后端。</p>
            </div>
          )}
        </div>
      ) : null}
    </section>
  );
}
