import React, { useState } from 'react';
import { Button as AriaButton, Dialog, DialogTrigger, Popover } from 'react-aria-components';
import { Code2, FileText, Minus, Plus, Bot } from 'lucide-react';
import { runtimeToolbarMetrics } from '../adapters/runtimeToolbarAdapter.js';

const METRIC_ICONS = Object.freeze({
  files: FileText,
  changedLines: Code2,
  additions: Plus,
  deletions: Minus,
});

function RuntimeToolbarStat({ metric }) {
  const [open, setOpen] = useState(false);
  const Icon = METRIC_ICONS[metric.key] || FileText;
  const toneClass = metric.tone === 'neutral' ? '' : ` ${metric.tone}`;
  return (
    <DialogTrigger isOpen={open} onOpenChange={setOpen}>
      <AriaButton
        type="button"
        className={`runtime-stat score runtime-toolbar-stat${toneClass}`}
        aria-label={metric.ariaLabel}
        aria-expanded={open}
        aria-haspopup="dialog"
        data-testid={`runtime-toolbar-stat-${metric.key}`}
      >
        <Icon size={13} aria-hidden="true" />
        <span className="runtime-toolbar-stat__label">{metric.label}</span>
        <strong>{metric.value}</strong>
        <span className="runtime-toolbar-stat__unit">{metric.unit}</span>
      </AriaButton>
      {open ? (
        <Popover className="runtime-toolbar-tooltip" data-testid="runtime-toolbar-tooltip" placement="bottom start">
          <Dialog aria-label={metric.ariaLabel} className="runtime-toolbar-tooltip-dialog">
            {metric.tooltip}
          </Dialog>
        </Popover>
      ) : null}
    </DialogTrigger>
  );
}

/*
 * Runtime 概览指标栏：左侧提供切换到 Agent 看板的入口，
 * 每个指标都有简短名称、数值、单位，点击（键盘可聚焦）打开解释 tooltip。
 */
function RuntimeToolbar({ diffSummary, onShowAgents }) {
  return (
    <div className="runtime-toolbar" aria-label="代码变更概览">
      <button
        type="button"
        className="runtime-view-switch"
        aria-label="切换到 Agent 看板"
        title="切换到 Agent 看板"
        data-testid="runtime-show-agents"
        onClick={onShowAgents}
      >
        <Bot size={13} aria-hidden="true" />
        Agents
      </button>
      <div className="runtime-toolbar__metrics">
        {runtimeToolbarMetrics(diffSummary).map((metric) => (
          <RuntimeToolbarStat key={metric.key} metric={metric} />
        ))}
      </div>
    </div>
  );
}

export { RuntimeToolbar };
