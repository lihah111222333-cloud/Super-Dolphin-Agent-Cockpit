import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { CodePreviewMarkdown, MarkdownImagePreview, MessageContent } from './MarkdownMessage.jsx';

describe('MarkdownMessage', () => {
  it('renders markdown blocks and routes file citation actions', () => {
    const onFileRef = vi.fn();

    render(
      <MessageContent
        text={[
          '# Plan',
          '',
          '- first item',
          '',
          ':codex-file-citation[]{path="src/main.go" line_range_start="9" line_range_end="11"}',
        ].join('\n')}
        actions={{ onFileRef }}
      />
    );

    expect(screen.getByRole('heading', { name: 'Plan' })).toBeInTheDocument();
    expect(screen.getByRole('listitem')).toHaveTextContent('first item');

    fireEvent.click(screen.getByRole('button', { name: /src\/main\.go/ }));

    expect(onFileRef).toHaveBeenCalledWith(expect.objectContaining({
      column: 0,
      line: 9,
      path: 'src/main.go',
    }));
  });

  it('renders code preview markdown without the message wrapper', () => {
    const { container } = render(
      <div className="message-markdown">
        <CodePreviewMarkdown content={'## Preview\n\n`ok`'} />
      </div>
    );

    expect(screen.getByRole('heading', { name: 'Preview' })).toBeInTheDocument();
    expect(container.querySelector('.message-markdown code')).toHaveTextContent('ok');
    expect(container.querySelector('.message-markdown .message-markdown')).toBeNull();
  });

  it('opens and closes local image previews in a lightbox', () => {
    render(<MarkdownImagePreview src="data:image/png;base64,AA==" label="sample.png" />);

    fireEvent.click(screen.getByRole('button', { name: /sample\.png/ }));

    expect(screen.getByRole('dialog', { name: /sample\.png/ })).toBeInTheDocument();

    fireEvent.keyDown(window, { key: 'Escape' });

    expect(screen.queryByRole('dialog', { name: /sample\.png/ })).not.toBeInTheDocument();
  });
});
