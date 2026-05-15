// @ts-nocheck
/**
 * assistant-markdown 回归测试
 *
 * 验证：
 * - 正常 markdown 不被误伤
 * - 工具调用型 / 进度型思考泄漏都能被识别
 * - 长串泄漏文本会被断句，Markdown 不再渲染成一大坨
 */

import { describe, it, expect, vi } from 'vitest';
import {
  isLikelyReasoningLeakText,
  normalizeReasoningText,
  renderAssistantMarkdown,
} from './utils/assistant-markdown.js';
import { createStreamingMarkdownStateResolver } from './utils/assistant-markdown-streaming.js';


const leakedProgressText = [
  'I’m resuming from the current filesystem state and verifying the two exact phase-3 surfaces now: the page helper/export block and the production chat-rail integration block, plus the cleaned integration test file.',
  'I found the helper is currently living at the wrong level in the file.',
  'I’m locating that stray definition around the setup prelude so I can move it back to the top-level utility section and leave only one canonical copy.',
  'I found the file has already evolved to a different phase-3 integration shape: a top-level `buildVisibleChatThreadCards(opts)` helper plus a `visibleChatThreadCardState` computed.',
  'I’m inspecting that live path directly now instead of forcing the earlier helper signature.',
  'The live path is now clearly helper-based and the module export resolves correctly in Node.',
  'I’m rerunning the two exact phase-3 test files from the current state.',
  'Phase-3 tests are green from the current state.',
  'I’m doing a split diagnostics pass so I can confirm the production page file separately from the test-file hints.',
  'I’ve stabilized the phase-3 integration.',
  'Next I’m doing exactly the requested follow-up: clean the hint-level TS issues in `unified-chat-component.test.js`, and add a toggle regression test for archived rail switching.',
].join('');

