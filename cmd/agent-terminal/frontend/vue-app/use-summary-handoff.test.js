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

  it('uses default 4000 char limit', () => {
    const long = 'x'.repeat(5000);
    const result = truncateSummaryText(long);
    expect(result.length).toBe(4001);
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

  // review P1 #3：picked 总长超 charLimit 时保留尾部（fork 意图 = 最近内容优先）
  it('picked 超 charLimit 时保留尾部并加 … 前缀（fork 最近优先）', () => {
    const items = [
      // 首条 + 12 条尾部（每条短，便于精确算总长）
      { id: 'u0', kind: 'user', text: 'EARLIEST_USER' },
      ...Array.from({ length: 11 }, (_, i) => ({ id: `m${i}`, kind: 'assistant', text: `mid${i}_padding_padding` })),
      { id: 'a-final', kind: 'assistant', text: 'LATEST_ASSISTANT_CONCLUSION' },
    ];
    // charLimit 设小，确保超额
    const result = extractTimelineSummary(items, { charLimit: 80 });
    // 主摘要部分（## 最近进展 之前）只断言尾部保留 + 头部被截。
    // 进度段会基于完整 items 单独抽取，可能仍包含首条 user——那段不在主摘要 truncate 范围。
    const mainPart = result.split('## 最近进展')[0];
    expect(mainPart).toContain('LATEST_ASSISTANT_CONCLUSION');
    expect(mainPart).not.toContain('EARLIEST_USER'); // 主摘要超额时舍弃头部
    expect(mainPart.startsWith('…')).toBe(true);     // 主摘要前缀 … 标记截断
  });

  it('picked 不超 charLimit 时不加 … 前缀（不破不需要的场景）', () => {
    const items = [
      { id: 'u0', kind: 'user', text: 'short' },
      { id: 'a0', kind: 'assistant', text: 'reply' },
    ];
    const result = extractTimelineSummary(items, { charLimit: 4000 });
    expect(result.startsWith('…')).toBe(false);
    expect(result).toContain('short');
    expect(result).toContain('reply');
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

  // Phase 4-fork-summary：tool item 必须把 output（实质结果）抽进摘要，
  // 不能只抽 tool name 让 agent 看到 [tool] lsp_file 这种空壳
  it('抽 tool.output（agent 主动开场需要的实质内容）', () => {
    const result = extractTimelineSummary([
      { id: 't1', kind: 'tool', tool: 'lsp_file', output: 'read src/foo.js → 230 lines, function getTokenLevel() at L42' },
    ]);
    expect(result).toContain('[tool]');
    expect(result).toContain('lsp_file');
    expect(result).toContain('getTokenLevel');
    expect(result).toContain('L42');
  });

  it('tool 含 output + preview + file 时优先 output', () => {
    const result = extractTimelineSummary([
      { id: 't1', kind: 'tool', tool: 'Read', output: 'real result line', preview: 'preview only', file: 'src/foo.js' },
    ]);
    // output 是真理；preview/file 不应同时出现避免噪声
    expect(result).toContain('real result line');
    expect(result).not.toContain('preview only');
  });

  it('tool 长 output 被 LONG_FIELD_LIMIT 600 约束（不会被旧 280 狠截）', () => {
    const longOutput = 'A'.repeat(550); // 550 < 600，应完整保留
    const result = extractTimelineSummary([
      { id: 't1', kind: 'tool', tool: 'Read', output: longOutput },
    ]);
    expect(result).toContain('A'.repeat(550)); // 完整保留 550 字（旧 280 会狠截）
  });

  it('plan / TodoWrite 长 text 不被旧 280 狠截（用 LONG_FIELD_LIMIT 600）', () => {
    const longTodo = '## TodoWrite\n1. 改 A\n2. 修 B\n3. 测 C\n4. ' + 'X'.repeat(400);
    const result = extractTimelineSummary([
      { id: 'p1', kind: 'plan', text: longTodo, done: false },
    ]);
    expect(result).toContain('TodoWrite');
    expect(result).toContain('改 A');
    expect(result).toContain('修 B');
    expect(result).toContain('测 C');
  });

  it('assistant 长结论不被旧 280 狠截（agent 承接需要完整结论）', () => {
    const longConclusion = '上次结论：经过排查，问题在 shared file 读取的 NotFound 显式错误路径，' + 'X'.repeat(400);
    const result = extractTimelineSummary([
      { id: 'a1', kind: 'assistant', text: longConclusion },
    ]);
    expect(result).toContain('上次结论');
    expect(result).toContain('shared file');
    expect(result).toContain('NotFound');
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
    // LONG_FIELD_LIMIT 600 ± ellipsis：单行 command output ≈ 700 chars，远低于
    // DEFAULT_SUMMARY_LIMIT 4000；放宽 1000 留 buffer 反映 Phase 2a 取舍
    expect(result.length).toBeLessThan(1000);
    expect(result.length).toBeGreaterThan(550); // 验证确实拿到了 long-field 体量的输出
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
    const longSummary = 'x'.repeat(5000); // 超过 4000 默认 limit 才能触发截断
    const out = buildSeedInstructionsFromSummary(longSummary);
    expect(out).toContain('xxxx');
    expect(out).toContain('…');
  });
});
