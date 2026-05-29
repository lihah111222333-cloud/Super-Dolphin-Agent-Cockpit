// @ts-nocheck
import { describe, it, expect, vi } from 'vitest';
import { applyImmediateTimelineFromMessages } from './stores/thread-history-ui.js';

describe('thread-history-ui immediate hydration', () => {
  it('preserves internal worker report metadata from thread/messages history', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'main-agent',
      response: {
        messages: [{
          id: 1,
          role: 'user',
          content: '任务已完成',
          createdAt: '2026-03-10T12:00:00Z',
          metadata: {
            internal: true,
            sourceKind: 'agent',
            fromThreadId: 'worker-agent',
            toThreadId: 'main-agent',
            fromDisplay: '代码修复代理',
            toDisplay: '主控代理',
          },
        }],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['main-agent']).toEqual([{
      id: 'main-agent-history-1',
      kind: 'user',
      text: '任务已完成',
      ts: '2026-03-10T12:00:00Z',
      internal: true,
      sourceKind: 'agent',
      fromThreadId: 'worker-agent',
      toThreadId: 'main-agent',
      fromDisplay: '代码修复代理',
      toDisplay: '主控代理',
    }]);
  });

  it('rehydrates image inputs into timeline attachments and strips raw image placeholders', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-image',
      response: {
        messages: [{
          id: 1,
          role: 'user',
          content: '请帮我看这张图\n<image name=[Image #1]></image>',
          createdAt: '2026-03-10T12:00:00Z',
          metadata: {
            input: [{ type: 'image', url: 'file:///tmp/screenshot.png' }],
          },
        }],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    // Non-clipboard local paths can't be served by the Wails asset route, so
    // previewUrl is left empty (the webview would block file:// anyway).
    expect(state.timelinesByThread['thread-image']).toEqual([expect.objectContaining({
      id: 'thread-image-history-1',
      kind: 'user',
      text: '请帮我看这张图',
      ts: '2026-03-10T12:00:00Z',
      attachments: [expect.objectContaining({
        kind: 'image',
        name: 'screenshot.png',
        path: '/tmp/screenshot.png',
        previewUrl: '',
      })],
    })]);
  });

  it('rehydrates clipboard image inputs through the /clipboard asset route', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-clip',
      response: {
        messages: [{
          id: 1,
          role: 'user',
          content: 'look at this',
          createdAt: '2026-03-10T12:00:00Z',
          metadata: {
            input: [{ type: 'localImage', path: '/var/folders/abc/T/clipboard-987654321.png' }],
          },
        }],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-clip']).toEqual([expect.objectContaining({
      kind: 'user',
      attachments: [expect.objectContaining({
        kind: 'image',
        path: '/var/folders/abc/T/clipboard-987654321.png',
        previewUrl: '/clipboard/clipboard-987654321.png',
      })],
    })]);
  });

  it('backfills the deduped image preview onto a later turn that only has the sha256 placeholder', () => {
    // Turn 1 carries the original base64 image with a sha256 hash. Turn 2's
    // text contains only the dedup placeholder produced by the claudecli
    // provider. The frontend must re-attach turn 1's image preview onto
    // turn 2 and strip the placeholder text from the bubble body.
    const fullHash = 'abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789';
    const prefix = fullHash.slice(0, 12);
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-dedup',
      response: {
        messages: [
          {
            id: 1,
            role: 'user',
            content: 'first description',
            createdAt: '2026-03-10T12:00:00Z',
            metadata: {
              input: [{ type: 'image', url: 'data:image/png;base64,AAAA', sha256: fullHash }],
            },
          },
          {
            id: 2,
            role: 'user',
            content: `[image previously attached in this session, sha256:${prefix}…]\n\nany change?`,
            createdAt: '2026-03-10T12:01:00Z',
          },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    const items = state.timelinesByThread['thread-dedup'];
    expect(items).toHaveLength(2);
    expect(items[1].text).toBe('any change?');
    expect(items[1].attachments).toEqual([expect.objectContaining({
      kind: 'image',
      previewUrl: 'data:image/png;base64,AAAA',
      sha256: fullHash,
    })]);
  });

  it('leaves placeholder text intact when no prior image matches the sha256 prefix', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-orphan',
      response: {
        messages: [{
          id: 1,
          role: 'user',
          content: '[image previously attached in this session, sha256:deadbeef0001…]\nstill here',
          createdAt: '2026-03-10T12:00:00Z',
        }],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    const item = state.timelinesByThread['thread-orphan'][0];
    // No prior image to clone; placeholder is still stripped from display
    // (the user already saw the image earlier, just not in this thread state),
    // but no attachment is fabricated.
    expect(item.text).toBe('still here');
    expect(item.attachments || []).toEqual([]);
  });

  it('normalizes thread/messages pages to chronological timeline order', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-1',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: '较新输出', createdAt: '2026-03-10T12:00:01Z' },
          { id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-1'].map((item) => item.kind)).toEqual(['user', 'assistant']);
    expect(state.timelinesByThread['thread-1'][0]).toEqual(expect.objectContaining({
      id: 'thread-1-history-1',
      text: '旧请求',
      ts: '2026-03-10T12:00:00Z',
    }));
    expect(state.timelinesByThread['thread-1'][1]).toEqual(expect.objectContaining({
      id: 'thread-1-history-2',
      text: '较新输出',
      ts: '2026-03-10T12:00:01Z',
    }));
  });

  it('skips immediate history apply when existing dialog timeline is newer and longer', () => {
    const existingTimeline = [{ id: 'thread-1-user', kind: 'user', text: '旧请求', ts: '2026-03-10T12:00:00Z' }, { id: 'thread-1-assistant', kind: 'assistant', text: '最新输出', ts: '2026-03-10T12:00:01Z' }, { id: 'thread-1-thinking', kind: 'thinking', text: '', ts: '2026-03-10T12:00:02Z' }];
    const state = { timelinesByThread: { 'thread-1': existingTimeline } };
    const logInfo = vi.fn();
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-1',
      response: { messages: [{ id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T12:00:00Z' }, { id: 2, role: 'assistant', content: '较旧输出', createdAt: '2026-03-10T12:00:01Z' }] },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo,
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-1']).toBe(existingTimeline);
    expect(logInfo).toHaveBeenCalledWith('thread', 'messages.load.local_timeline.skipped_stale', {
      thread_id: 'thread-1',
      existing_count: 3,
      incoming_count: 2,
      existing_dialog_count: 2,
      incoming_dialog_count: 2,
      existing_latest_dialog_ts: Date.parse('2026-03-10T12:00:01Z'),
      incoming_latest_dialog_ts: Date.parse('2026-03-10T12:00:01Z'),
    });
  });

  it('skips same-length history pages that would overwrite fresher waiting-state dialog', () => {
    const existingTimeline = [
      { id: 'thread-2-user', kind: 'user', text: '最新提示词', ts: '2026-03-10T12:00:00Z' },
      { id: 'thread-2-assistant', kind: 'assistant', text: '最新输出', ts: '2026-03-10T12:00:02Z' },
    ];
    const state = { timelinesByThread: { 'thread-2': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-2',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: '较旧输出', createdAt: '2026-03-10T12:00:01Z' },
          { id: 1, role: 'user', content: '最新提示词', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: () => ({ changed: true, items: [] }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-2']).toBe(existingTimeline);
  });
  it('applies incoming history page when existing live-patch entries have no ts but incoming has valid ts', () => {
    // live-patch 写入的条目没有 ts（或 ts=''），不应阻止拥有完整 ts 的 history page
    const existingTimeline = [
      { id: 'live-1', kind: 'user', text: '最新提示词', ts: '' },
      { id: 'live-2', kind: 'assistant', text: '等待中...', ts: '' },
    ];
    const state = { timelinesByThread: { 'thread-3': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-3',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: '等待中...', createdAt: '2026-03-10T12:00:01Z' },
          { id: 1, role: 'user', content: '最新提示词', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    // 应该接受 incoming（有完整 ts），不能因 length 相等就跳过
    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-3'][0].ts).toBe('2026-03-10T12:00:00Z');
    expect(state.timelinesByThread['thread-3'][1].ts).toBe('2026-03-10T12:00:01Z');
  });

  it('preserves non-dialog live runtime items when hydrated history arrives', () => {
    const state = { timelinesByThread: { 'thread-live': [
      { id: 'thread-live-user-1', kind: 'user', text: '最新提示词', ts: '2026-03-10T12:00:00Z' },
      { id: 'thread-live-thinking-1', kind: 'thinking', text: '', ts: '2026-03-10T12:00:01Z' },
    ] } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-live',
      response: {
        messages: [
          { id: 1, role: 'user', content: '最新提示词', createdAt: '2026-03-10T12:00:00Z' },
          { id: 2, role: 'assistant', content: '最终回复', createdAt: '2026-03-10T12:00:02Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
      logWarn: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-live'].map((item) => item.kind)).toEqual(['user', 'thinking', 'assistant']);
  });

  it('skips incoming history page when existing dialog has valid ts but incoming lacks ts', () => {
    const existingTimeline = [{ id: 'ts-1', kind: 'user', text: 'A', ts: '2026-03-10T12:00:00Z' }, { id: 'ts-2', kind: 'assistant', text: 'B', ts: '2026-03-10T12:00:01Z' }];
    const state = { timelinesByThread: { 'thread-5': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-5',
      response: { messages: [{ id: 2, role: 'assistant', content: 'B', createdAt: '' }, { id: 1, role: 'user', content: 'A', createdAt: '' }] },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: () => ({ changed: true, items: [] }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-5']).toBe(existingTimeline);
  });

  it('still skips incoming history page when both existing and incoming have no valid ts and lengths are equal', () => {
    // 两边都没有 ts → fallback 比长度，相等则跳过（保留本地）
    const existingTimeline = [
      { id: 'live-1', kind: 'user', text: 'A', ts: '' },
      { id: 'live-2', kind: 'assistant', text: 'B', ts: '' },
    ];
    const state = { timelinesByThread: { 'thread-4': existingTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-4',
      response: {
        messages: [
          { id: 2, role: 'assistant', content: 'B', createdAt: '' },
          { id: 1, role: 'user', content: 'A', createdAt: '' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: () => ({ changed: true, items: [] }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(false);
    expect(state.timelinesByThread['thread-4']).toBe(existingTimeline);
  });

  // ─── 回归: 防止 history hydration 回退到替换已充足的 runtime timeline ───

  it('[regression] still applies history when only history items exist (no runtime data)', () => {
    const historyTimeline = [
      { id: 'thread-h-history-1', kind: 'user', text: '旧请求', ts: '2026-03-10T11:00:00Z' },
    ];
    const state = { timelinesByThread: { 'thread-h': historyTimeline } };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-h',
      response: {
        messages: [
          { id: 1, role: 'user', content: '旧请求', createdAt: '2026-03-10T11:00:00Z' },
          { id: 2, role: 'assistant', content: '新回复', createdAt: '2026-03-10T12:00:01Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-h']).toHaveLength(2);
  });

  it('[regression] applies history on empty timeline (first load)', () => {
    const state = { timelinesByThread: {} };
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-new',
      response: {
        messages: [
          { id: 1, role: 'user', content: 'Hello', createdAt: '2026-03-10T12:00:00Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    expect(state.timelinesByThread['thread-new']).toHaveLength(1);
  });

  // ─── 回归: 防止流输出期间出现 double-text 重复气泡 ───

  it('[regression] drops stale DB message when local streaming buffer is ahead', () => {
    // 模拟本地正处于 streaming 状态，且本地 buffer 的文本已经比 DB 拉取到的文本长
    const existingTimeline = [
      { id: 'thread-stream-user', kind: 'user', text: '请求', ts: '2026-03-10T12:00:00Z' },
      { id: 'thread-stream-streaming', kind: 'assistant', text: '嗨，我在。直接说要处理的任务', done: false, ts: '2026-03-10T12:00:01Z' }
    ];
    const state = { 
      timelinesByThread: { 'thread-stream': existingTimeline },
      threads: [{ id: 'thread-stream', state: 'responding', isTurnCompletedLocally: false }]
    };
    
    // DB 刚返回的 timeline 中的文本较短（滞后），但增加前一轮消息强行绕过 sameOrNewer 拦截进入合并逻辑
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-stream',
      response: {
        messages: [
          { id: 0, role: 'user', content: '上轮', createdAt: '2026-03-10T11:00:00Z' },
          { id: 1, role: 'user', content: '请求', createdAt: '2026-03-10T12:00:00Z' },
          { id: 2, role: 'assistant', content: '嗨，我在。', createdAt: '2026-03-10T12:00:01Z' }, // 滞后于本地
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    const resultTimeline = state.timelinesByThread['thread-stream'];
    // 断言：DB 中滞后的 assistant 消息被剔除，本地超前的 buffer 被保留，防止重复
    expect(resultTimeline).toHaveLength(3);
    expect(resultTimeline[2].id).toBe('thread-stream-streaming');
    expect(resultTimeline[2].text).toBe('嗨，我在。直接说要处理的任务');
    expect(resultTimeline[2].done).toBe(false);
  });

  it('[regression] drops local buffer and takes over DB message when DB is ahead', () => {
    // 模拟本地由于断网等原因，流缓冲停留在较短的文本
    const existingTimeline = [
      { id: 'thread-stream-user', kind: 'user', text: '请求', ts: '2026-03-10T12:00:00Z' },
      { id: 'thread-stream-streaming', kind: 'assistant', text: '嗨', done: false, ts: '2026-03-10T12:00:01Z' }
    ];
    const state = { 
      timelinesByThread: { 'thread-stream': existingTimeline },
      threads: [{ id: 'thread-stream', state: 'responding', isTurnCompletedLocally: false }]
    };
    
    // DB 返回的 timeline 中的文本更长（超前）
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-stream',
      response: {
        messages: [
          { id: 1, role: 'user', content: '请求', createdAt: '2026-03-10T12:00:00Z' },
          { id: 2, role: 'assistant', content: '嗨，我在。直接说', createdAt: '2026-03-10T12:00:01Z' },
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    const resultTimeline = state.timelinesByThread['thread-stream'];
    // 断言：保留 DB 最新消息，丢弃本地 buffer，但将 DB 消息设为 done: false 以便继续接收 stream
    expect(resultTimeline).toHaveLength(2);
    expect(resultTimeline[1].id).not.toBe('thread-stream-streaming');
    expect(resultTimeline[1].text).toBe('嗨，我在。直接说');
    expect(resultTimeline[1].done).toBe(false); // DB item is taken over as the streaming buffer
  });

  it('[regression] forces state transition cleanly without preserving buffers if turn completed locally', () => {
    const existingTimeline = [
      { id: 'thread-stream-user', kind: 'user', text: '请求', ts: '2026-03-10T12:00:00Z' },
      { id: 'thread-stream-streaming', kind: 'assistant', text: '嗨，我在。直接说要处理的任务或贴报错。即可', done: false, ts: '2026-03-10T12:00:01Z' }
    ];
    const state = { 
      timelinesByThread: { 'thread-stream': existingTimeline },
      // 尽管后端轮询状态还是 responding，但本地刚刚收到了 turn/completed
      threads: [{ id: 'thread-stream', state: 'responding', isTurnCompletedLocally: true }]
    };
    
    // 加一个历史记录，强行让 incoming 更长，以防被拦截
    const applied = applyImmediateTimelineFromMessages({
      threadId: 'thread-stream',
      response: {
        messages: [
          { id: 0, role: 'user', content: '上轮', createdAt: '2026-03-10T11:00:00Z' },
          { id: 1, role: 'user', content: '请求', createdAt: '2026-03-10T12:00:00Z' },
          { id: 2, role: 'assistant', content: '嗨，我在。直接说要处理的任务或贴报错即可。', createdAt: '2026-03-10T12:00:01Z' }, // finalized message
        ],
      },
      state,
      normalizeThreadID: (value) => value,
      freezeTimelineItemsAtomic: (items) => ({ changed: true, items }),
      logInfo: vi.fn(),
    });

    expect(applied).toBe(true);
    const resultTimeline = state.timelinesByThread['thread-stream'];
    // 断言：由于 turn 已经 local completed，不再当作 streaming 对待，直接采纳 DB finalized 状态，无重复
    expect(resultTimeline).toHaveLength(3);
    expect(resultTimeline[2].id).not.toBe('thread-stream-streaming');
    expect(resultTimeline[2].text).toBe('嗨，我在。直接说要处理的任务或贴报错即可。');
    expect(resultTimeline[2].done).not.toBe(false); 
  });
});
