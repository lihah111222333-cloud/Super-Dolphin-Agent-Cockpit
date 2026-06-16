import { describe, expect, it, vi } from 'vitest';
import {
  CONVERSATION_DROP_TARGET_ID,
  clipboardPathsFromText,
  extractFilePathsFromTransferData,
  nativeDropFiles,
  nativeDropTargetAcceptsFiles,
} from './useComposerInteractions.js';

vi.mock('../services/chatCodeService.js', () => ({
  onFilesDropped: vi.fn(() => () => {}),
}));

describe('useComposerInteractions file transfer helpers', () => {
  it('normalizes clipboard paths from file URIs and quoted local paths', () => {
    const windowsPath = 'C:\\repo\\notes\\brief.md';
    const paths = clipboardPathsFromText([
      'copy',
      'file:///tmp/report%20one.md',
      `"${windowsPath}"`,
      'file:///tmp/report%20one.md',
      '# ignored comment',
      'not-a-local-path',
    ].join('\n'));

    expect(paths).toEqual(['/tmp/report one.md', windowsPath]);
  });

  it('extracts file paths only from transfer data path types', () => {
    const getData = vi.fn((type) => {
      if (type === 'text/uri-list') return 'file:///tmp/a.txt\nfile:///tmp/b.txt';
      if (type === 'text/plain') return '/tmp/plain.txt';
      return '';
    });
    const paths = extractFilePathsFromTransferData({
      types: ['text/uri-list'],
      getData,
    });

    expect(paths).toEqual(['/tmp/a.txt', '/tmp/b.txt']);
    expect(getData).toHaveBeenCalledTimes(1);
    expect(getData).toHaveBeenCalledWith('text/uri-list');
  });

  it('accepts native file drops only from composer or conversation targets', () => {
    expect(nativeDropTargetAcceptsFiles({ id: CONVERSATION_DROP_TARGET_ID })).toBe(true);
    expect(nativeDropTargetAcceptsFiles({ classList: ['composer-card'] })).toBe(true);
    expect(nativeDropTargetAcceptsFiles({ attributes: { class: 'timeline-shell' } })).toBe(true);
    expect(nativeDropTargetAcceptsFiles({ attributes: { 'data-file-drop-target': '' } })).toBe(true);

    expect(nativeDropTargetAcceptsFiles({ id: 'sidebar-thread-item', classList: ['thread-card'] })).toBe(false);
    expect(nativeDropTargetAcceptsFiles(undefined)).toBe(false);
    expect(nativeDropTargetAcceptsFiles(undefined, { acceptEmptyDetails: true })).toBe(true);
  });

  it('unwraps native drop payloads and rejects clearly unrelated targets', () => {
    expect(nativeDropFiles({
      data: {
        payload: {
          files: ['/tmp/native.txt'],
          details: { attributes: { 'data-file-drop-target': '' } },
        },
      },
    })).toEqual(['/tmp/native.txt']);

    expect(nativeDropFiles({
      files: ['/tmp/sidebar.txt'],
      details: { classList: ['app-nav'] },
    })).toEqual([]);
  });
});
