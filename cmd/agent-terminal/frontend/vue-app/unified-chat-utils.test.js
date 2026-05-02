/**
 * Phase 1-1: UnifiedChatPage 纯函数回归测试
 *
 * 覆盖 UnifiedChatPage.js 第 137~487 行的所有纯函数。
 * 拆分后 import 路径改为新文件, 断言不变 → 证明行为一致。
 */
import { describe, it, expect } from 'vitest';
import {
    normalizeDiffPath,
    whitespaceTrace,
    basename,
    pickDiffFile,
    serializeDiffFile,
    buildFocusedDiffSelection,
} from './utils/diff-utils.js';
import {
    buildSyntheticDiffFromCodeOpen,
    codeOpenSnippetLines,
    isTextPreviewPath,
    isMarkdownPath,
    buildTextPreviewFromCodeOpen,
    buildMarkdownPreviewFromCodeOpen,
    isPreviewableImagePath,
    toFilePreviewURL,
    buildImagePreviewFromCodeOpen,
} from './utils/preview-utils.js';
import {
    requestHistoryLoad,
    ensureThreadSelectionFresh,
    shouldForceThreadSelectionScroll,
    buildVisibleChatThreadCards,
} from './utils/thread-page-utils.js';





// ─── normalizeDiffPath ──────────────────────────────────────────────
describe('normalizeDiffPath', () => {
    it('returns empty string for null/undefined/empty', () => {
        expect(normalizeDiffPath(null)).toBe('');
        expect(normalizeDiffPath(undefined)).toBe('');
        expect(normalizeDiffPath('')).toBe('');
    });

    it('converts backslashes to forward slashes', () => {
        expect(normalizeDiffPath('src\\utils\\file.js')).toBe('src/utils/file.js');
    });

    it('strips leading ./ prefix', () => {
        expect(normalizeDiffPath('./src/file.js')).toBe('src/file.js');
    });

    it('strips a/ and b/ prefixes (git diff format)', () => {
        expect(normalizeDiffPath('a/src/file.js')).toBe('src/file.js');
        expect(normalizeDiffPath('b/src/file.js')).toBe('src/file.js');
    });

    it('lowercases the result', () => {
        expect(normalizeDiffPath('Src/MyFile.JS')).toBe('src/myfile.js');
    });

    it('trims whitespace', () => {
        expect(normalizeDiffPath('  src/file.js  ')).toBe('src/file.js');
    });
});

// ─── whitespaceTrace ────────────────────────────────────────────────
describe('whitespaceTrace', () => {
    it('handles empty/null input', () => {
        const r = whitespaceTrace('');
        expect(r.value).toBe('');
        expect(r.compact).toBe('');
        expect(r.hasMultiSpace).toBe(false);
        expect(r.charLen).toBe(0);
    });

    it('detects multi-space', () => {
        const r = whitespaceTrace('hello  world');
        expect(r.hasMultiSpace).toBe(true);
        expect(r.compact).toBe('hello world');
        expect(r.charLen).toBe(12);
    });

    it('preserves single spaces', () => {
        const r = whitespaceTrace('hello world');
        expect(r.hasMultiSpace).toBe(false);
    });
});

// ─── basename ───────────────────────────────────────────────────────
describe('basename', () => {
    it('returns empty for null/empty', () => {
        expect(basename('')).toBe('');
        expect(basename(null)).toBe('');
    });

    it('extracts filename from path', () => {
        expect(basename('src/utils/file.js')).toBe('file.js');
    });

    it('handles single filename', () => {
        expect(basename('file.js')).toBe('file.js');
    });

    it('normalizes path first (lowercase, slashes)', () => {
        expect(basename('Src\\Utils\\File.JS')).toBe('file.js');
    });
});

