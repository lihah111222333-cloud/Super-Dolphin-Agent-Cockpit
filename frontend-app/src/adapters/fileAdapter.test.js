import { describe, expect, it } from 'vitest';
import { adaptSharedFileDetail } from './fileAdapter.js';

describe('fileAdapter', () => {
  it('rejects malformed shared file detail instead of fabricating path from fallback', () => {
    expect(() => adaptSharedFileDetail({ content: 'latest content' }, { path: 'reports/fallback.md' }))
      .toThrow(/shared file detail path is required/);
  });

  it('keeps display-only fallback fields for valid shared file details', () => {
    expect(adaptSharedFileDetail({ path: 'reports/final.md', content: 'latest content' }, {
      updatedBy: 'agent',
      updatedAt: '2026-07-07T08:00:00Z',
    })).toMatchObject({
      path: 'reports/final.md',
      content: 'latest content',
      updatedBy: 'agent',
      updatedAt: '2026-07-07T08:00:00Z',
    });
  });
});
