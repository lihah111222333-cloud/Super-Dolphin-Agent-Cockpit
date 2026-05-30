import React from 'react';
import * as Vue from '../lib/vue.esm-browser.prod.js';

export function ContextUsageBanner({
  level = 'normal',
  usedPercent = 0,
  usedTokens = 0,
  contextWindow = 0,
  canCompact = true,
  compacting = false,
  onCompact = null,
  onFork = null,
}) {
  const showTokenSection = level && level !== 'normal';
  const visible = showTokenSection;

  if (!visible) return null;

  const levelLabel = () => {
    if (level === 'critical') return '严重';
    if (level === 'danger') return '紧张';
    if (level === 'warn') return '提醒';
    return '';
  };

  const handleCompactClick = () => {
    if (compacting || !canCompact) return;
    if (typeof onCompact === 'function') onCompact();
  };

  const handleForkClick = () => {
    if (typeof onFork === 'function') onFork();
  };

  const compactButtonTitle = (() => {
    if (!canCompact) return '当前 agent 不支持上下文压缩';
    if (compacting) return '正在压缩…';
    return '触发上下文压缩';
  })();

  return (
    <div
      className={`context-usage-banner is-token-${level}`}
      role="alert"
      data-testid="context-usage-banner"
    >
      {showTokenSection && (
        <>
          <span className="context-usage-banner-icon" aria-hidden="true">⚠</span>
          <span className="context-usage-banner-msg">
            上下文使用率
            <strong>{Math.round(Number(usedPercent) || 0)}%</strong>
            {contextWindow > 0 && (
              <span style={{ opacity: 0.75, marginLeft: '4px' }}>
                ({usedTokens} / {contextWindow} tokens)
              </span>
            )}
            — {levelLabel()}，建议压缩上下文或新建继承会话以避免触发自动截断。
          </span>
          <button
            type="button"
            className="btn btn-ghost btn-xs context-usage-banner-action"
            disabled={!canCompact || compacting}
            title={compactButtonTitle}
            onClick={handleCompactClick}
          >
            {compacting ? '压缩中…' : '压缩上下文'}
          </button>
          <button
            type="button"
            className="btn btn-primary btn-xs context-usage-banner-action"
            title="以当前会话为背景新建一个继承对话"
            onClick={handleForkClick}
          >
            新建继承会话
          </button>
        </>
      )}
    </div>
  );
}

ContextUsageBanner.props = {
  level: { type: String, default: 'normal' },
  usedPercent: { type: Number, default: 0 },
  usedTokens: { type: Number, default: 0 },
  contextWindow: { type: Number, default: 0 },
  canCompact: { type: Boolean, default: true },
  compacting: { type: Boolean, default: false },
};

ContextUsageBanner.emits = ['compact', 'fork'];

ContextUsageBanner.template = `
  <div data-testid="context-usage-banner">
    <button @click="onCompact"></button>
    <button @click="onFork"></button>
  </div>
`;

ContextUsageBanner.setup = function(props, { emit }) {
  const visible = () => {
    return props.level && props.level !== 'normal';
  };
  const showTokenSection = () => {
    return props.level && props.level !== 'normal';
  };
  const levelLabel = () => {
    if (props.level === 'critical') return '严重';
    if (props.level === 'danger') return '紧张';
    if (props.level === 'warn') return '提醒';
    return '';
  };
  const onCompact = () => {
    if (props.compacting || !props.canCompact) return;
    emit('compact');
  };
  const onFork = () => {
    emit('fork');
  };
  return {
    visible,
    showTokenSection,
    levelLabel,
    onCompact,
    onFork,
  };
};

export default ContextUsageBanner;
