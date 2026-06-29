import { describe, expect, it } from 'vitest';
import {
  appendUniqueAttachments,
  attachmentDisplayName,
  attachmentKey,
  basename,
  buildTurnInput,
  composerDraftKey,
  composerScopeCwd,
  createImageFileAttachment,
  droppedFilePath,
  fileListOf,
  fileLooksImage,
  isEmptyComposerDraftSnapshot,
  normalizeAttachment,
  normalizeComposerDraftSnapshot,
  normalizeFileAttachment,
} from './composerAttachments.js';

describe('composerAttachments', () => {
  it('normalizes file and image attachments', () => {
    expect(basename('C:/tmp/report.md')).toBe('report.md');
    expect(normalizeFileAttachment(' C:/tmp/photo.png ')).toEqual({
      path: 'C:/tmp/photo.png',
      name: 'photo.png',
      kind: 'image',
      previewUrl: 'file://C:/tmp/photo.png',
    });
    expect(normalizeAttachment({ url: 'C:/tmp/readme.txt' })).toEqual({
      path: 'C:/tmp/readme.txt',
      name: 'readme.txt',
      kind: 'file',
      previewUrl: 'C:/tmp/readme.txt',
    });
    expect(normalizeAttachment('')).toBeNull();
  });

  it('preserves token-backed native image previews while keeping path for send', () => {
    const attachment = normalizeAttachment({
      path: '/Users/mima0000/Pictures/native-secret.png',
      previewUrl: '/local-image?id=drop_asset_123',
    });

    expect(attachment).toEqual({
      path: '/Users/mima0000/Pictures/native-secret.png',
      name: 'native-secret.png',
      kind: 'image',
      previewUrl: '/local-image?id=drop_asset_123',
    });
    expect(attachmentDisplayName(attachment)).toBe('native-secret.png');
  });

  it('clones composer draft snapshots and detects empty drafts', () => {
    const snapshot = normalizeComposerDraftSnapshot({
      draft: 123,
      attachments: [{ path: 'C:/tmp/a.md', name: 'A' }],
    });
    expect(snapshot).toEqual({
      draft: '123',
      attachments: [{
        path: 'C:/tmp/a.md',
        name: 'A',
        kind: 'file',
        previewUrl: '',
      }],
    });
    expect(isEmptyComposerDraftSnapshot(snapshot)).toBe(false);
    expect(isEmptyComposerDraftSnapshot({ draft: '', attachments: [] })).toBe(true);
  });

  it('builds stable composer draft keys from cwd and thread id', () => {
    expect(composerScopeCwd({ activeProject: 'D:/repo///', cwd: 'D:/fallback' })).toBe('D:/repo');
    expect(composerDraftKey({ activeProject: '.', cwd: 'D:/repo', activeThreadId: 'thread-1' })).toBe('D:/repo::thread:thread-1');
    expect(composerDraftKey({ cwd: '' })).toBe('__missing_cwd__::new:chat');
  });

  it('deduplicates attachments by normalized attachment key', () => {
    const current = [normalizeFileAttachment('C:/tmp/a.md')];
    const next = appendUniqueAttachments(current, [
      'C:/tmp/a.md',
      'C:/tmp/b.png',
      { path: '' },
    ]);
    expect(next).toEqual([
      {
        path: 'C:/tmp/a.md',
        name: 'a.md',
        kind: 'file',
        previewUrl: '',
      },
      {
        path: 'C:/tmp/b.png',
        name: 'b.png',
        kind: 'image',
        previewUrl: 'file://C:/tmp/b.png',
      },
    ]);
    expect(attachmentKey(next[1])).toBe('C:/tmp/b.png');
  });

  it('normalizes dropped and pasted files', () => {
    expect(fileListOf([null, { name: 'a.png' }])).toEqual([{ name: 'a.png' }]);
    expect(droppedFilePath({ path: ' C:/tmp/a.txt ' })).toBe('C:/tmp/a.txt');
    expect(fileLooksImage({ type: 'image/png' })).toBe(true);
    expect(fileLooksImage({ name: 'screen.webp' })).toBe(true);
    expect(fileLooksImage({ name: 'readme.md' })).toBe(false);
  });

  it('creates clipboard-backed image attachments', async () => {
    const imageFileAttachment = createImageFileAttachment({
      saveClipboardImage: async (base64) => {
        expect(base64).toBe('aGVsbG8=');
        return 'C:/tmp/clipboard.png';
      },
      nowMillis: () => 123,
    });
    const attachment = await imageFileAttachment(new Blob(['hello'], { type: 'image/png' }), 2, 'paste');
    expect(attachment).toEqual({
      path: 'C:/tmp/clipboard.png',
      name: 'paste-123-2.png',
      kind: 'image',
      previewUrl: 'data:image/png;base64,aGVsbG8=',
    });
  });

  it('builds turn input from text, plain file paths, and local images', () => {
    expect(buildTurnInput(' hello ', [
      { path: 'C:/tmp/readme.md' },
      { path: 'C:/tmp/image.png', kind: 'image', previewUrl: 'data:image/png;base64,abc' },
      { path: '' },
    ])).toEqual([
      { type: 'text', text: 'hello' },
      { type: 'mention', name: 'readme.md', path: 'C:/tmp/readme.md' },
      { type: 'localImage', path: 'C:/tmp/image.png', url: 'data:image/png;base64,abc' },
    ]);
  });
});