// ─── pickDiffFile ───────────────────────────────────────────────────
describe('pickDiffFile', () => {
    const files = [
        { filename: 'src/utils/file.js', lines: [] },
        { filename: 'src/components/App.js', lines: [] },
        { filename: 'README.md', lines: [] },
    ];

    it('returns null for empty files or target', () => {
        expect(pickDiffFile([], 'file.js')).toBeNull();
        expect(pickDiffFile(files, '')).toBeNull();
        expect(pickDiffFile(null, 'file.js')).toBeNull();
    });

    it('finds exact match', () => {
        const result = pickDiffFile(files, 'src/utils/file.js');
        expect(result?.filename).toBe('src/utils/file.js');
    });

    it('finds by suffix match (target is shorter)', () => {
        const result = pickDiffFile(files, 'utils/file.js');
        expect(result?.filename).toBe('src/utils/file.js');
    });

    it('finds by basename fallback when the basename is unique', () => {
        const result = pickDiffFile(files, 'other/path/App.js');
        expect(result?.filename).toBe('src/components/App.js');
    });

    it('returns null for ambiguous basename-only matches', () => {
        const result = pickDiffFile([
            { filename: 'src/a/index.ts', lines: [] },
            { filename: 'src/b/index.ts', lines: [] },
        ], 'worker/index.ts');
        expect(result).toBeNull();
    });

    it('returns null for no match', () => {
        expect(pickDiffFile(files, 'nonexistent.ts')).toBeNull();
    });
});

// ─── serializeDiffFile ──────────────────────────────────────────────
describe('serializeDiffFile', () => {
    it('returns empty for null/undefined', () => {
        expect(serializeDiffFile(null)).toBe('');
        expect(serializeDiffFile(undefined)).toBe('');
    });

    it('returns empty for file without filename', () => {
        expect(serializeDiffFile({ filename: '', lines: [] })).toBe('');
    });

    it('serializes add/del/ctx/hunk/meta lines correctly', () => {
        const file = {
            filename: 'src/file.js',
            lines: [
                { type: 'hunk', text: '@@ -1,3 +1,4 @@' },
                { type: 'ctx', text: 'const a = 1;' },
                { type: 'del', text: 'const b = 2;' },
                { type: 'add', text: 'const b = 3;' },
                { type: 'add', text: 'const c = 4;' },
                { type: 'meta', text: '\\ No newline at end of file' },
            ],
        };
        const result = serializeDiffFile(file);
        expect(result).toContain('diff --git a/src/file.js b/src/file.js');
        expect(result).toContain('--- a/src/file.js');
        expect(result).toContain('+++ b/src/file.js');
        expect(result).toContain('@@ -1,3 +1,4 @@');
        expect(result).toContain(' const a = 1;');
        expect(result).toContain('-const b = 2;');
        expect(result).toContain('+const b = 3;');
        expect(result).toContain('+const c = 4;');
        expect(result).toContain('\\ No newline at end of file');
    });
});

// ─── buildFocusedDiffSelection ──────────────────────────────────────
describe('buildFocusedDiffSelection', () => {
    const rawDiff = [
        'diff --git a/src/a.js b/src/a.js',
        '--- a/src/a.js',
        '+++ b/src/a.js',
        '@@ -1,2 +1,3 @@',
        ' line1',
        '+added',
        ' line2',
        'diff --git a/src/b.js b/src/b.js',
        '--- a/src/b.js',
        '+++ b/src/b.js',
        '@@ -1,1 +1,1 @@',
        '-old',
        '+new',
    ].join('\n');

    it('returns null for empty diff', () => {
        expect(buildFocusedDiffSelection('', 'src/a.js')).toBeNull();
    });

    it('returns focused diff for matching file', () => {
        const result = buildFocusedDiffSelection(rawDiff, 'src/a.js');
        expect(result).not.toBeNull();
        expect(result.filename).toBe('src/a.js');
        expect(result.diffText).toContain('+added');
        expect(result.diffText).not.toContain('-old');
    });

    it('returns null when target file not found', () => {
        expect(buildFocusedDiffSelection(rawDiff, 'nonexistent.js')).toBeNull();
    });
});

// ─── codeOpenSnippetLines ───────────────────────────────────────────
describe('codeOpenSnippetLines', () => {
    it('handles array snippet (structured)', () => {
        const result = codeOpenSnippetLines({
            snippet: [{ text: 'line1' }, { text: 'line2' }],
        });
        expect(result).toEqual(['line1', 'line2']);
    });

    it('handles string snippet', () => {
        const result = codeOpenSnippetLines({ snippet: 'line1\nline2' });
        expect(result).toEqual(['line1', 'line2']);
    });

    it('handles null/undefined', () => {
        const result = codeOpenSnippetLines(null);
        expect(result).toEqual(['']);
    });
});

