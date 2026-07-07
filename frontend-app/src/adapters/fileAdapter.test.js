import { describe, expect, it } from 'vitest';
import { adaptSharedFileDetail } from './fileAdapter.js';

describe('fileAdapter', () => {
  it('adapts direct shared file detail responses', () => {
    expect(adaptSharedFileDetail({
      path: 'notes/a.md',
      content: 'hello',
      updatedBy: 'agent',
      updatedAt: '2026-07-07T08:00:00Z',
    })).toEqual({
      id: 'notes/a.md:0',
      path: 'notes/a.md',
      content: 'hello',
      updatedBy: 'agent',
      updatedAt: '2026-07-07T08:00:00Z',
      createdAt: '',
    });
  });

  it('adapts nested shared file detail responses', () => {
    expect(adaptSharedFileDetail({
      file: {
        path: 'notes/a.md',
        content: 'hello',
      },
    })).toMatchObject({
      path: 'notes/a.md',
      content: 'hello',
    });
  });

  it('rejects missing or malformed detail responses', () => {
    expect(() => adaptSharedFileDetail(undefined)).toThrow('shared file detail response must be an object');
    expect(() => adaptSharedFileDetail({})).toThrow('shared file detail path is required');
    expect(() => adaptSharedFileDetail({ file: null })).toThrow('shared file detail file must be an object');
    expect(() => adaptSharedFileDetail({ content: 'hello' }, { path: 'notes/a.md' })).toThrow('shared file detail path is required');
  });

  it('keeps display-only fallback fields for valid shared file details', () => {
    expect(adaptSharedFileDetail({ path: 'reports/final.md', content: 'latest content' }, {
      path: 'reports/fallback.md',
      content: 'old content',
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
