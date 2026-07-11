import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { materializeTurnTimelineEntries } from './chatTurnGroupingModel.js';

describe('materializeTurnTimelineEntries', () => {
  it('groups process items before the final assistant reply for each user turn', () => {
    const entries = materializeTurnTimelineEntries([
      { id: 'orphan', role: 'assistant', kind: 'assistant', text: '历史孤立消息' },
      { id: 'u1', role: 'user', kind: 'user', text: '开始' },
      { id: 'progress', role: 'assistant', kind: 'assistant', text: '正在定位' },
      { id: 'tool', role: 'assistant', kind: 'tool', title: 'grep', text: 'result' },
      { id: 'final', role: 'assistant', kind: 'assistant', text: '处理完成' },
    ], { activeCurrentTurn: true });

    expect(entries.map((entry) => entry.type)).toEqual(['message', 'message', 'process', 'message']);
    expect(entries[2]).toMatchObject({
      active: true,
      messages: [{ id: 'progress' }, { id: 'tool' }],
    });
    expect(entries[3].message.id).toBe('final');
  });

  it('keeps approvals outside the collapsed process group', () => {
    const entries = materializeTurnTimelineEntries([
      { id: 'u1', role: 'user', text: '执行' },
      { id: 'thinking', role: 'assistant', kind: 'thinking', text: '分析' },
      { id: 'approval', role: 'assistant', kind: 'approval', requestId: 7, status: 'pending' },
      { id: 'final', role: 'assistant', kind: 'assistant', text: '完成' },
    ]);

    expect(entries.find((entry) => entry.type === 'process')?.messages.map((item) => item.id)).toEqual(['thinking']);
    expect(entries.find((entry) => entry.message?.id === 'approval')?.type).toBe('message');
  });

  it('classifies approvals through the single approval adapter', () => {
    const source = readFileSync('src/pages/chat/thread/chatTurnGroupingModel.js', 'utf8');

    expect(source).toContain('features/approval/model/approvalDecision.js');
    expect(source).not.toContain('./chatApprovalModel.js');
  });

  it('does not create an empty process group for a direct answer', () => {
    const entries = materializeTurnTimelineEntries([
      { id: 'u1', role: 'user', text: '直接回答' },
      { id: 'final', role: 'assistant', kind: 'assistant', text: '答案' },
    ]);

    expect(entries.map((entry) => entry.type)).toEqual(['message', 'message']);
  });

  it('marks only the latest turn process as active', () => {
    const entries = materializeTurnTimelineEntries([
      { id: 'u1', role: 'user', text: '第一轮' },
      { id: 'thinking-1', role: 'assistant', kind: 'thinking', text: '分析一' },
      { id: 'final-1', role: 'assistant', kind: 'assistant', text: '答案一' },
      { id: 'u2', role: 'user', text: '第二轮' },
      { id: 'thinking-2', role: 'assistant', kind: 'thinking', text: '分析二' },
    ], { activeCurrentTurn: true });

    const processEntries = entries.filter((entry) => entry.type === 'process');
    expect(processEntries.map((entry) => entry.active)).toEqual([false, true]);
    expect(entries.at(-1)).toMatchObject({ type: 'process', messages: [{ id: 'thinking-2' }] });
  });

  it('does not mutate the source timeline', () => {
    const messages = [
      { id: 'u1', role: 'user', text: '开始' },
      { id: 'progress', role: 'assistant', text: '处理中' },
      { id: 'final', role: 'assistant', text: '完成' },
    ];
    const snapshot = structuredClone(messages);

    materializeTurnTimelineEntries(messages);

    expect(messages).toEqual(snapshot);
  });
});