// ─── buildSyntheticDiffFromCodeOpen ─────────────────────────────────
describe('buildSyntheticDiffFromCodeOpen', () => {
    it('returns empty for null', () => {
        expect(buildSyntheticDiffFromCodeOpen(null)).toBe('');
    });

    it('returns empty for result without path', () => {
        expect(buildSyntheticDiffFromCodeOpen({ snippet: 'x' })).toBe('');
    });

    it('builds synthetic diff from array snippet', () => {
        const result = buildSyntheticDiffFromCodeOpen({
            relative: 'src/file.js',
            startLine: 10,
            snippet: [{ line: 10, text: 'const a = 1;' }, { line: 11, text: 'const b = 2;' }],
        });
        expect(result).toContain('diff --git a/src/file.js b/src/file.js');
        expect(result).toContain('@@ -10,2 +10,2 @@');
        expect(result).toContain(' const a = 1;');
        expect(result).toContain(' const b = 2;');
    });
});

// ─── isMarkdownPath ─────────────────────────────────────────────────
describe('isMarkdownPath', () => {
    it('returns true for .md/.markdown files', () => {
        expect(isMarkdownPath('README.md')).toBe(true);
        expect(isMarkdownPath('docs/guide.MD')).toBe(true);
        expect(isMarkdownPath('docs/spec.markdown')).toBe(true);
    });

    it('returns false for non-md files', () => {
        expect(isMarkdownPath('file.txt')).toBe(false);
        expect(isMarkdownPath('file.js')).toBe(false);
    });

    it('returns false for empty/null', () => {
        expect(isMarkdownPath('')).toBe(false);
        expect(isMarkdownPath(null)).toBe(false);
    });
});

// ─── isTextPreviewPath ──────────────────────────────────────────────
describe('isTextPreviewPath', () => {
    it('returns true for supported text preview extensions', () => {
        expect(isTextPreviewPath('README.md')).toBe(true);
        expect(isTextPreviewPath('notes.txt')).toBe(true);
        expect(isTextPreviewPath('config.json')).toBe(true);
        expect(isTextPreviewPath('config.yaml')).toBe(true);
        expect(isTextPreviewPath('config.YML')).toBe(true);
    });

    it('returns false for code files', () => {
        expect(isTextPreviewPath('main.go')).toBe(false);
        expect(isTextPreviewPath('app.js')).toBe(false);
    });
});

// ─── buildMarkdownPreviewFromCodeOpen ───────────────────────────────
describe('buildMarkdownPreviewFromCodeOpen', () => {
    it('returns null for non-ok result', () => {
        expect(buildMarkdownPreviewFromCodeOpen({ ok: false })).toBeNull();
    });

    it('returns null for non-markdown file', () => {
        expect(buildMarkdownPreviewFromCodeOpen({
            ok: true,
            relative: 'file.js',
            language: 'javascript',
            snippet: 'code',
        })).toBeNull();
    });

    it('returns preview for markdown file', () => {
        const result = buildMarkdownPreviewFromCodeOpen({
            ok: true,
            relative: 'README.md',
            language: 'markdown',
            startLine: 1,
            endLine: 3,
            totalLines: 50,
            snippet: [{ text: '# Title' }, { text: '' }, { text: 'Content' }],
        });
        expect(result).not.toBeNull();
        expect(result.previewKind).toBe('markdown');
        expect(result.path).toBe('README.md');
        expect(result.filePath).toBe('');
        expect(result.text).toBe('# Title\n\nContent');
        expect(result.language).toBe('markdown');
        expect(result.startLine).toBe(1);
        expect(result.endLine).toBe(3);
        expect(result.totalLines).toBe(50);
        expect(result.editable).toBe(false);
    });

    it('returns preview for .markdown files even when language metadata is empty', () => {
        const result = buildMarkdownPreviewFromCodeOpen({
            ok: true,
            relative: 'docs/spec.markdown',
            language: '',
            startLine: 2,
            endLine: 3,
            totalLines: 30,
            snippet: [{ text: '## Spec' }, { text: 'content' }],
        });
        expect(result).not.toBeNull();
        expect(result.previewKind).toBe('markdown');
        expect(result.path).toBe('docs/spec.markdown');
        expect(result.text).toBe('## Spec\ncontent');
        expect(result.language).toBe('markdown');
        expect(result.startLine).toBe(2);
        expect(result.endLine).toBe(3);
        expect(result.totalLines).toBe(30);
    });

    it('repairs desktop bridge mojibake for markdown preview', () => {
        const expected = '# 标题\n\n桌面端中文预览正常。';
        const mojibake = new TextDecoder('latin1').decode(new TextEncoder().encode(expected));
        const result = buildMarkdownPreviewFromCodeOpen({
            ok: true,
            relative: 'docs/guide.md',
            language: 'markdown',
            startLine: 1,
            endLine: 3,
            totalLines: 3,
            snippet: mojibake.split('\n').map((text, index) => ({ line: index + 1, text })),
        });
        expect(result).not.toBeNull();
        if (!result) throw new Error('expected markdown preview result');
        expect(result.text).toBe(expected);
    });
});

