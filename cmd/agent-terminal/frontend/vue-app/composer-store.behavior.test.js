// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const apiMock = vi.hoisted(() => ({
  saveClipboardImage: vi.fn(),
  selectFiles: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  saveClipboardImage: apiMock.saveClipboardImage,
  selectFiles: apiMock.selectFiles,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { useComposerStore } from './stores/composer.js';

beforeEach(() => {
  apiMock.saveClipboardImage.mockReset();
  apiMock.selectFiles.mockReset();
  const store = useComposerStore();
  store.clearComposer();
  store.state.attaching = false;
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('composer store behavior', () => {
  it('attaches paths once and classifies image/file attachments', () => {
    const store = useComposerStore();

    const added = store.attachByPaths(['/tmp/readme.md', '/tmp/readme.md', '/tmp/image.png']);

    expect(added).toBe(2);
    expect(store.state.attachments).toEqual([
      { kind: 'file', name: 'readme.md', path: '/tmp/readme.md', previewUrl: '' },
      { kind: 'image', name: 'image.png', path: '/tmp/image.png', previewUrl: 'file:///tmp/image.png' },
    ]);
    expect(store.canSend.value).toBe(true);
  });

  it('attaches picked files and clears attaching state afterwards', async () => {
    const store = useComposerStore();
    apiMock.selectFiles.mockResolvedValueOnce(['/tmp/a.txt', '/tmp/b.png']);

    await store.attachByPicker();

    expect(apiMock.selectFiles).toHaveBeenCalled();
    expect(store.state.attaching).toBe(false);
    expect(store.state.attachments).toHaveLength(2);
  });

  it('handles pasted images through FileReader + clipboard image save', async () => {
    const store = useComposerStore();
    apiMock.saveClipboardImage.mockResolvedValueOnce('/tmp/pasted.png');
    vi.stubGlobal('FileReader', class {
      readAsDataURL() {
        this.result = 'data:image/png;base64,ZmFrZQ==';
        this.onload();
      }
    });

    const prevented = vi.fn();
    const ok = await store.handlePaste({
      preventDefault: prevented,
      clipboardData: {
        items: [{ type: 'image/png', getAsFile: () => ({}) }],
      },
    });

    expect(ok).toBe(true);
    expect(prevented).toHaveBeenCalled();
    expect(apiMock.saveClipboardImage).toHaveBeenCalledWith('ZmFrZQ==');
    expect(store.state.attachments[0].path).toBe('/tmp/pasted.png');
  });

  it('handles dropped pathful files and returns true when anything is added', async () => {
    const store = useComposerStore();
    const prevented = vi.fn();

    const ok = await store.handleDrop({
      preventDefault: prevented,
      dataTransfer: {
        files: [
          { path: '/tmp/drop.txt', name: 'drop.txt' },
          { path: '/tmp/drop.png', name: 'drop.png' },
        ],
      },
    });

    expect(ok).toBe(true);
    expect(prevented).toHaveBeenCalled();
    expect(store.state.attachments).toHaveLength(2);
  });

  it('removes attachments and clears composer text', () => {
    const store = useComposerStore();
    store.state.text = 'hello';
    store.attachByPaths(['/tmp/one.txt', '/tmp/two.txt']);

    store.removeAttachment(0);
    expect(store.state.attachments.map((item) => item.name)).toEqual(['two.txt']);

    store.clearComposer();
    expect(store.state.text).toBe('');
    expect(store.state.attachments).toEqual([]);
    expect(store.canSend.value).toBe(false);
  });
});
