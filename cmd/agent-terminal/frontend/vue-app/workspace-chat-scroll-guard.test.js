// @ts-nocheck
/**
 * 回归守卫 — 确保 ChatTimeline 使用稳定 :key 避免 DOM 全量重建。
 *
 * 根因: :key="selectedThreadId || 'timeline'" 导致每次切换线程时
 * Vue 销毁重建整个 timeline DOM，scrollHeight 塌缩 → 滚动跳顶。
 */
import { describe, it, expect } from 'vitest';
import { WorkspaceChatPanel } from './components/unified-chat/WorkspaceChatPanel.js';

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
});