// ─── buildTextPreviewFromCodeOpen ───────────────────────────────────
describe('buildTextPreviewFromCodeOpen', () => {
    it('builds a text preview state for .txt files', () => {
        const result = buildTextPreviewFromCodeOpen({
            ok: true,
            previewKind: 'text',
            relative: 'docs/notes.txt',
            filePath: '/repo/docs/notes.txt',
            language: 'plaintext',
            startLine: 1,
            endLine: 2,
            totalLines: 2,
            snippet: 'hello\nworld',
        });
        expect(result).toEqual({
            previewKind: 'text',
            path: 'docs/notes.txt',
            filePath: '/repo/docs/notes.txt',
            text: 'hello\nworld',
            language: 'plaintext',
            startLine: 1,
            endLine: 2,
            totalLines: 2,
            editable: true,
        });
    });

    it('builds a highlighted text preview state for json/yaml files', () => {
        const jsonResult = buildTextPreviewFromCodeOpen({
            ok: true,
            previewKind: 'text',
            relative: 'config/app.json',
            filePath: '/repo/config/app.json',
            language: 'json',
            totalLines: 1,
            snippet: '{"enabled":true}',
        });
        const yamlResult = buildTextPreviewFromCodeOpen({
            ok: true,
            relative: 'config/app.yaml',
            filePath: '/repo/config/app.yaml',
            language: '',
            totalLines: 2,
            snippet: 'enabled: true\nname: demo',
        });
        const ymlResult = buildTextPreviewFromCodeOpen({
            ok: true,
            relative: 'config/app.yml',
            filePath: '/repo/config/app.yml',
            totalLines: 1,
            snippet: 'name: demo',
        });

        expect(jsonResult?.previewKind).toBe('text');
        expect(jsonResult?.language).toBe('json');
        expect(jsonResult?.editable).toBe(true);
        expect(yamlResult?.previewKind).toBe('text');
        expect(yamlResult?.language).toBe('yaml');
        expect(yamlResult?.path).toBe('config/app.yaml');
        expect(ymlResult?.previewKind).toBe('text');
        expect(ymlResult?.language).toBe('yaml');
        expect(ymlResult?.path).toBe('config/app.yml');
    });
});



// ─── isPreviewableImagePath ─────────────────────────────────────────
describe('isPreviewableImagePath', () => {
    it('returns true for png/jpg/jpeg/svg', () => {
        expect(isPreviewableImagePath('image.png')).toBe(true);
        expect(isPreviewableImagePath('photo.jpg')).toBe(true);
        expect(isPreviewableImagePath('photo.jpeg')).toBe(true);
        expect(isPreviewableImagePath('icon.svg')).toBe(true);
    });

    it('returns false for other extensions', () => {
        expect(isPreviewableImagePath('doc.pdf')).toBe(false);
        expect(isPreviewableImagePath('file.gif')).toBe(false);
    });

    it('returns false for empty', () => {
        expect(isPreviewableImagePath('')).toBe(false);
    });
});

// ─── toFilePreviewURL ───────────────────────────────────────────────
describe('toFilePreviewURL', () => {
    it('returns empty for empty input', () => {
        expect(toFilePreviewURL('')).toBe('');
    });

    it('passes through existing file:// URLs', () => {
        expect(toFilePreviewURL('file:///path/to/file.png')).toBe('file:///path/to/file.png');
    });

    it('passes through http/https URLs', () => {
        expect(toFilePreviewURL('https://example.com/img.png')).toBe('https://example.com/img.png');
    });

    it('passes through data: URLs', () => {
        expect(toFilePreviewURL('data:image/png;base64,abc')).toBe('data:image/png;base64,abc');
    });

    it('converts Unix absolute path to file:// URL', () => {
        expect(toFilePreviewURL('/home/user/image.png')).toBe('file:///home/user/image.png');
    });

    it('converts Windows path to file:/// URL', () => {
        expect(toFilePreviewURL('C:\\Users\\file.png')).toBe('file:///C:/Users/file.png');
    });
});

