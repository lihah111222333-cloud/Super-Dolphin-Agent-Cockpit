// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';
import { reactive, ref } from '../lib/vue.esm-browser.prod.js';
import { useMultiAgentLaunch } from './composables/useMultiAgentLaunch.js';

vi.mock('./services/log.js', () => ({
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

describe('useMultiAgentLaunch', () => {
  it('creates a parent thread automatically when launching from an empty chat selection', async () => {
    const text = '拉5个子agent出来帮我分析这个项目';
    const attachments = [{ name: 'scope.md' }];
    const selectedThreadId = ref('');
    const threadStore = {
      state: reactive({ timelinesByThread: {} }),
      startThread: vi.fn()
        .mockResolvedValueOnce('parent-thread')
        .mockResolvedValueOnce('child-1')
        .mockResolvedValueOnce('child-2')
        .mockResolvedValueOnce('child-3')
        .mockResolvedValueOnce('child-4')
        .mockResolvedValueOnce('child-5'),
      sendMessage: vi.fn(async () => ({})),
      refreshSidebarState: vi.fn(async () => ({})),
    };
    const composer = {
      state: reactive({ text, attachments }),
      clearComposer: vi.fn(),
    };
    const launcher = useMultiAgentLaunch({
      threadStore,
      projectStore: {},
      selectedThreadId,
      composer,
      resolveCwd: () => '/repo',
      scheduleScrollToBottom: vi.fn(),
    });

    await expect(launcher.maybeLaunchFromComposer()).resolves.toBe(true);

    expect(selectedThreadId.value).toBe('parent-thread');
    expect(threadStore.startThread).toHaveBeenCalledTimes(6);
    expect(threadStore.startThread).toHaveBeenNthCalledWith(1, '/repo', expect.objectContaining({
      name: '5 子 Agent 并行任务',
      deferSpawn: true,
      focusMode: 'chat',
      skipInitialRuntimeSync: true,
      optimisticUserMessage: { text, attachments },
    }));
    const parentGroupId = threadStore.startThread.mock.calls[0][1].config.multiAgentGroupId;
    expect(parentGroupId).toMatch(/^mag_/);
    for (let index = 2; index <= 6; index += 1) {
      expect(threadStore.startThread.mock.calls[index - 1][1]).toEqual(expect.objectContaining({
        skipSaveActive: true,
        parentAgentId: 'parent-thread',
        config: expect.objectContaining({
          multiAgentGroupId: parentGroupId,
          parentThreadId: 'parent-thread',
          parentAgentId: 'parent-thread',
        }),
      }));
    }
    expect(threadStore.sendMessage).toHaveBeenCalledTimes(5);
    expect(composer.clearComposer).toHaveBeenCalledTimes(1);
    expect(threadStore.state.timelinesByThread['parent-thread'].at(-1).text).toContain('已创建 5 个子 Agent');
  });
});
