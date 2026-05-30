// @ts-nocheck
// 纯 setup 单元测试：项目 vitest 跑 node 环境无 jsdom，
// 不挂载 DOM，直接调用 setup() 返回的 helper + 验证 emit。
import { describe, expect, it, vi } from 'vitest';
import { ContextUsageBanner } from './components/ContextUsageBanner.js';

function instantiate(props, emit = vi.fn()) {
  const merged = { ...props };
  const finalProps = {};
  for (const [k, def] of Object.entries(ContextUsageBanner.props)) {
    finalProps[k] = (k in merged) ? merged[k] : def.default;
  }
  const exposed = ContextUsageBanner.setup(finalProps, { emit });
  return { props: finalProps, emit, ...exposed };
}

describe('ContextUsageBanner · visibility', () => {
  it('not visible when level=normal', () => {
    const { visible, showTokenSection } = instantiate({ level: 'normal' });
    expect(visible()).toBe(false);
    expect(showTokenSection()).toBe(false);
  });

  it.each(['warn', 'danger', 'critical'])('visible when level=%s', (level) => {
    const { visible, showTokenSection } = instantiate({ level });
    expect(visible()).toBe(true);
    expect(showTokenSection()).toBe(true);
  });
});

describe('ContextUsageBanner · level label', () => {
  it.each([
    ['critical', '严重'],
    ['danger', '紧张'],
    ['warn', '提醒'],
    ['normal', ''],
    ['unknown', ''],
  ])('levelLabel(%s) -> %s', (level, expected) => {
    const { levelLabel } = instantiate({ level });
    expect(levelLabel()).toBe(expected);
  });
});

describe('ContextUsageBanner · emits', () => {
  it('onCompact emits compact when canCompact=true and not compacting', () => {
    const emit = vi.fn();
    const { onCompact } = instantiate({ canCompact: true, compacting: false }, emit);
    onCompact();
    expect(emit).toHaveBeenCalledWith('compact');
  });

  it('onCompact does not emit when compacting', () => {
    const emit = vi.fn();
    const { onCompact } = instantiate({ canCompact: true, compacting: true }, emit);
    onCompact();
    expect(emit).not.toHaveBeenCalled();
  });

  it('onCompact does not emit when canCompact=false', () => {
    const emit = vi.fn();
    const { onCompact } = instantiate({ canCompact: false, compacting: false }, emit);
    onCompact();
    expect(emit).not.toHaveBeenCalled();
  });

  it('onFork emits fork', () => {
    const emit = vi.fn();
    const { onFork } = instantiate({}, emit);
    onFork();
    expect(emit).toHaveBeenCalledWith('fork');
  });
});

describe('ContextUsageBanner · component shape', () => {
  it('only declares token banner props', () => {
    expect(Object.keys(ContextUsageBanner.props).sort()).toEqual([
      'canCompact',
      'compacting',
      'contextWindow',
      'level',
      'usedPercent',
      'usedTokens',
    ].sort());
  });

  it('only declares compact and fork emits', () => {
    expect(ContextUsageBanner.emits).toEqual(['compact', 'fork']);
  });

  it('template keeps token actions', () => {
    expect(ContextUsageBanner.template).toContain('@click="onCompact"');
    expect(ContextUsageBanner.template).toContain('@click="onFork"');
    expect(ContextUsageBanner.template).toContain('data-testid="context-usage-banner"');
  });
});

describe('ContextUsageBanner · removed auto continue and watchdog UI', () => {
  it('does not expose auto-continue props, emits, handlers, or test ids', () => {
    expect(ContextUsageBanner.props.failedInfo).toBeUndefined();
    expect(ContextUsageBanner.props.retrying).toBeUndefined();
    expect(ContextUsageBanner.props.retryError).toBeUndefined();
    expect(ContextUsageBanner.emits).not.toContain('retry-auto-continue');
    expect(ContextUsageBanner.template).not.toContain('auto-continue-failed-row');
    expect(ContextUsageBanner.template).not.toContain('auto-continue-retry-btn');
    expect(ContextUsageBanner.template).not.toContain('auto-continue-retry-error');
    expect(ContextUsageBanner.template).not.toContain('onRetry');
  });

  it('does not expose watchdog props, emits, handlers, or test ids', () => {
    expect(ContextUsageBanner.props.stuckInfo).toBeUndefined();
    expect(ContextUsageBanner.props.pokingStuck).toBeUndefined();
    expect(ContextUsageBanner.emits).not.toContain('retry-stuck-thread');
    expect(ContextUsageBanner.emits).not.toContain('force-stuck-thread');
    expect(ContextUsageBanner.emits).not.toContain('mark-stuck-done');
    expect(ContextUsageBanner.template).not.toContain('thread-stuck-row');
    expect(ContextUsageBanner.template).not.toContain('thread-stuck-poke-btn');
    expect(ContextUsageBanner.template).not.toContain('thread-stuck-force-btn');
    expect(ContextUsageBanner.template).not.toContain('thread-stuck-mark-done-btn');
    expect(ContextUsageBanner.template).not.toContain('watchdog');
  });
});
