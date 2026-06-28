// @ts-nocheck
import { describe, expect, it } from 'vitest';

import {
  defaultLayoutForMode,
  normalizeChatLayout,
  normalizeCmdLayout,
  deriveChatAgents,
  parseAgentBadge,
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

  describe('parseAgentBadge', () => {
    it('splits [label] rest into a pill + remainder', () => {
      const got = parseAgentBadge('[SQL 与数据建模专家] 写一条 JOIN');
      expect(got).toEqual({ label: 'SQL 与数据建模专家', name: '写一条 JOIN' });
    });

    it('returns empty label when no prefix is present', () => {
      expect(parseAgentBadge('新对话')).toEqual({ label: '', name: '新对话' });
    });

    it('handles nullish / empty input without throwing', () => {
      expect(parseAgentBadge(null)).toEqual({ label: '', name: '' });
      expect(parseAgentBadge(undefined)).toEqual({ label: '', name: '' });
      expect(parseAgentBadge('')).toEqual({ label: '', name: '' });
    });

    it('refuses nested brackets so user-typed [foo [bar]] stays as plain name', () => {
      // Our backend never emits nested brackets; if a user types one into the
      // name field we want it left alone rather than split into a false pill.
      expect(parseAgentBadge('[nested [inner]] hi')).toEqual({ label: '', name: '[nested [inner]] hi' });
    });

    it('requires a space after the closing bracket (avoids [a]b false positive)', () => {
      expect(parseAgentBadge('[a]b')).toEqual({ label: '', name: '[a]b' });
    });
  });
});
