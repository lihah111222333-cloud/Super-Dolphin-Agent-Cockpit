import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AttachmentPreviewModal } from './AttachmentPreviewModal.jsx';
import { ComposerAttachments } from './ComposerAttachments.jsx';
import { composerAttachmentKey } from './composerAttachmentKey.js';

const screenshotName = '\u5c4f\u5e55\u622a\u56fe 2026-06-13 170324.png';

function windowsScreenshotPath() {
  return ['C:', 'Users', 'mima0000', 'Pictures', 'Screenshots', screenshotName].join('\\');
}

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

  it('renders image attachments from clipboard temp paths when previewUrl is missing', () => {
    render(
      <ComposerAttachments
        attachments={[{
          kind: 'image',
          name: 'clipboard-333.png',
          path: 'C:/Users/ai/AppData/Local/Temp/clipboard-333.png',
        }]}
        onPreview={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    const imagePreview = screen.getByLabelText(/\u9884\u89c8\u9644\u4ef6 clipboard-333\.png/);
    expect(imagePreview.querySelector('img')).toHaveAttribute('src', '/clipboard/clipboard-333.png');
  });

  it('renders Codex clipboard image attachments without a broken file URL', () => {
    render(
      <ComposerAttachments
        attachments={[{
          kind: 'image',
          name: 'screen.png',
          path: 'C:/Users/ai/AppData/Local/Temp/codex-clipboard-f05.png',
        }]}
        onPreview={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    const imagePreview = screen.getByLabelText(/\u9884\u89c8\u9644\u4ef6 screen\.png/);
    expect(imagePreview.querySelector('img')).toHaveAttribute('src', '/clipboard/codex-clipboard-f05.png');
  });

  it('renders normal local screenshot attachments as file pills', () => {
    const screenshotPath = windowsScreenshotPath();

    render(
      <ComposerAttachments
        attachments={[{
          kind: 'image',
          name: screenshotName,
          path: screenshotPath,
          previewUrl: `file://${screenshotPath}`,
        }]}
        onPreview={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    const imagePreview = screen.getByLabelText(/\u9884\u89c8\u9644\u4ef6 \u5c4f\u5e55\u622a\u56fe 2026-06-13 170324\.png/);
    expect(imagePreview.querySelector('img')).toBeNull();
    expect(imagePreview).toHaveTextContent(screenshotName);
  });

  it('normalizes attachment identity from path, preview URL, or URL', () => {
    expect(composerAttachmentKey({ path: ' /tmp/a.txt ' })).toBe('/tmp/a.txt');
    expect(composerAttachmentKey({ previewUrl: ' blob:a ' })).toBe('blob:a');
    expect(composerAttachmentKey({ url: ' file:///tmp/a.txt ' })).toBe('file:///tmp/a.txt');
  });

  it('does not render raw native image paths in composer or preview modal', () => {
    const rawPath = '/Users/mima0000/Pictures/native-secret.png';
    const attachment = {
      kind: 'image',
      path: rawPath,
      previewUrl: '/local-image?id=drop_asset_456',
    };

    const { rerender } = render(
      <ComposerAttachments
        attachments={[attachment]}
        onPreview={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    const pill = screen.getByRole('button', { name: '预览附件 native-secret.png' });
    expect(pill).toHaveTextContent('native-secret.png');
    expect(pill.querySelector('img')).toHaveAttribute('src', '/local-image?id=drop_asset_456');
    expect(screen.queryByText(rawPath)).not.toBeInTheDocument();

    rerender(
      <AttachmentPreviewModal
        attachment={attachment}
        onClose={vi.fn()}
        onRemove={vi.fn()}
      />,
    );

    expect(screen.getByRole('dialog', { name: '附件预览' })).toHaveTextContent('native-secret.png');
    expect(screen.queryByText(rawPath)).not.toBeInTheDocument();
    expect(screen.getByAltText('native-secret.png')).toHaveAttribute('src', '/local-image?id=drop_asset_456');
  });
});
