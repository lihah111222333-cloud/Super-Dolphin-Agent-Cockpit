import { describe, expect, it } from 'vitest';
import { buildVisibleChatThreadCards } from './utils/thread-page-utils.js';

function buildCards(threads) {
  return buildVisibleChatThreadCards({
    threads,
    selectedThreadId: '',
    pinnedMap: {},
    archivedMap: {},
    runtimeById: {},
    showArchived: false,
    displayNameOf: (thread) => thread.name,
    statusOf: () => 'idle',
    statusHeaderOf: () => '等待指示',
    interruptibleOf: () => false,
    routingOf: () => ({}),
    pendingLaunchOf: () => false,
  }).cards;
}

describe('thread page multi-agent sorting', () => {
  it('orders child agent cards by numeric agent prefix', () => {
    const cards = buildCards([
      { id: 't7', name: 'agent7 · 审查', state: 'idle' },
      { id: 't2', name: 'agent2 · 搜索', state: 'idle' },
      { id: 't1', name: 'agent1 · 需求', state: 'idle' },
      { id: 't10', name: 'agent10 · 汇总', state: 'idle' },
    ]);
    expect(cards.map((card) => card.name)).toEqual([
      'agent1 · 需求',
      'agent2 · 搜索',
      'agent7 · 审查',
      'agent10 · 汇总',
    ]);
  });

  it('keeps non-agent cards after ordered child agents', () => {
    const cards = buildCards([
      { id: 'main', name: '主对话', state: 'idle' },
      { id: 't3', name: 'agent3 · 方案', state: 'idle' },
      { id: 't1', name: 'agent1 · 需求', state: 'idle' },
    ]);
    expect(cards.map((card) => card.name)).toEqual(['agent1 · 需求', 'agent3 · 方案', '主对话']);
  });

  it('keeps repeated multi-agent launches grouped by first-seen order', () => {
    const cards = buildCards([
      { id: 'batch1-a1', name: 'agent1 · 第一批需求', state: 'idle' },
      { id: 'batch1-a2', name: 'agent2 · 第一批搜索', state: 'idle' },
      { id: 'batch1-a3', name: 'agent3 · 第一批方案', state: 'idle' },
      { id: 'batch1-a4', name: 'agent4 · 第一批风险', state: 'idle' },
      { id: 'batch1-a5', name: 'agent5 · 第一批汇总', state: 'idle' },
      { id: 'batch2-a1', name: 'agent1 · 第二批需求', state: 'idle' },
      { id: 'batch2-a2', name: 'agent2 · 第二批搜索', state: 'idle' },
      { id: 'batch2-a3', name: 'agent3 · 第二批方案', state: 'idle' },
      { id: 'batch2-a4', name: 'agent4 · 第二批风险', state: 'idle' },
      { id: 'batch2-a5', name: 'agent5 · 第二批汇总', state: 'idle' },
    ]);
    expect(cards.map((card) => card.id)).toEqual([
      'batch1-a1',
      'batch1-a2',
      'batch1-a3',
      'batch1-a4',
      'batch1-a5',
      'batch2-a1',
      'batch2-a2',
      'batch2-a3',
      'batch2-a4',
      'batch2-a5',
    ]);
  });
});
