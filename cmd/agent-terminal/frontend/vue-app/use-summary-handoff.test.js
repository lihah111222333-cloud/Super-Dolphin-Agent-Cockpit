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
