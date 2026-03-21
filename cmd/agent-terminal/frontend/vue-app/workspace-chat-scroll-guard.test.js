// @ts-nocheck
/**
 * 回归守卫 — 确保 ChatTimeline 使用稳定 :key 避免 DOM 全量重建。
 *
 * 根因: :key="selectedThreadId || 'timeline'" 导致每次切换线程时
 * Vue 销毁重建整个 timeline DOM，scrollHeight 塌缩 → 滚动跳顶。
 */
import { afterEach, describe, it, expect, vi } from 'vitest';
import { WorkspaceChatPanel } from './components/unified-chat/WorkspaceChatPanel.js';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('[regression] WorkspaceChatPanel scroll guards', () => {
  it('ChatTimeline must NOT use selectedThreadId as :key (prevents DOM rebuild)', () => {
    const template = WorkspaceChatPanel.template;
    // :key 不应包含 selectedThreadId — 会导致每次切换线程时销毁重建 DOM
    expect(template).not.toMatch(/:key="selectedThreadId/);
  });

  it('ChatTimeline uses a stable :key to preserve DOM across thread switches', () => {
    const template = WorkspaceChatPanel.template;
    // 确保使用固定字符串 key
    expect(template).toMatch(/:key="'timeline'"/);
  });

  it('dismissing pinned plan does not schedule scroll restoration', () => {
    const emit = vi.fn();
    const stopPropagation = vi.fn();
    const requestAnimationFrame = vi.fn();
    vi.stubGlobal('requestAnimationFrame', requestAnimationFrame);

    const vm = WorkspaceChatPanel.setup(
      { activePinnedPlan: { id: 'plan-1' } },
      { emit },
    );

    vm.onDismissPinnedPlanClick({ stopPropagation });

    expect(stopPropagation).toHaveBeenCalledTimes(1);
    expect(emit).toHaveBeenCalledWith('dismiss-pinned-plan');
    expect(requestAnimationFrame).not.toHaveBeenCalled();
  });
});
