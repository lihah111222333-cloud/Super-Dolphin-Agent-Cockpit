import { describe, expect, it } from 'vitest';
import {
  dedupeAssistantTimelineItems,
  isVisibleTimelineItem,
  mergeTimelineItems,
  normalizeTimelineItem,
  sortTimelineChronologically } from './timelineRuntime.js';

describe('timelineRuntime', () => {
  it('normalizes user and assistant timeline payloads from backend-shaped fields', () => {
    const hiddenUser = normalizeTimelineItem({
      id: 'user-hidden',
      role: 'user',
      content: '<turn_aborted></turn_aborted><image name="shot"></image>',
      created_at: '2026-06-15T01:00:00.123456Z',
    });

    expect(hiddenUser).toMatchObject({
      id: 'user-hidden',
      role: 'user',
      kind: 'user',
      text: '',
      controlOnly: true,
      done: true,
    });

    const tool = normalizeTimelineItem({
      message_id: 'tool-1',
      type: 'tool',
      output: { text: 'created file' },
      tool_call_id: 'call-1',
      status: 'completed',
      elapsed_ms: '42',
      started_at: '2026-06-15T01:00:01Z',
    });

    expect(tool).toMatchObject({
      id: 'tool-1',
      role: 'assistant',
      kind: 'tool',
      text: 'created file',
      callId: 'call-1',
      done: true,
      elapsedMs: 42,
    });
  });

  it('filters timeline items that should not render in the chat stream', () => {
    expect(isVisibleTimelineItem({ role: 'user', controlOnly: true })).toBe(false);
    expect(isVisibleTimelineItem({
      role: 'user',
      text: '# AGENTS.md instructions for D:\\project\\Super-Dolphin\n<INSTRUCTIONS>rules</INSTRUCTIONS>',
    })).toBe(false);
    expect(isVisibleTimelineItem({
      role: 'user',
      text: '<recommended_plugins>plugins</recommended_plugins># AGENTS.md instructions for D:\\project\\Super-Dolphin\n<INSTRUCTIONS>rules</INSTRUCTIONS>',
    })).toBe(false);
    expect(isVisibleTimelineItem({ role: 'user', text: '<recommended_plugins>plugins</recommended_plugins>' })).toBe(false);
    expect(isVisibleTimelineItem({ itemType: 'message', text: 'backend lifecycle item' })).toBe(false);
    expect(isVisibleTimelineItem({ role: 'assistant', kind: 'command', title: 'command' })).toBe(false);
    expect(isVisibleTimelineItem({ role: 'assistant', kind: 'command', title: '$ npm test' })).toBe(true);
    expect(isVisibleTimelineItem({ role: 'assistant', kind: 'thinking' })).toBe(true);
  });

  it('normalizes internal hook prompts as hidden control messages', () => {
    const hookPrompt = normalizeTimelineItem({
      id: 'hook-prompt',
      role: 'user',
      content: '<hook_prompt hook_run_id="stop:1">internal stop gate details</hook_prompt>',
    });

    expect(hookPrompt).toMatchObject({
      id: 'hook-prompt',
      role: 'user',
      text: '',
      controlOnly: true,
    });
    expect(isVisibleTimelineItem(hookPrompt)).toBe(false);
  });

  it('keeps backend approval requests visible even when the text is empty', () => {
    const approval = normalizeTimelineItem({
      id: 'approval-42',
      kind: 'approval',
      status: 'pending',
      requestId: 42,
      text: '',
    });

    expect(approval).toEqual(expect.objectContaining({
      id: 'approval-42',
      kind: 'approval',
      requestId: 42,
      status: 'pending',
      text: '',
    }));
    expect(isVisibleTimelineItem(approval)).toBe(true);
  });

  it('deduplicates repeated assistant content within one user turn', () => {
    const items = dedupeAssistantTimelineItems([
      { id: 'u1', role: 'user', kind: 'user', text: 'build it', time: '2026-06-15T01:00:00Z' },
      { id: 'a1', role: 'assistant', kind: 'assistant', text: 'working on it', done: false, time: '2026-06-15T01:00:01Z' },
      { id: 'a2', role: 'assistant', kind: 'assistant', text: 'working on it', done: true, time: '2026-06-15T01:00:02Z' },
      { id: 'u2', role: 'user', kind: 'user', text: 'next', time: '2026-06-15T01:00:03Z' },
      { id: 'a3', role: 'assistant', kind: 'assistant', text: 'working on it', done: true, time: '2026-06-15T01:00:04Z' },
    ]);

    expect(items.map((item) => item.id)).toEqual(['u1', 'a1', 'u2', 'a3']);
    expect(items[1]).toMatchObject({ id: 'a1', done: true });
  });

  it('merges incoming timeline items while preserving local attachments', () => {
    const imageAttachment = { name: 'shot.png', mime: 'image/png' };
    const merged = mergeTimelineItems([
      {
        id: 'assistant-old',
        role: 'assistant',
        kind: 'assistant',
        text: 'same answer',
        time: '2026-06-15T01:00:00Z',
        attachments: [imageAttachment],
      },
    ], [
      {
        id: 'assistant-new',
        role: 'assistant',
        kind: 'assistant',
        text: 'same answer',
        time: '2026-06-15T01:00:01Z',
      },
    ], { preserveExistingVisible: true });

    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      id: 'assistant-new',
      attachments: [imageAttachment],
    });
  });

  it('coalesces lifecycle items by kind and call id', () => {
    const merged = mergeTimelineItems([
      {
        id: 'tool-start',
        role: 'assistant',
        kind: 'tool',
        callId: 'call-1',
        text: '',
        status: 'running',
        time: '2026-06-15T01:00:00Z',
        done: false,
      },
    ], [
      {
        id: 'tool-done',
        role: 'assistant',
        kind: 'tool',
        callId: 'call-1',
        text: 'created report',
        status: 'completed',
        time: '2026-06-15T01:00:01Z',
        completedAt: '2026-06-15T01:00:02Z',
      },
    ], { preserveExistingVisible: true });

    expect(merged).toHaveLength(1);
    expect(merged[0]).toMatchObject({
      id: 'tool-done',
      callId: 'call-1',
      text: 'created report',
      status: 'completed',
      time: '2026-06-15T01:00:00Z',
      completedAt: '2026-06-15T01:00:02Z',
      done: true,
    });
  });

  it('sorts timeline items chronologically and keeps input order for equal timestamps', () => {
    expect(sortTimelineChronologically([
      { id: 'late', time: '2026-06-15T01:00:02Z' },
      { id: 'early', time: '2026-06-15T01:00:01Z' },
      { id: 'same-a' },
      { id: 'same-b' },
    ]).map((item) => item.id)).toEqual(['same-a', 'same-b', 'early', 'late']);
  });

  it('fails fast for invalid timeline timestamps', () => {
    expect(() => sortTimelineChronologically([
      { id: 'bad-time', time: 'not-a-date' },
    ])).toThrow(/ISO-8601 UTC timestamp/);
  });
});
