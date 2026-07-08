import { describe, expect, it } from 'vitest';
import {
  buildHistoryMessageAttachments,
  dagNodeHistoryFallbackItems,
  extractHistoryMetadata,
  normalizeThreadMessageItems,
  stripHistoryImagePlaceholders,
  threadOpenHistoryFallbackItems } from './threadHistoryTimeline.js';

describe('threadHistoryTimeline', () => {
  it('extracts object metadata and ignores invalid values', () => {
    expect(extractHistoryMetadata({ metadata: { input: [] } })).toEqual({ input: [] });
    expect(extractHistoryMetadata({ metadata: '{"input":[]}' })).toBeNull();
    expect(extractHistoryMetadata({ metadata: null })).toBeNull();
  });

  it('rebuilds image attachments from provider input metadata', () => {
    expect(buildHistoryMessageAttachments({
      metadata: {
        input: [
          { type: 'text', text: 'ignored' },
          { type: 'image', path: '/tmp/image.png' },
          { type: 'localImage', url: '/clipboard/existing.png', name: 'existing' },
          { type: 'localImage', source: 'C:/tmp/local.webp' },
          { type: 'localImage', path: 'C:/Users/ai/AppData/Local/Temp/clipboard-win.png' },
          { type: 'localImage', path: 'C:/Users/ai/AppData/Local/Temp/codex-clipboard-f05.png' },
        ],
      },
    })).toEqual([
      {
        kind: 'image',
        name: 'image.png',
        path: '/tmp/image.png',
        previewUrl: '/clipboard/image.png',
      },
      {
        kind: 'image',
        name: 'existing',
        path: '/clipboard/existing.png',
        previewUrl: '/clipboard/existing.png',
      },
      {
        kind: 'image',
        name: 'local.webp',
        path: 'C:/tmp/local.webp',
        previewUrl: 'C:/tmp/local.webp',
      },
      {
        kind: 'image',
        name: 'clipboard-win.png',
        path: 'C:/Users/ai/AppData/Local/Temp/clipboard-win.png',
        previewUrl: '/clipboard/clipboard-win.png',
      },
      {
        kind: 'image',
        name: 'codex-clipboard-f05.png',
        path: 'C:/Users/ai/AppData/Local/Temp/codex-clipboard-f05.png',
        previewUrl: '/clipboard/codex-clipboard-f05.png',
      },
    ]);
  });

  it('normalizes history messages into visible timeline items', () => {
    const items = normalizeThreadMessageItems([
      {
        id: 'm2',
        role: 'assistant',
        content: 'answer',
        created_at: '2026-06-15T01:00:02Z',
      },
      {
        id: 'm1',
        role: 'user',
        content: 'question <image path="x"></image>',
        created_at: '2026-06-15T01:00:01Z',
        metadata: { input: [{ type: 'localImage', path: '/tmp/image.png' }] },
      },
      {
        id: 'empty',
        role: 'assistant',
        content: '',
      },
    ]);
    expect(items.map((item) => item.id)).toEqual(['m1', 'm2']);
    expect(items[0].text).toBe('question');
    expect(items[0].attachments).toEqual([{
      kind: 'image',
      name: 'image.png',
      path: '/tmp/image.png',
      previewUrl: '/clipboard/image.png',
    }]);
    expect(stripHistoryImagePlaceholders('x <image></image>', false)).toBe('x <image></image>');
  });

  it('builds DAG node fallback prompt and result items', () => {
    const items = dagNodeHistoryFallbackItems('thread-1', {
      node_key: 'node-1',
      title: 'Write report',
      started_at: '2026-06-15T01:00:00Z',
      finished_at: '2026-06-15T01:00:10Z',
      config: { exec: { first_turn: 'write it' } },
      raw: { result: { text: 'done' } },
    });
    expect(items).toHaveLength(2);
    expect(items[0]).toMatchObject({ id: 'dag-node:node-1:prompt', role: 'user', text: 'write it' });
    expect(items[1]).toMatchObject({
      id: 'dag-node:node-1:result',
      role: 'assistant',
      text: 'done',
      title: 'DAG 节点结果：Write report',
    });
  });

  it('only returns fallback items for dag-node history source', () => {
    expect(threadOpenHistoryFallbackItems('thread-1', { source: 'manual' })).toEqual([]);
    expect(threadOpenHistoryFallbackItems('thread-1', {
      source: 'dag-node',
      dagNode: { id: 'node-1', config: { prompt: 'hello' } },
    })).toHaveLength(1);
  });
});