// ─── buildImagePreviewFromCodeOpen ──────────────────────────────────
describe('buildImagePreviewFromCodeOpen', () => {
    it('returns null for non-ok result', () => {
        expect(buildImagePreviewFromCodeOpen({ ok: false })).toBeNull();
    });

    it('returns null for non-image result', () => {
        expect(buildImagePreviewFromCodeOpen({
            ok: true,
            relative: 'file.js',
            mediaType: 'text/plain',
        })).toBeNull();
    });

    it('returns preview for image by mediaType', () => {
        const result = buildImagePreviewFromCodeOpen({
            ok: true,
            relative: 'photo.png',
            mediaType: 'image/png',
            thumbnailURL: 'thumb.png',
            previewURL: 'full.png',
            sizeBytes: 1024,
        });
        expect(result).not.toBeNull();
        expect(result.src).toBe('thumb.png');
        expect(result.fullSrc).toBe('full.png');
        expect(result.path).toBe('photo.png');
        expect(result.mediaType).toBe('image/png');
        expect(result.sizeBytes).toBe(1024);
    });

    it('returns preview for image by path extension', () => {
        const result = buildImagePreviewFromCodeOpen({
            ok: true,
            relative: 'icon.svg',
            filePath: '/path/icon.svg',
        });
        expect(result).not.toBeNull();
        expect(result.path).toBe('icon.svg');
    });
});

// Cross-thread diff lookup was removed intentionally.
// File ref recovery is now scoped to the currently selected thread / agent ID.

// ─── requestHistoryLoad ─────────────────────────────────────────────
describe('requestHistoryLoad', () => {
    it('returns false when threadId is empty', async () => {
        const result = await requestHistoryLoad({}, '');
        expect(result).toBe(false);
    });

    it('returns false when loadMessages is not a function', async () => {
        const result = await requestHistoryLoad({}, 'thread-1');
        expect(result).toBe(false);
    });

    it('returns false when timeline already has dialog history', async () => {
        const store = {
            loadMessages: async () => { },
            getThreadTimeline: () => [{ id: 1, kind: 'assistant', text: 'cached' }],
        };
        const result = await requestHistoryLoad(store, 'thread-1');
        expect(result).toBe(false);
    });

    it('loads messages when dialog history exists but cache metadata is stale', async () => {
        let called = false;
        const store = {
            loadMessages: async () => { called = true; },
            getThreadTimeline: () => [{ id: 1, kind: 'assistant', text: 'runtime only' }],
            shouldReloadThreadHistory: () => true,
        };
        const result = await requestHistoryLoad(store, 'thread-1');
        expect(result).toBe(true);
        expect(called).toBe(true);
    });

    it('forces messages reload even when dialog history is already cached', async () => {
        let called = false;
        const store = {
            loadMessages: async () => { called = true; },
            getThreadTimeline: () => [{ id: 1, kind: 'assistant', text: 'cached' }],
        };
        const result = await requestHistoryLoad(store, 'thread-1', { force: true });
        expect(result).toBe(true);
        expect(called).toBe(true);
    });

    it('loads messages when timeline only has transient process items', async () => {
        let called = false;
        const store = {
            loadMessages: async () => { called = true; },
            getThreadTimeline: () => [{ id: 1, kind: 'thinking', text: 'processing' }],
        };
        const result = await requestHistoryLoad(store, 'thread-1');
        expect(result).toBe(true);
        expect(called).toBe(true);
    });

    it('loads messages and returns true', async () => {
        let called = false;
        const store = {
            loadMessages: async () => { called = true; },
            getThreadTimeline: () => [],
        };
        const result = await requestHistoryLoad(store, 'thread-1');
        expect(result).toBe(true);
        expect(called).toBe(true);
    });
});

