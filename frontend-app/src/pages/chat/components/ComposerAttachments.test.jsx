import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { ComposerAttachments } from './ComposerAttachments.jsx';
import { composerAttachmentKey } from './composerAttachmentKey.js';

const fileAttachment = {
  kind: 'file',
  name: 'report.md',
  path: '/tmp/report.md',
};

const imageAttachment = {
  kind: 'image',
  name: 'screen.png',
  path: '/tmp/screen.png',
  previewUrl: 'blob:screen-preview',
};

describe('ComposerAttachments', () => {
  it('renders nothing when there are no attachments', () => {
    const { container } = render(<ComposerAttachments attachments={[]} onPreview={vi.fn()} onRemove={vi.fn()} />);

    expect(container).toBeEmptyDOMElement();
  });

  it('renders file and image attachments and routes actions through props', () => {
    const onPreview = vi.fn();
    const onRemove = vi.fn();

    render(
      <ComposerAttachments
        attachments={[fileAttachment, imageAttachment]}
        onPreview={onPreview}
        onRemove={onRemove}
      />,
    );

    const imagePreview = screen.getByRole('button', { name: '预览附件 screen.png' });
    expect(screen.getByRole('button', { name: '预览附件 report.md' })).toHaveTextContent('report.md');
    expect(imagePreview).toHaveTextContent('screen.png');
    expect(imagePreview.querySelector('img')).toHaveAttribute('src', 'blob:screen-preview');

    fireEvent.click(screen.getByRole('button', { name: '预览附件 report.md' }));
    fireEvent.click(screen.getByRole('button', { name: '移除附件 screen.png' }));

    expect(onPreview).toHaveBeenCalledWith(fileAttachment);
    expect(onRemove).toHaveBeenCalledWith(imageAttachment);
  });

  it('normalizes attachment identity from path, preview URL, or URL', () => {
    expect(composerAttachmentKey({ path: ' /tmp/a.txt ' })).toBe('/tmp/a.txt');
    expect(composerAttachmentKey({ previewUrl: ' blob:a ' })).toBe('blob:a');
    expect(composerAttachmentKey({ url: ' file:///tmp/a.txt ' })).toBe('file:///tmp/a.txt');
  });
});
