// @ts-nocheck
import { describe, expect, it, vi } from 'vitest';

import { AttachmentPreview } from './components/timeline/AttachmentPreview.ts';

describe('AttachmentPreview', () => {
  it('derives image and file lists through attachment api helpers', () => {
    const attachmentApi = {
      imageAttachments: vi.fn((items) => items.filter((item) => item.kind === 'image')),
      fileAttachments: vi.fn((items) => items.filter((item) => item.kind === 'file')),
      attachmentLabel: vi.fn((item) => item.name),
      attachmentPreview: vi.fn((item) => item.previewUrl || ''),
      attachmentType: vi.fn((item) => item.kind),
      onAttachmentHoverMove: vi.fn(),
      onAttachmentHoverLeave: vi.fn(),
      openAttachmentLightbox: vi.fn(),
    };
    const attachments = [
      { kind: 'image', name: 'a.png', previewUrl: 'file:///a.png' },
      { kind: 'file', name: 'notes.txt' },
    ];

    const vm = AttachmentPreview.setup({ attachments, attachmentApi });

    expect(vm.imageList.value).toEqual([attachments[0]]);
    expect(vm.fileList.value).toEqual([attachments[1]]);
    expect(vm.attachmentLabel(attachments[0])).toBe('a.png');
    expect(vm.attachmentPreview(attachments[0])).toBe('file:///a.png');
    expect(vm.attachmentType(attachments[1])).toBe('file');
  });

  it('delegates hover and lightbox actions to the attachment api', () => {
    const attachmentApi = {
      imageAttachments: () => [],
      fileAttachments: () => [],
      attachmentLabel: () => '',
      attachmentPreview: () => '',
      attachmentType: () => '',
      onAttachmentHoverMove: vi.fn(),
      onAttachmentHoverLeave: vi.fn(),
      openAttachmentLightbox: vi.fn(),
    };
    const attachment = { kind: 'image', name: 'a.png' };
    const event = { type: 'mouseenter' };

    const vm = AttachmentPreview.setup({ attachments: [attachment], attachmentApi });
    vm.onHoverMove(event, attachment);
    vm.onHoverLeave();
    vm.openLightbox(attachment);

    expect(attachmentApi.onAttachmentHoverMove).toHaveBeenCalledWith(event, attachment);
    expect(attachmentApi.onAttachmentHoverLeave).toHaveBeenCalled();
    expect(attachmentApi.openAttachmentLightbox).toHaveBeenCalledWith(attachment);
  });
});