describe('ensureThreadSelectionFresh', () => {
    it('does not re-sync thread state when history load already fetched messages', async () => {
        let loaded = false;
        let synced = false;
        const store = {
            loadMessages: async () => { loaded = true; },
            getThreadTimeline: () => [],
            syncThreadState: async () => { synced = true; },
        };

        const result = await ensureThreadSelectionFresh(store, 'thread-1');

        expect(result).toEqual({
            requestedHistory: true,
            syncedThreadState: false,
            forcedHistoryReload: false,
        });
        expect(loaded).toBe(true);
        expect(synced).toBe(false);
    });

    it('forces provider history reload when thread cache is considered stale', async () => {
        let loadedThreadId = '';
        let synced = false;
        const store = {
            loadMessages: async (threadId) => { loadedThreadId = threadId; },
            getThreadTimeline: () => [{ id: 1, kind: 'assistant', text: 'cached' }],
            shouldReloadThreadHistory: () => true,
            syncThreadState: async () => { synced = true; },
        };

        const result = await ensureThreadSelectionFresh(store, 'thread-1');

        expect(result).toEqual({
            requestedHistory: true,
            syncedThreadState: false,
            forcedHistoryReload: true,
        });
        expect(loadedThreadId).toBe('thread-1');
        expect(synced).toBe(false);
    });

    it('syncs the visible selection before forcing an atomic history refresh', async () => {
        let loadedThreadId = '';
        let syncedThreadId = '';
        let timeline = /** @type {any[]} */ ([]);
        const store = {
            loadMessages: async (threadId) => { loadedThreadId = threadId; },
            getThreadTimeline: () => timeline,
            shouldReloadThreadHistory: () => false,
            syncThreadState: async (threadId) => {
                syncedThreadId = threadId;
                timeline = [{ id: 1, kind: 'assistant', text: 'latest' }];
            },
        };

        const result = await ensureThreadSelectionFresh(store, 'thread-1', { reason: 'selection', previousThreadId: 'thread-0' });

        expect(result).toEqual({ requestedHistory: true, syncedThreadState: true, forcedHistoryReload: false });
        expect(syncedThreadId).toBe('thread-1');
        expect(loadedThreadId).toBe('thread-1');
    });


    it('falls back to provider history reload when visible selection switch has no scoped sync', async () => {
        let loadedThreadId = '';
        const store = {
            loadMessages: async (threadId) => { loadedThreadId = threadId; },
            getThreadTimeline: () => [{ id: 1, kind: 'assistant', text: 'cached' }],
            shouldReloadThreadHistory: () => false,
        };

        const result = await ensureThreadSelectionFresh(store, 'thread-1', { reason: 'selection', previousThreadId: 'thread-0' });

        expect(result).toEqual({ requestedHistory: true, syncedThreadState: false, forcedHistoryReload: false });
        expect(loadedThreadId).toBe('thread-1');
    });


    it('re-syncs selected thread state when dialog history is already cached and reload is not needed', async () => {
        let syncedThreadId = '';
        const store = {
            loadMessages: async () => { },
            getThreadTimeline: () => [{ id: 1, kind: 'assistant', text: 'cached' }],
            shouldReloadThreadHistory: () => false,
            syncThreadState: async (threadId) => { syncedThreadId = threadId; },
        };

        const result = await ensureThreadSelectionFresh(store, 'thread-1');

        expect(result).toEqual({
            requestedHistory: false,
            syncedThreadState: true,
            forcedHistoryReload: false,
        });
        expect(syncedThreadId).toBe('thread-1');
    });

    it('skips lightweight thread sync when store does not provide one', async () => {
        const store = {
            loadMessages: async () => { },
            getThreadTimeline: () => [{ id: 1, kind: 'assistant', text: 'cached' }],
            shouldReloadThreadHistory: () => false,
        };

        const result = await ensureThreadSelectionFresh(store, 'thread-1');

        expect(result).toEqual({
            requestedHistory: false,
            syncedThreadState: false,
            forcedHistoryReload: false,
        });
    });
});
describe('shouldForceThreadSelectionScroll', () => {
    it('forces bottom scroll only when history hydration changed the timeline', () => {
        expect(shouldForceThreadSelectionScroll({ requestedHistory: true, syncedThreadState: false, forcedHistoryReload: false })).toBe(true);
        expect(shouldForceThreadSelectionScroll({ requestedHistory: false, syncedThreadState: false, forcedHistoryReload: true })).toBe(true);
    });

    it('keeps focus anchored for lightweight syncs or pending diff focus jumps', () => {
        expect(shouldForceThreadSelectionScroll({ requestedHistory: false, syncedThreadState: false, forcedHistoryReload: false })).toBe(false);
        expect(shouldForceThreadSelectionScroll({ requestedHistory: true, syncedThreadState: false, forcedHistoryReload: true }, true)).toBe(false);
    });
});



