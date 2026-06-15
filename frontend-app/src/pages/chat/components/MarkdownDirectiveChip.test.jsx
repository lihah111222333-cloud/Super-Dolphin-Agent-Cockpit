import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { MarkdownCitationLinkChip, MarkdownDirectiveChip } from './MarkdownDirectiveChip.jsx';
import { citationMarkdownLinkChipModel, directiveChipModel } from './markdownDirectiveModel.js';

describe('MarkdownDirectiveChip', () => {
  it('routes file citation directives to file actions', () => {
    const onFileRef = vi.fn();

    render(
      <MarkdownDirectiveChip
        chip={directiveChipModel(':codex-file-citation[]{path="src/main.go" line_range_start="9" line_range_end="11"}')}
        actions={{ onFileRef }}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /src\/main\.go/ }));

    expect(onFileRef).toHaveBeenCalledWith(expect.objectContaining({
      column: 0,
      line: 9,
      path: 'src/main.go',
    }));
  });

  it('routes code comment directives to citation actions', () => {
    const onCitation = vi.fn();

    render(
      <MarkdownDirectiveChip
        chip={directiveChipModel(':code-comment[Please rename this]{title="Naming" path="src/main.go" line_range_start="9" line_range_end="11"}')}
        actions={{ onCitation }}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /Naming/ }));

    expect(onCitation).toHaveBeenCalledWith(expect.objectContaining({
      kind: 'code-comment',
      lineEnd: 11,
      lineStart: 9,
      message: 'Please rename this',
      path: 'src/main.go',
      title: 'Naming',
    }));
  });

  it('turns agent markdown links into conversation citation actions', () => {
    const onCitation = vi.fn();

    render(
      <MarkdownCitationLinkChip
        chip={citationMarkdownLinkChipModel('[Follow-up](agent://thread-2)')}
        actions={{ onCitation }}
      />
    );

    fireEvent.click(screen.getByRole('button', { name: /Follow-up/ }));

    expect(onCitation).toHaveBeenCalledWith(expect.objectContaining({
      conversationId: 'thread-2',
      kind: 'conversation',
      raw: 'Follow-up',
    }));
  });
});
