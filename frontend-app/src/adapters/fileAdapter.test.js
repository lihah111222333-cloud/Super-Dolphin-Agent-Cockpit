import { describe, expect, it } from 'vitest';
import { adaptSharedFileDetail, adaptSharedFilesDashboard } from './fileAdapter.js';
import { parseSharedFilesDashboardResponse } from '../shared/api/backendSchemas.js';

const dashboardFile = { path: 'reports/final.md', content: 'body', updated_by: 'agent' };
const dashboardRetention = {
  items: [{ path: 'reports/final.md', protected: true }],
  protectedCount: 1,
};
const malformedRetention = { items: [{ protected: true }] };

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

  it('normalizes shared files dashboard aliases at the schema boundary', () => {
    const result = parseSharedFilesDashboardResponse({
      memory: [dashboardFile],
      finalOutputRefs: ['reports/final.md'],
      sharedFileRetention: dashboardRetention,
    });

    expect(result.files).toEqual([
      expect.objectContaining({ path: 'reports/final.md', updatedBy: 'agent' }),
    ]);
    expect(result.finalOutputRefs).toEqual([
      expect.objectContaining({ path: 'reports/final.md' }),
    ]);
    expect(result.retention).toEqual(expect.objectContaining({ protectedCount: 1 }));
  });

  it('rejects malformed shared files dashboard fields at the schema boundary', () => {
    expect(() => adaptSharedFilesDashboard({ files: [{ content: 'missing path' }] }))
      .toThrow('shared file item 0 path is required');
    expect(() => adaptSharedFilesDashboard({ files: [], finalOutputRefs: [null] }))
      .toThrow('final output ref 0 must be an object');
    expect(() => adaptSharedFilesDashboard({
      files: [],
      sharedFileRetention: malformedRetention,
    })).toThrow('shared file retention item 0 path is required');
  });

  it('rejects malformed shared file detail instead of fabricating path from fallback', () => {
    expect(() => adaptSharedFileDetail({ content: 'latest content' }, { path: 'reports/fallback.md' }))
      .toThrow(/shared file detail path is required/);
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
