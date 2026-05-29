import { describe, expect, it } from 'vitest';
import { applyComposerCitationAction, buildComposerTextFromCitation } from './utils/citation-action-utils.js';

describe('citation action utils', () => {
  it('builds composer text for task, code-comment, and automation directives', () => {
    expect(buildComposerTextFromCitation({ kind: 'task', prompt: 'Review the patch' })).toBe('Review the patch');
    expect(buildComposerTextFromCitation({ kind: 'code-comment', title: 'Naming', path: 'src/main.go', message: 'Please rename this' }))
      .toBe('Code comment (src/main.go): Naming\nPlease rename this');
    expect(buildComposerTextFromCitation({ kind: 'automation-update', title: 'Nightly lint', prompt: 'Run lint on main' }))
      .toBe('Automation update (Nightly lint):\nRun lint on main');
  });

  it('applies citation actions by appending text into composer state', () => {
    const composer = { state: { text: 'Existing draft' } };
    const applied = applyComposerCitationAction(composer, { kind: 'task', prompt: 'Review the patch' });
    expect(applied).toBe(true);
    expect(composer.state.text).toBe('Existing draft\n\nReview the patch');
  });
});