// ─── buildVisibleChatThreadCards ────────────────────────────────────
describe('buildVisibleChatThreadCards', () => {
    it('builds cards only for active threads when archived rail is hidden', () => {
        let displayCalls = 0;
        let statusCalls = 0;
        let statusHeaderCalls = 0;
        let interruptCalls = 0;
        const result = buildVisibleChatThreadCards({
            threads: [
                { id: 'thread-active', name: 'Active' },
                { id: 'thread-archived', name: 'Archived' },
            ],
            selectedThreadId: 'thread-active',

            pinnedMap: { 'thread-active': 11 },
            archivedMap: { 'thread-archived': 99 },
            runtimeById: {
                'thread-active': { provider: 'codex' },
                'thread-archived': { provider: 'claude' },
            },
            showArchived: false,
            displayNameOf(thread) {
                displayCalls += 1;
                return thread.name;
            },
            statusOf(threadId) {
                statusCalls += 1;
                return `status:${threadId}`;
            },
            statusHeaderOf(threadId) {
                statusHeaderCalls += 1;
                return `header:${threadId}`;
            },
            interruptibleOf() {
                interruptCalls += 1;
                return true;
            },
        });

        expect(result.activeCount).toBe(1);
        expect(result.archivedCount).toBe(1);
        expect(result.cards.map((item) => item.id)).toEqual(['thread-active']);
        expect(displayCalls).toBe(1);
        expect(statusCalls).toBe(1);
        expect(statusHeaderCalls).toBe(1);
        expect(interruptCalls).toBe(1);
    });

    it('builds cards only for archived threads when archived rail is shown', () => {
        let displayCalls = 0;
        let statusCalls = 0;
        let statusHeaderCalls = 0;
        let interruptCalls = 0;
        const result = buildVisibleChatThreadCards({
            threads: [
                { id: 'thread-active', name: 'Active' },
                { id: 'thread-archived', name: 'Archived' },
            ],
            selectedThreadId: 'thread-archived',

            archivedMap: { 'thread-archived': 99 },
            runtimeById: {
                'thread-active': { provider: 'codex' },
                'thread-archived': { provider: 'claude' },
            },
            showArchived: true,
            displayNameOf(thread) {
                displayCalls += 1;
                return thread.name;
            },
            statusOf(threadId) {
                statusCalls += 1;
                return `status:${threadId}`;
            },
            statusHeaderOf(threadId) {
                statusHeaderCalls += 1;
                return `header:${threadId}`;
            },
            interruptibleOf() {
                interruptCalls += 1;
                return true;
            },
        });

        expect(result.activeCount).toBe(1);
        expect(result.archivedCount).toBe(1);
        expect(result.cards.map((item) => item.id)).toEqual(['thread-archived']);
        expect(displayCalls).toBe(1);
        expect(statusCalls).toBe(0);
        expect(statusHeaderCalls).toBe(0);
        expect(interruptCalls).toBe(0);
        expect(result.cards[0].status).toBe('idle');
        expect(result.cards[0].statusHeader).toBe('已归档');
    });

    it('treats thread.state==archived as archived without preference timestamp', () => {
        const result = buildVisibleChatThreadCards({
            threads: [{ id: 'thread-archived', name: 'Archived', state: 'archived' }],
            archivedMap: {},
            showArchived: true,
        });

        expect(result.activeCount).toBe(0);
        expect(result.archivedCount).toBe(1);
        expect(result.cards.map((item) => item.id)).toEqual(['thread-archived']);
    });


    it('treats thread.lifecycleStatus==archived as archived after runtime state overlay', () => {
        const result = buildVisibleChatThreadCards({
            threads: [{ id: 'thread-archived', name: 'Archived', lifecycleStatus: 'archived', state: 'idle', threadStatus: 'idle' }],
            archivedMap: {},
            showArchived: false,
        });

        expect(result.activeCount).toBe(0);
        expect(result.archivedCount).toBe(1);
        expect(result.cards).toEqual([]);
    });

    it('hides thread.state==archived from active list', () => {
        const result = buildVisibleChatThreadCards({
            threads: [{ id: 'thread-archived', name: 'Archived', state: 'archived' }],
            archivedMap: {},
            showArchived: false,
        });

        expect(result.activeCount).toBe(0);
        expect(result.archivedCount).toBe(1);
        expect(result.cards).toEqual([]);
    });

    it('filters deleted lifecycle threads out of both active and archived lists', () => {
        const activeResult = buildVisibleChatThreadCards({
            threads: [{ id: 'thread-deleted', name: 'thread-deleted', lifecycleStatus: 'deleted', state: 'deleted' }],
            archivedMap: {},
            showArchived: false,
        });
        const archivedResult = buildVisibleChatThreadCards({
            threads: [{ id: 'thread-deleted', name: 'thread-deleted', lifecycleStatus: 'deleted', state: 'deleted' }],
            archivedMap: { 'thread-deleted': Date.now() },
            showArchived: true,
        });

        expect(activeResult.activeCount).toBe(0);
        expect(activeResult.archivedCount).toBe(0);
        expect(activeResult.cards).toEqual([]);
        expect(archivedResult.activeCount).toBe(0);
        expect(archivedResult.archivedCount).toBe(0);
        expect(archivedResult.cards).toEqual([]);
    });


    it('records card build perf phases and avoids repeated routing lookups', () => {

        const marks = [];
        let routingCalls = 0;
        const result = buildVisibleChatThreadCards({
            threads: [
                { id: 'thread-1', name: 'One' },
                { id: 'thread-2', name: 'Two' },
            ],
            runtimeById: { 'thread-1': { provider: 'codex' } },
            routingOf(threadId) {
                routingCalls += 1;
                return { agentKey: `agent:${threadId}`, agentTitle: 'Agent', promptKey: 'prompt/default' };
            },
            pendingLaunchOf: (threadId) => threadId === 'thread-2',
            perf: {
                mark(stage, durationMs, fields) {
                    marks.push({ stage, durationMs, fields });
                },
            },
        });

        expect(result.cards).toHaveLength(2);
        expect(routingCalls).toBe(2);
        expect(result.cards[0]).toEqual(expect.objectContaining({
            agentKey: 'agent:thread-1',
            agentTitle: 'Agent',
            promptKey: 'prompt/default',
        }));
        expect(result.cards[1]).toEqual(expect.objectContaining({ pendingLaunch: true }));
        expect(marks.map((item) => item.stage)).toEqual([
            'normalize_inputs',
            'partition_threads',
            'build_cards',
            'total_build_visible_cards',
        ]);
        expect(marks.find((item) => item.stage === 'build_cards').fields).toEqual(expect.objectContaining({
            card_count: 2,
            routing_calls: 2,
            pending_launch_calls: 2,
            runtime_hits: 1,
        }));
    });

    it('marks archived threads as stale when archivedAt exceeds 7 days', () => {
        const eightDaysAgo = Date.now() - 8 * 24 * 60 * 60 * 1000;
        const result = buildVisibleChatThreadCards({
            threads: [{ id: 'old-thread', name: 'Old Thread' }],
            archivedMap: { 'old-thread': eightDaysAgo },
            showArchived: true,
            displayNameOf: (t) => t.name,
        });
        expect(result.cards[0].isStale).toBe(true);
        expect(result.cards[0].staleReason).toBe('expired');
    });

    it('marks archived threads as stale when showId is true (empty)', () => {
        const result = buildVisibleChatThreadCards({
            threads: [{ id: 'thread-123', name: 'thread-123' }],
            archivedMap: { 'thread-123': Date.now() },
            showArchived: true,
            displayNameOf: (t) => t.name,
        });
        expect(result.cards[0].isStale).toBe(true);
        expect(result.cards[0].staleReason).toBe('empty');
    });

    it('does not mark active threads as stale', () => {
        const result = buildVisibleChatThreadCards({
            threads: [{ id: 'active-thread', name: 'active-thread' }],
            archivedMap: {},
            showArchived: false,
            displayNameOf: (t) => t.name,
        });
        expect(result.cards[0].isStale).toBe(false);
        expect(result.cards[0].staleReason).toBe('');
    });

    it('sorts archived cards by archivedAt descending', () => {
        const now = Date.now();
        const result = buildVisibleChatThreadCards({
            threads: [
                { id: 'old', name: 'Old' },
                { id: 'new', name: 'New' },
                { id: 'mid', name: 'Mid' },
            ],
            archivedMap: {
                'old': now - 3000,
                'new': now - 1000,
                'mid': now - 2000,
            },
            showArchived: true,
            displayNameOf: (t) => t.name,
        });
        expect(result.cards.map((c) => c.id)).toEqual(['new', 'mid', 'old']);
    });

    it('expired takes precedence over empty for stale reason', () => {
        const eightDaysAgo = Date.now() - 8 * 24 * 60 * 60 * 1000;
        const result = buildVisibleChatThreadCards({
            threads: [{ id: 'thread-abc', name: 'thread-abc' }],
            archivedMap: { 'thread-abc': eightDaysAgo },
            showArchived: true,
            displayNameOf: (t) => t.name,
        });
        expect(result.cards[0].isStale).toBe(true);
        expect(result.cards[0].staleReason).toBe('expired');
    });
});
