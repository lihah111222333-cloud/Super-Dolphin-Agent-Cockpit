import { describe, expect, it } from 'vitest';
import { codePreviewStateAfterSave } from './codePreviewAdapter.js';

describe('codePreviewStateAfterSave', () => {
  it('keeps edits typed during an in-flight save dirty instead of marking them saved', () => {
    const current = {
      saving: true,
      filePath: 'docs/plan.md',
      relative: 'docs/plan.md',
      content: 'old saved content',
      draft: 'saved snapshot\nnew unsaved edit',
      previewKind: 'markdown',
      editing: true,
      totalLines: 1,
    };

    const next = codePreviewStateAfterSave(
      current,
      { filePath: 'docs/plan.md', totalLines: 2 },
      'docs/plan.md',
      'saved snapshot',
    );

    expect(next.saving).toBe(false);
    expect(next.content).toBe('saved snapshot');
    expect(next.draft).toBe('saved snapshot\nnew unsaved edit');
    expect(next.editing).toBe(true);
    expect(next.totalLines).toBe(2);
    expect(next.status).toBe('已保存 docs/plan.md，仍有未保存更改');
  });

  it('exits markdown editing after save only when the draft did not change during save', () => {
    const current = {
      saving: true,
      filePath: 'docs/plan.md',
      relative: 'docs/plan.md',
      content: 'old saved content',
      draft: 'saved snapshot',
      previewKind: 'markdown',
      editing: true,
      totalLines: 1,
    };

    const next = codePreviewStateAfterSave(current, {}, 'docs/plan.md', 'saved snapshot');

    expect(next.content).toBe('saved snapshot');
    expect(next.draft).toBe('saved snapshot');
    expect(next.editing).toBe(false);
    expect(next.totalLines).toBe(1);
    expect(next.status).toBe('已保存 docs/plan.md');
  });
});
