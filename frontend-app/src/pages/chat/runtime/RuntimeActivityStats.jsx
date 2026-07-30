import React, { useRef } from 'react';
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from 'react-aria-components';
import { Boxes, Code2, FileText, GitBranch, Link2, Settings, Workflow } from 'lucide-react';
import { runtimeStatTooltipStyle } from './runtimeActivityGeometry.js';

const STAT_ICONS = Object.freeze({
  command: GitBranch,
  file: FileText,
  goRun: Link2,
  jsonRender: Boxes,
  lsp: Code2,
  playwright: Workflow,
  tool: Settings,
});

const STAT_CAPTIONS = Object.freeze({
  command: '命令',
  file: '文件',
  goRun: 'go',
  jsonRender: 'JSON',
  lsp: 'LSP',
  playwright: 'PW',
  tool: '工具',
});

function runtimePopoverShouldCloseOnInteractOutside(element) {
  return !element.closest('.activity-panel-resizer');
}

function runtimeStatDetailEntriesForKey(detailEntriesByStat, key) {
  const entries = detailEntriesByStat[key];
  if (!Array.isArray(entries)) throw new Error(`runtime stat ${key} detail entries must be an array`);
  return entries;
}

function RuntimeStatList({ activeStat, detailEntriesByStat, onStatOpenChange, statItems, tokenUsage }) {
  return (
    <ul className="runtime-icons" aria-label="工具调用统计">
      {statItems.map((item) => (
        <RuntimeStatItem
          key={item.key}
          item={item}
          activeStat={activeStat}
          detailEntries={runtimeStatDetailEntriesForKey(detailEntriesByStat, item.key)}
          onOpenChange={onStatOpenChange}
        />
      ))}
      <li className="runtime-context" aria-label={tokenUsage ? `上下文使用率 ${tokenUsage.usedPercent.toFixed(1)}%` : '等待后端同步上下文使用率'}>
        {tokenUsage ? `${tokenUsage.usedPercent.toFixed(1)}% context` : 'context --'}
      </li>
    </ul>
  );
}

function RuntimeStatItem({ activeStat, detailEntries, item, onOpenChange }) {
  const { key, label, className, value } = item;
  const triggerRef = useRef(null);
  const isOpen = activeStat?.key === key;
  const Icon = STAT_ICONS[key] || Settings;
  return (
    <li className="runtime-stat-listitem">
      <DialogTrigger isOpen={isOpen} onOpenChange={(open) => onOpenChange(key, open, triggerRef.current)}>
        <AriaButton
          ref={triggerRef}
          type="button"
          className={`runtime-stat ${className}`}
          aria-expanded={isOpen}
          aria-haspopup="dialog"
          aria-label={key === 'tool' ? '工具调用总数' : `${label} 调用次数`}
        >
          <Icon size={16} aria-hidden="true" />
          <strong>{value}</strong>
          <span className="runtime-stat-caption" aria-hidden="true">{STAT_CAPTIONS[key] || key}</span>
        </AriaButton>
        {isOpen ? <RuntimeStatTooltip activeStat={activeStat} detailEntries={detailEntries} item={item} /> : null}
      </DialogTrigger>
    </li>
  );
}

function RuntimeStatTooltip({ activeStat, detailEntries, item }) {
  if (!activeStat || !item) return null;
  return (
    <Popover className="runtime-stat-tooltip" data-testid="runtime-stat-tooltip" style={runtimeStatTooltipStyle(activeStat.anchorRect)} shouldCloseOnInteractOutside={runtimePopoverShouldCloseOnInteractOutside}>
      <Dialog aria-label={`${item.label} 调用明细`} className="runtime-stat-tooltip-dialog">
        <span className="runtime-stat-tooltip-title"><b>{item.label}</b><strong>{item.value}</strong></span>
        {detailEntries.length > 0 ? (
          <span className="runtime-stat-tooltip-list">
            {detailEntries.map((entry) => (
              <span key={entry.name} className="runtime-stat-tooltip-row">
                <span className="runtime-stat-tooltip-name">{entry.name}</span>
                <strong>{entry.count}</strong>
              </span>
            ))}
          </span>
        ) : <span className="runtime-stat-tooltip-empty">后端暂无明细</span>}
      </Dialog>
    </Popover>
  );
}

export { RuntimeStatList, RuntimeStatTooltip };
