// @ts-nocheck
import { describe, expect, it } from 'vitest';

import {
  defaultLayoutForMode,
  normalizeChatLayout,
  normalizeCmdLayout,
  deriveChatAgents,

} from './stores/thread-view.model.js';

describe('thread-view.model', () => {
  it('returns default layouts for chat and cmd modes', () => {
    expect(defaultLayoutForMode('chat')).toBe('focus');
    expect(defaultLayoutForMode('cmd')).toBe('mix');
  });

  it('normalizes chat and cmd layout values', () => {
    expect(normalizeChatLayout('mix')).toBe('mix');
    expect(normalizeChatLayout('overview')).toBe('focus');

    expect(normalizeCmdLayout('overview')).toBe('overview');
    expect(normalizeCmdLayout('chat')).toBe('chat');
    expect(normalizeCmdLayout('focus')).toBe('mix');
  });

  it('derives chat and cmd agents directly', () => {
    const threads = [
      { id: 'main', name: 'Main' },
      { id: 'worker-1', name: 'Worker 1' },
      { id: 'worker-2', name: 'Worker 2' },
    ];

    expect(deriveChatAgents({ threads })).toBe(threads);
  });
});