describe('normalizeReasoningText', () => {
  it('returns empty string for null/undefined/empty', () => {
    expect(normalizeReasoningText(null)).toBe('');
    expect(normalizeReasoningText(undefined)).toBe('');
    expect(normalizeReasoningText('')).toBe('');
  });

  it('returns normal text unchanged', () => {
    const text = '这是一段正常的助手回复，不包含任何工具调用。Hello world!';
    expect(normalizeReasoningText(text)).toBe(text);
  });

  it('does not activate for single tool call', () => {
    const text = '查看文件 read_file(offset=0) 看一下内容。';
    expect(normalizeReasoningText(text)).toBe(text);
  });

  it('inserts line breaks for ≥2 tool calls in one continuous string', () => {
    const text = '现在查 providerAdapter.ThreadStart。read_file(offset=0) // 来自 lsp_grep line=23。再下钻到 service 层。read_file(offset=229) // 来自 lsp_structure line=304。';
    const result = normalizeReasoningText(text);

    expect(result).toContain('`read_file(offset=0) // 来自 lsp_grep line=23`');
    expect(result).toContain('`read_file(offset=229) // 来自 lsp_structure line=304`');
    expect(result).toContain('现在查 providerAdapter.ThreadStart');
    expect(result).toContain('再下钻到 service 层');
  });

  it('handles text already containing newlines', () => {
    const text = '第一段。\nread_file(offset=0) // 来自 lsp_grep\n第二段。\nread_file(offset=100) // 来自 lsp_structure';
    const result = normalizeReasoningText(text);
    expect(result).toContain('`read_file(offset=0) // 来自 lsp_grep`');
    expect(result).toContain('`read_file(offset=100) // 来自 lsp_structure`');
  });

  it('does not produce excessive consecutive blank lines', () => {
    const text = 'read_file(offset=0) // a\n\nread_file(offset=1) // b\n\nread_file(offset=2) // c';
    const result = normalizeReasoningText(text);
    expect(result).not.toMatch(/\n{3,}/);
  });

  it('preserves normal markdown content', () => {
    const text = '# 标题\n\n这是正常的 markdown 内容。\n\n- 列表项 1\n- 列表项 2';
    expect(normalizeReasoningText(text)).toBe(text);
    expect(isLikelyReasoningLeakText(text)).toBe(false);
  });

  it('wraps lsp-prefixed tool calls in backticks', () => {
    const text = '先查结构。lsp_grep(query="foo") // 搜索。再看定义。lsp_structure(file="bar.go") // 结构。';
    const result = normalizeReasoningText(text);
    expect(result).toContain('`lsp_grep(query="foo") // 搜索`');
    expect(result).toContain('`lsp_structure(file="bar.go") // 结构`');
  });

  it('handles the exact user-reported problematic text with tool calls', () => {
    const text = '现在查 providerAdapter.ThreadStart 与 turn/start 的实现，确认 thread id / providerThreadId / manager 绑定有没有断链。read_file(offset=0) // 来自 lsp_grep line=23 与 line=48，两个目标都在文件前 80 行内，可从头读取。再下钻到 service 层：看 RunThreadStart 怎么建绑定，以及 TurnStart 对 threadID 的校验路径。read_file(offset=229) // 来自 lsp_structure line=304，按规则取 304-75=229。继续下钻 runtimesvc.TurnStart / EnsureThreadReadyForTurn，这里最可能决定“能不能发消息”。read_file(offset=184) // 来自 lsp_structure line=259，按规则取 259-75=184，覆盖 EnsureThreadReadyForTurn 与 TurnStart。';
    const result = normalizeReasoningText(text);

    expect(result).toContain('\n');
    expect(result).toContain('`read_file(offset=0)');
    expect(result).toContain('`read_file(offset=229)');
    expect(result).toContain('`read_file(offset=184)');
  });

  it('detects leaked progress-style reasoning without tool calls', () => {
    expect(isLikelyReasoningLeakText(leakedProgressText)).toBe(true);
    const result = normalizeReasoningText(leakedProgressText);
    expect(result).toContain('\n\n');
    expect(result).toContain('buildVisibleChatThreadCards(opts)');
    expect(result).toContain('unified-chat-component.test.js');
  });

  it('renders merged leaked progress text as multiple markdown paragraphs', () => {
    const html = renderAssistantMarkdown(leakedProgressText);
    const paragraphCount = html.match(/<p>/g)?.length || 0;
    expect(paragraphCount).toBeGreaterThan(1);
    expect(html).toContain('chat-md-inline-code');
    expect(html).toContain('buildVisibleChatThreadCards(opts)');
  });
  it('coalesces streaming markdown state updates until the next frame', () => {
    vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', undefined);
    vi.stubGlobal('cancelAnimationFrame', undefined);
    const flushes = [];
    const resolve = createStreamingMarkdownStateResolver(() => '', () => flushes.push('flushed'));
    const first = resolve({ id: 'assistant-1', kind: 'assistant', text: 'Hello\n', done: false });
    const second = resolve({ id: 'assistant-1', kind: 'assistant', text: 'Hello\nWorld', done: false });
    expect(second).toBe(first);
    vi.advanceTimersByTime(32);
    const third = resolve({ id: 'assistant-1', kind: 'assistant', text: 'Hello\nWorld', done: false });
    expect(flushes).toEqual(['flushed']);
    expect(third).not.toBe(first);
    expect(third.text).toBe('Hello\nWorld');
    expect(third.heightPx).toBeGreaterThanOrEqual(0);
    resolve.dispose?.();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('stale-guard forces flush after 200ms backstop', () => {
    vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', undefined);
    vi.stubGlobal('cancelAnimationFrame', undefined);
    const flushes = [];
    const resolve = createStreamingMarkdownStateResolver(() => '', () => flushes.push('flushed'));
    // Initial call — first seen, renders immediately
    resolve({ id: 'a1', kind: 'assistant', text: 'Hello\n', done: false });
    // Second call with new text — returns stale, schedules 32ms flush + 200ms backstop
    resolve({ id: 'a1', kind: 'assistant', text: 'Hello\nWorld\n', done: false });
    expect(flushes).toEqual([]);
    // Advance 32ms — normal flush fires, stale-guard should be cleared
    vi.advanceTimersByTime(32);
    expect(flushes).toEqual(['flushed']);
    // After flush, resolve with same text returns current (non-stale) state
    const current = resolve({ id: 'a1', kind: 'assistant', text: 'Hello\nWorld\n', done: false });
    expect(current.text).toBe('Hello\nWorld\n');
    expect(current.heightPx).toBeGreaterThanOrEqual(0);
    // New text again — schedules 32ms flush + 200ms backstop
    flushes.length = 0;
    resolve({ id: 'a1', kind: 'assistant', text: 'Hello\nWorld\nFoo\n', done: false });
    // Normal flush fires at 32ms
    vi.advanceTimersByTime(32);
    expect(flushes).toEqual(['flushed']);
    // 200ms backstop should NOT double-fire since normal flush already cleared it
    flushes.length = 0;
    vi.advanceTimersByTime(200);
    expect(flushes).toEqual([]);
    resolve.dispose?.();
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  it('dispose clears stale-guard timer without leaking', () => {
    vi.useFakeTimers();
    vi.stubGlobal('requestAnimationFrame', undefined);
    vi.stubGlobal('cancelAnimationFrame', undefined);
    const flushes = [];
    const resolve = createStreamingMarkdownStateResolver(() => '', () => flushes.push('flushed'));
    resolve({ id: 'b1', kind: 'assistant', text: 'Hi\n', done: false });
    resolve({ id: 'b1', kind: 'assistant', text: 'Hi\nBye\n', done: false });
    // Dispose before any timer fires
    resolve.dispose?.();
    vi.advanceTimersByTime(300);
    // No flush should have fired after dispose
    expect(flushes).toEqual([]);
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
});
