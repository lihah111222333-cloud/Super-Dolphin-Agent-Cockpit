// @ts-nocheck
import { describe, expect, it } from 'vitest';
import {
  truncateSummaryText,
  extractTimelineSummary,
  buildSeedInstructionsFromSummary,
} from './composables/useSummaryHandoff.js';

describe('truncateSummaryText', () => {
  it('returns empty string for null/undefined/whitespace', () => {
    expect(truncateSummaryText(null)).toBe('');
    expect(truncateSummaryText(undefined)).toBe('');
    expect(truncateSummaryText('   \n\t  ')).toBe('');
  });

  it('returns text as-is when under limit', () => {
    expect(truncateSummaryText('hello', 10)).toBe('hello');
  });

  it('truncates with ellipsis when over limit', () => {
    expect(truncateSummaryText('abcdefghij', 5)).toBe('abcde…');
  });

  it('uses default 2400 char limit', () => {
    const long = 'x'.repeat(3000);
    const result = truncateSummaryText(long);
    expect(result.length).toBe(2401);
    expect(result.endsWith('…')).toBe(true);
  });
});

describe('extractTimelineSummary', () => {
  it('returns empty for null/empty timeline', () => {
    expect(extractTimelineSummary(null)).toBe('');
    expect(extractTimelineSummary([])).toBe('');
  });

  it('formats single item with role tag', () => {
    const result = extractTimelineSummary([{ role: 'user', text: 'hi' }]);
    expect(result).toBe('[user] hi');
  });

  it('falls back to kind when role missing', () => {
    const result = extractTimelineSummary([{ kind: 'thinking', text: 'pondering' }]);
    expect(result).toBe('[thinking] pondering');
  });

  it('uses content when text missing', () => {
    const result = extractTimelineSummary([{ role: 'assistant', content: 'reply' }]);
    expect(result).toBe('[assistant] reply');
  });

  it('skips items without text/content', () => {
    const result = extractTimelineSummary([
      { role: 'user', text: 'hi' },
      { role: 'tool' },
      { role: 'assistant', text: 'hello' },
    ]);
    expect(result).toContain('[user] hi');
    expect(result).toContain('[assistant] hello');
    expect(result).not.toContain('[tool]');
  });

  it('takes first item plus last N from tail', () => {
    const items = [];
    for (let i = 0; i < 20; i++) items.push({ role: 'user', text: `m${i}` });
    const result = extractTimelineSummary(items, { recentCount: 3 });
    // first message + last 3
    expect(result).toContain('[user] m0');
    expect(result).toContain('[user] m17');
    expect(result).toContain('[user] m18');
    expect(result).toContain('[user] m19');
    expect(result).not.toContain('[user] m10');
  });

  it('respects char limit', () => {
    const items = [{ role: 'user', text: 'x'.repeat(5000) }];
    const result = extractTimelineSummary(items, { charLimit: 100 });
    expect(result.length).toBeLessThanOrEqual(101);
    expect(result.endsWith('…')).toBe(true);
  });

  it('formats tool item with name + preview + status', () => {
    const result = extractTimelineSummary([
      { id: 't1', kind: 'tool', tool: 'Read', preview: 'src/foo.js', status: 'ok' },
    ]);
    expect(result).toContain('[tool]');
    expect(result).toContain('Read');
    expect(result).toContain('src/foo.js');
  });

  it('marks failed tool with status flag', () => {
    const result = extractTimelineSummary([
      { id: 't1', kind: 'tool', tool: 'Bash', preview: 'rm /', status: 'failed' },
    ]);
    expect(result).toContain('[tool 失败]');
  });

  it('formats command item with command + exitCode + truncated output', () => {
    const result = extractTimelineSummary([
      { id: 'c1', kind: 'command', command: 'npm test', exitCode: 0, output: 'all green' },
    ]);
    expect(result).toContain('[cmd]');
    expect(result).toContain('$ npm test');
    expect(result).toContain('(exit=0)');
    expect(result).toContain('all green');
  });

  it('formats plan item with done flag', () => {
    const items = [
      { id: 'u1', role: 'user', text: 'start' },
      { id: 'p1', kind: 'plan', text: 'step A', done: true },
      { id: 'p2', kind: 'plan', text: 'step B', done: false },
      { id: 'u2', role: 'user', text: 'continue' },
    ];
    const result = extractTimelineSummary(items);
    expect(result).toContain('[plan 完成] step A');
    expect(result).toContain('[plan 进行中] step B');
  });

  it('always includes ALL plan items even when outside recent N window', () => {
    const items = [];
    items.push({ id: 'u0', role: 'user', text: 'task start' });
    items.push({ id: 'p1', kind: 'plan', text: 'early plan', done: true });
    for (let i = 0; i < 30; i++) items.push({ id: `t${i}`, role: 'user', text: `msg ${i}` });
    const result = extractTimelineSummary(items, { recentCount: 5 });
    // early plan should still appear despite being far from tail
    expect(result).toContain('[plan 完成] early plan');
    expect(result).toContain('[user] task start');
    expect(result).toContain('[user] msg 29');
  });

  it('deduplicates items appearing in both first / plan / tail buckets', () => {
    const planItem = { id: 'only-one', kind: 'plan', text: 'lone plan', done: false };
    const result = extractTimelineSummary([planItem]);
    // first + plan + tail all match the same item; should appear exactly once
    const occurrences = (result.match(/\[plan/g) || []).length;
    expect(occurrences).toBe(1);
  });

  it('clips long output of a command before applying global char limit', () => {
    const result = extractTimelineSummary([
      { id: 'c1', kind: 'command', command: 'ls', output: 'X'.repeat(5000) },
    ]);
    // PER_ITEM_FIELD_LIMIT 280 ± ellipsis means single line stays < ~400 chars, much under default 2400
    expect(result.length).toBeLessThan(500);
  });
});

describe('extractTimelineSummary · progress section', () => {
  // 真实 timeline 由 historyMessageToTimelineItem 生成（thread-history-ui.js:186-193），
  // 形状是 { id, kind: 'user'|'assistant', text, ts }——不带 role 字段。
  // 之前的 fixture 用 `role:` 与生产脱钩，导致进度段抽取在生产里静默失效（07e3220 回归）。
  // 下方所有 fixture 一律按生产形状构造。
  function buildToolStormTimeline() {
    const items = [
      { id: 'u-init', kind: 'user', text: '帮我重构 utils/format-utils.js 里的 token 分级函数', ts: '2026-04-28T10:00:00Z' },
      { id: 'a-mid', kind: 'assistant', text: '我已经读了 format-utils.js，准备把 getTokenLevel 拆成两个分函数并补上阈值参数。', ts: '2026-04-28T10:00:01Z' },
    ];
    for (let i = 0; i < 15; i++) {
      items.push({ id: `t${i}`, kind: 'tool', tool: 'Read', preview: `file${i}.js` });
    }
    return items;
  }

  it('appends a progress section that survives a tool storm tail', () => {
    const result = extractTimelineSummary(buildToolStormTimeline());
    expect(result).toContain('## 最近进展');
    expect(result).toContain('最近用户诉求');
    expect(result).toContain('重构 utils/format-utils.js');
    expect(result).toContain('助手当前思路');
    expect(result).toContain('getTokenLevel 拆成两个分函数');
  });

  it('skips short assistant replies (<40 chars) when picking current thinking', () => {
    const items = [
      { id: 'u1', kind: 'user', text: '重构 X', ts: '2026-04-28T10:00:00Z' },
      { id: 'a1', kind: 'assistant', text: '已理解，详细分析后设计了三步拆分方案。', ts: '2026-04-28T10:00:01Z' }, // 足够长
      { id: 'a2', kind: 'assistant', text: '好的', ts: '2026-04-28T10:00:02Z' }, // 太短应跳过
      { id: 't1', kind: 'tool', tool: 'Read', preview: 'a.js' },
      { id: 't2', kind: 'tool', tool: 'Edit', preview: 'b.js' },
    ];
    const result = extractTimelineSummary(items);
    expect(result).toContain('## 最近进展');
    // 应选中长的 a1，不是最后一条 a2
    expect(result).toContain('详细分析后设计了三步拆分方案');
    // a2 「好的」不该出现在「助手当前思路」行里
    expect(result).not.toMatch(/助手当前思路：好的/);
  });

  it('does NOT append progress section for short timelines (<5 items)', () => {
    const items = [
      { id: 'u1', kind: 'user', text: '你好', ts: '2026-04-28T10:00:00Z' },
      { id: 'a1', kind: 'assistant', text: '你好，需要我做什么？', ts: '2026-04-28T10:00:01Z' },
    ];
    const result = extractTimelineSummary(items);
    expect(result).not.toContain('## 最近进展');
  });

  it('includes recent plan items in progress section with done flag', () => {
    const items = [{ id: 'u-init', kind: 'user', text: 'task start', ts: '2026-04-28T10:00:00Z' }];
    for (let i = 0; i < 10; i++) {
      items.push({ id: `t${i}`, kind: 'tool', tool: 'Bash', preview: `step ${i}` });
    }
    items.push({ id: 'p-done', kind: 'plan', text: '实现 thresholds 设置 UI', done: true });
    items.push({ id: 'p-active', kind: 'plan', text: '接入 banner 事件路由', done: false });
    const result = extractTimelineSummary(items);
    expect(result).toContain('## 最近进展');
    expect(result).toContain('进度【已完成】：实现 thresholds 设置 UI');
    expect(result).toContain('进度【进行中】：接入 banner 事件路由');
  });

  // 回归测试：旧 fixture 用 role 让进度段抽取在生产里失效。这条用 thread-history-ui.js 真实输出形态，
  // 防止以后再脱钩。
  it('extracts user / assistant from production-shape items (kind only, no role)', () => {
    const items = [
      { id: 'thread-history-1', kind: 'user', text: '排查 fork 摘要里没有进度段的问题', ts: '2026-04-28T10:00:00Z' },
      { id: 'thread-history-2', kind: 'assistant', text: '已确认 historyMessageToTimelineItem 只产出 kind 不产出 role，定位到 extractProgressSection 的谓词只看 role 所以漏。', ts: '2026-04-28T10:00:01Z' },
      { id: 'thread-history-3', kind: 'user', text: '修复并加回归测试', ts: '2026-04-28T10:00:02Z' },
      { id: 't1', kind: 'tool', tool: 'Read', preview: 'useSummaryHandoff.js' },
      { id: 't2', kind: 'tool', tool: 'Edit', preview: 'useSummaryHandoff.js' },
    ];
    const result = extractTimelineSummary(items);
    expect(result).toContain('## 最近进展');
    expect(result).toContain('最近用户诉求');
    expect(result).toContain('修复并加回归测试');
    expect(result).toContain('助手当前思路');
    expect(result).toContain('historyMessageToTimelineItem');
  });

  // 双字段（既有 role 也有 kind）兼容性：以 role 优先，确保旧 fixture 仍可被识别。
  it('also accepts items that carry both role and kind (legacy fixture compatibility)', () => {
    const items = [
      { id: 'u1', role: 'user', kind: 'user', text: '同时带 role 和 kind 的消息也要被识别' },
      { id: 'a1', role: 'assistant', kind: 'assistant', text: '这是一段足够长的助手回复，用来通过 40 字符的进度段长度门槛过滤。' },
      { id: 't1', kind: 'tool', tool: 'Read', preview: 'a.js' },
      { id: 't2', kind: 'tool', tool: 'Read', preview: 'b.js' },
      { id: 't3', kind: 'tool', tool: 'Read', preview: 'c.js' },
    ];
    const result = extractTimelineSummary(items);
    expect(result).toContain('## 最近进展');
    expect(result).toContain('同时带 role 和 kind 的消息');
    expect(result).toContain('40 字符的进度段长度门槛');
  });
});

describe('buildSeedInstructionsFromSummary', () => {
  it('returns empty string when both summary and sharedFiles empty', () => {
    expect(buildSeedInstructionsFromSummary('')).toBe('');
    expect(buildSeedInstructionsFromSummary('', { sharedFiles: [] })).toBe('');
  });

  it('returns empty when sharedFiles all empty/invalid', () => {
    expect(
      buildSeedInstructionsFromSummary('', {
        sharedFiles: [{ path: '', content: 'x' }, { path: 'a.md', content: '' }],
      }),
    ).toBe('');
  });

  it('includes summary block with default source title', () => {
    const out = buildSeedInstructionsFromSummary('test summary');
    expect(out).toContain('前一个对话');
    expect(out).toContain('摘要：');
    expect(out).toContain('test summary');
  });

  it('uses custom sourceTitle and intro', () => {
    const out = buildSeedInstructionsFromSummary('s', {
      sourceTitle: 'Foo',
      intro: 'Custom intro.',
    });
    expect(out).toContain('Custom intro.');
    expect(out).toContain('来源：Foo');
  });

  it('wraps shared files with four-backtick fence (avoiding triple-backtick collision)', () => {
    const out = buildSeedInstructionsFromSummary('s', {
      sharedFiles: [{ path: 'memo.md', content: 'has ``` triple ticks inside' }],
    });
    expect(out).toContain('共享文件：memo.md');
    expect(out).toContain('````');
    // four-backtick fence should bracket content even though content has ```
    const matches = out.match(/````/g);
    expect(matches?.length).toBe(2);
    expect(out).toContain('has ``` triple ticks inside');
  });

  it('includes both summary and shared files when both present', () => {
    const out = buildSeedInstructionsFromSummary('the summary', {
      sourceTitle: 'src',
      sharedFiles: [{ path: 'a.md', content: 'aaa' }, { path: 'b.md', content: 'bbb' }],
    });
    expect(out).toContain('摘要：');
    expect(out).toContain('the summary');
    expect(out).toContain('挂载的共享文件');
    expect(out).toContain('共享文件：a.md');
    expect(out).toContain('共享文件：b.md');
  });

  it('truncates summary to default limit', () => {
    const longSummary = 'x'.repeat(3000);
    const out = buildSeedInstructionsFromSummary(longSummary);
    expect(out).toContain('xxxx');
    expect(out).toContain('…');
  });
});
