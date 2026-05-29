// @ts-nocheck
import { describe, it, expect } from 'vitest';
import { ChatTimeline } from './components/ChatTimeline.js';

function setupTimeline(items, displayNames = {}) {
  return ChatTimeline.setup(
    {
      items,
      activeStatus: '',
      activeStatusText: '',
      activeStatusMeta: '',
      pinnedPlanVisible: false,
      pinnedPlanItemId: null,
      resolveThreadDisplayName: (threadId) => displayNames[threadId] || threadId,
    },
    { emit: () => {} },
  );
}

describe('ChatTimeline internal message rendering', () => {
  it('marks internal agent reports as dedicated internal bubbles and shows aliases', () => {
    const vm = setupTimeline(
      [{
        id: 'internal-1',
        kind: 'user',
        internal: true,
        text: '任务已完成',
        fromThreadId: 'agent-worker',
        toThreadId: 'agent-main',
        fromDisplay: 'worker-fallback',
        toDisplay: 'main-fallback',
      }],
      {
        'agent-worker': '代码修复代理',
        'agent-main': '主控代理',
      },
    );

    const item = vm.visibleItems.value[0];
    expect(item.internal).toBe(true);
    expect(vm.bubbleRole(item)).toBe('role-internal');
    expect(vm.internalRouteLabel(item)).toBe('→ 主控代理');
    expect(vm.roleLabel(item)).toBe('代码修复代理');
  });
});
