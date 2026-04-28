// @ts-nocheck
// 纯 setup 单元测试：项目 vitest 跑 node 环境无 jsdom，
// 不挂载 DOM，直接调用 setup() 返回的 helper + 验证 emit。
import { describe, expect, it, vi } from 'vitest';
import { ContextUsageBanner } from './components/ContextUsageBanner.js';

function instantiate(props, emit = vi.fn()) {
  const merged = { ...ContextUsageBanner.props && {}, ...props };
  // 模拟 vue 默认值填充
  const finalProps = {};
  for (const [k, def] of Object.entries(ContextUsageBanner.props)) {
    finalProps[k] = (k in merged) ? merged[k] : def.default;
  }
  const exposed = ContextUsageBanner.setup(finalProps, { emit });
  return { props: finalProps, emit, ...exposed };
}

describe('ContextUsageBanner · visibility', () => {
  it('not visible when level=normal and no failedInfo', () => {
    const { visible, showTokenSection, showFailedSection } = instantiate({ level: 'normal' });
    expect(visible()).toBe(false);
    expect(showTokenSection()).toBe(false);
    expect(showFailedSection()).toBe(false);
  });

  it('visible (token only) when level=critical without failedInfo', () => {
    const { visible, showTokenSection, showFailedSection } = instantiate({ level: 'critical' });
    expect(visible()).toBe(true);
    expect(showTokenSection()).toBe(true);
    expect(showFailedSection()).toBe(false);
  });

  it('visible (failed only) when failedInfo present even at level=normal', () => {
    const { visible, showTokenSection, showFailedSection } = instantiate({
      level: 'normal',
      failedInfo: { reason: 'continue_failed', error_message: 'boom' },
    });
    expect(visible()).toBe(true);
    expect(showTokenSection()).toBe(false);
    expect(showFailedSection()).toBe(true);
  });

  it('visible (both sections) when level=critical AND failedInfo present', () => {
    const { visible, showTokenSection, showFailedSection } = instantiate({
      level: 'critical',
      failedInfo: { reason: 'compact_then_continue_failed' },
    });
    expect(visible()).toBe(true);
    expect(showTokenSection()).toBe(true);
    expect(showFailedSection()).toBe(true);
  });
});

describe('ContextUsageBanner · level label', () => {
  it.each([
    ['critical', '严重'],
    ['danger', '紧张'],
    ['warn', '提醒'],
    ['normal', ''],
    ['unknown', ''],
  ])('levelLabel(%s) → %s', (level, expected) => {
    const { levelLabel } = instantiate({ level });
    expect(levelLabel()).toBe(expected);
  });
});

describe('ContextUsageBanner · failedReasonLabel translation', () => {
  it.each([
    ['continue_failed', '自动续接失败'],
    ['compact_then_continue_failed', '上下文压缩与自动续接均失败'],
    ['recover_then_continue_failed', '进程恢复与自动续接均失败'],
    ['gated_thread_already_continued', '此 thread 已自动续接过 1 次'],
    ['gated_global_fuse_blown', '系统级自动续接保险丝已触发'],
  ])('translates known reason %s', (reason, label) => {
    const { failedReasonLabel } = instantiate({ failedInfo: { reason } });
    expect(failedReasonLabel()).toBe(label);
  });

  it('falls back to generic label for unknown reason', () => {
    const { failedReasonLabel } = instantiate({ failedInfo: { reason: 'mystery_x' } });
    expect(failedReasonLabel()).toBe('自动续接失败');
  });

  it('returns empty when failedInfo is null', () => {
    const { failedReasonLabel } = instantiate({});
    expect(failedReasonLabel()).toBe('');
  });
});

describe('ContextUsageBanner · failedErrorSnippet truncation', () => {
  it('returns empty when no error_message', () => {
    const { failedErrorSnippet } = instantiate({ failedInfo: { reason: 'continue_failed' } });
    expect(failedErrorSnippet()).toBe('');
  });

  it('truncates to 120 chars', () => {
    const long = 'x'.repeat(500);
    const { failedErrorSnippet } = instantiate({ failedInfo: { reason: 'continue_failed', error_message: long } });
    expect(failedErrorSnippet().length).toBe(120);
    expect(failedErrorSnippet()).toBe('x'.repeat(120));
  });

  it('preserves message under 120 chars', () => {
    const { failedErrorSnippet } = instantiate({ failedInfo: { reason: 'x', error_message: 'short error' } });
    expect(failedErrorSnippet()).toBe('short error');
  });
});

describe('ContextUsageBanner · emits', () => {
  it('onCompact emits compact when canCompact=true and not compacting', () => {
    const emit = vi.fn();
    const { onCompact } = instantiate({ canCompact: true, compacting: false }, emit);
    onCompact();
    expect(emit).toHaveBeenCalledWith('compact');
  });

  it('onCompact does NOT emit when compacting', () => {
    const emit = vi.fn();
    const { onCompact } = instantiate({ canCompact: true, compacting: true }, emit);
    onCompact();
    expect(emit).not.toHaveBeenCalled();
  });

  it('onCompact does NOT emit when !canCompact', () => {
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

  it('onRetry emits retry-auto-continue when not retrying', () => {
    const emit = vi.fn();
    const { onRetry } = instantiate({ retrying: false }, emit);
    onRetry();
    expect(emit).toHaveBeenCalledWith('retry-auto-continue');
  });

  it('onRetry does NOT emit when retrying', () => {
    const emit = vi.fn();
    const { onRetry } = instantiate({ retrying: true }, emit);
    onRetry();
    expect(emit).not.toHaveBeenCalled();
  });
});

describe('ContextUsageBanner · component shape', () => {
  it('declares all expected props with defaults', () => {
    expect(ContextUsageBanner.props.failedInfo.default).toBe(null);
    expect(ContextUsageBanner.props.retrying.default).toBe(false);
  });

  it('declares retry-auto-continue in emits', () => {
    expect(ContextUsageBanner.emits).toContain('retry-auto-continue');
    expect(ContextUsageBanner.emits).toContain('compact');
    expect(ContextUsageBanner.emits).toContain('fork');
  });

  it('template references retry test-id and failed row test-id', () => {
    expect(ContextUsageBanner.template).toContain('auto-continue-failed-row');
    expect(ContextUsageBanner.template).toContain('auto-continue-retry-btn');
    expect(ContextUsageBanner.template).toContain("@click=\"onRetry\"");
  });
});
