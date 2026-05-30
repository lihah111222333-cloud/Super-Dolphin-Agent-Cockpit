// @ts-nocheck
import { describe, it, expect, vi, beforeEach } from 'vitest';

const apiMock = vi.hoisted(() => ({
  callAPI: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  callAPI: apiMock.callAPI,
}));

vi.mock('./stores/projects.js', () => ({
  useProjectStore: () => ({
    state: {
      active: '/repo',
      projects: ['/repo', '/repo-2'],
    },
  }),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

vi.mock('./utils/assistant-markdown.js', () => ({
  renderAssistantMarkdown: (text) => text,
  injectSentenceBreaks: vi.fn((text) => text),
}));

import { reactive, nextTick } from '../lib/vue.esm-browser.prod.js';
import { DiffPanel } from './components/DiffPanel.js';

function makeDiffText() {
  return [
    'diff --git a/src/a.js b/src/a.js',
    '--- a/src/a.js',
    '+++ b/src/a.js',
    '@@ -1,1 +1,2 @@',
    ' line1',
    '+added',
    'diff --git a/src/b.js b/src/b.js',
    '--- a/src/b.js',
    '+++ b/src/b.js',
    '@@ -1,1 +1,2 @@',
    ' line9',
    '+other',
  ].join('\n');
}

function makeMarkdownPreview(overrides = {}) {
  return {
    previewKind: 'markdown',
    path: 'docs/readme.md',
    filePath: '/repo/docs/readme.md',
    text: '# Title\n\n[ref](src/a.js#L7)',
    language: 'markdown',
    startLine: 1,
    endLine: 3,
    totalLines: 3,
    editable: true,
    ...overrides,
  };
}

function makeTextPreview(overrides = {}) {
  return {
    previewKind: 'text',
    path: 'config/app.yaml',
    filePath: '/repo/config/app.yaml',
    text: 'enabled: true\nname: demo',
    language: 'yaml',
    startLine: 1,
    endLine: 2,
    totalLines: 2,
    editable: true,
    ...overrides,
  };
}

function makeProps(overrides = {}) {
  return reactive({
    diffText: makeDiffText(),
    mediaPreview: null,
    markdownPreview: null,
    focusFile: '',
    focusLine: 0,
    project: '',
    projects: [],
    ...overrides,
  });
}

describe('DiffPanel', () => {
  beforeEach(() => {
    apiMock.callAPI.mockReset().mockResolvedValue({ ok: true, filePath: '/repo/docs/readme.md', relative: 'docs/readme.md', totalLines: 3 });
  });

  it('groups diff entries by filename and collapses files independently', () => {
    const vm = DiffPanel.setup(makeProps());
    expect(vm.files.value.map((file) => vm.displayFilePath(file))).toEqual(['src/a.js', 'src/b.js']);
    expect(vm.fileCaretSymbol(vm.files.value[0], 0)).toBe('▾');

    vm.toggleFileCollapsed(vm.files.value[0], 0);
    expect(vm.isFileCollapsed(vm.files.value[0], 0)).toBe(true);
    expect(vm.isFileCollapsed(vm.files.value[1], 1)).toBe(false);
    expect(vm.fileCaretSymbol(vm.files.value[0], 0)).toBe('▸');
  });

  it('groups headerless multi-file unified diff into separate file sections', () => {
    const diffText = [
      '--- a/src/a.js',
      '+++ b/src/a.js',
      '@@ -1,1 +1,1 @@',
      '-old',
      '+new',
      '--- a/src/b.js',
      '+++ b/src/b.js',
      '@@ -1,1 +1,1 @@',
      '-x',
      '+y',
    ].join('\n');
    const vm = DiffPanel.setup(makeProps({ diffText }));
    expect(vm.files.value.map((file) => vm.displayFilePath(file))).toEqual(['src/a.js', 'src/b.js']);
  });

  it('re-expands the focused file before syncing focus', async () => {
    const props = makeProps();
    const vm = DiffPanel.setup(props);

    vm.toggleFileCollapsed(vm.files.value[0], 0);
    expect(vm.isFileCollapsed(vm.files.value[0], 0)).toBe(true);

    props.focusFile = 'src/a.js';
    props.focusLine = 2;
    await nextTick();
    await nextTick();

    expect(vm.isFileCollapsed(vm.files.value[0], 0)).toBe(false);
  });

  it('shows the empty diff contract when diff text is blank', () => {
    const vm = DiffPanel.setup(makeProps({ diffText: '' }));

    expect(vm.files.value).toEqual([]);
    expect(vm.fileCountText.value).toBe('0 files');
    expect(vm.hasDiffPreview.value).toBe(true);
    expect(DiffPanel.template).toContain('class="diff-empty"');
  });

  it('falls back to a lightweight preview for very large diffs until full diff is requested', () => {
    const largeDiff = `${makeDiffText()}\n${' line\n'.repeat(30000)}`;
    const vm = DiffPanel.setup(makeProps({ diffText: largeDiff }));

    expect(vm.showLargeDiffPreview.value).toBe(true);
    expect(vm.largeDiffPreviewText.value).toContain('当前 diff 约');

    vm.loadFullDiff();

    expect(vm.showLargeDiffPreview.value).toBe(false);
  });

  it('scrolls the focused diff line into view when a rendered line is found', async () => {
    const props = makeProps();
    const vm = DiffPanel.setup(props);
    const scrollIntoView = vi.fn();

    vm.panelRef.value = {
      querySelector: vi.fn((selector) => (
        selector === '.diff-line.is-focused-line'
          ? { textContent: '+added', scrollIntoView }
          : null
      )),
    };

    props.focusFile = 'src/a.js';
    props.focusLine = 2;
    await nextTick();
    await nextTick();

    expect(scrollIntoView).toHaveBeenCalledWith({ behavior: 'smooth', block: 'center' });
  });

  it('opens and closes the media lightbox when a media preview exists', () => {
    const vm = DiffPanel.setup(makeProps({
      diffText: '',
      mediaPreview: {
        src: 'thumb.png',
        fullSrc: 'full.png',
        path: 'screenshots/diff.png',
      },
    }));

    vm.openLightbox();
    expect(vm.lightboxOpen.value).toBe(true);

    vm.closeLightbox();
    expect(vm.lightboxOpen.value).toBe(false);
  });

  it('renders markdown and text previews in different read modes', () => {
    const markdownVm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeMarkdownPreview() }));
    const textVm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeTextPreview() }));
    const plainTextVm = DiffPanel.setup(makeProps({
      diffText: '',
      markdownPreview: makeTextPreview({ path: 'docs/notes.txt', filePath: '/repo/docs/notes.txt', text: 'hello\nworld', language: 'plaintext' }),
    }));

    expect(markdownVm.isMarkdownPreview.value).toBe(true);
    expect(markdownVm.markdownHtml.value).toContain('# Title');
    expect(textVm.isCodeTextPreview.value).toBe(true);
    expect(textVm.textPreviewHtml.value).toContain('```yaml');
    expect(plainTextVm.isPlainTextPreview.value).toBe(true);
    expect(plainTextVm.textPreviewPlainText.value).toBe('hello\nworld');
  });

  it('emits markdown preview file-ref and citation clicks through the shared handler', () => {
    const emit = vi.fn();
    const vm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeMarkdownPreview({ text: '[ref](src/a.js#L7) :task-stub[Review]{title="Task"}' }) }), { emit });

    const fileRefNode = {
      getAttribute: vi.fn((name) => ({ 'data-file-path': 'src/a.js', 'data-file-line': '7', 'data-file-column': '2' }[name] || '')),
      textContent: 'src/a.js:7',
    };
    vm.onMarkdownPreviewClick({ target: { closest: vi.fn((selector) => (selector.includes('chat-md-file-ref') ? fileRefNode : null)) }, preventDefault: vi.fn(), stopPropagation: vi.fn() });
    expect(emit).toHaveBeenCalledWith('file-ref-click', { path: 'src/a.js', line: 7, column: 2, raw: 'src/a.js:7' });

    const citationNode = {
      getAttribute: vi.fn((name) => ({ 'data-citation-kind': 'task', 'data-task-title': 'Task', 'data-task-prompt': 'Review patch' }[name] || '')),
      textContent: 'Task',
    };
    vm.onMarkdownPreviewClick({ target: { closest: vi.fn((selector) => (selector.includes('chat-md-citation') ? citationNode : null)) }, preventDefault: vi.fn(), stopPropagation: vi.fn() });
    expect(emit).toHaveBeenCalledWith('citation-click', { kind: 'task', title: 'Task', prompt: 'Review patch', raw: 'Task' });
  });

  it('switches into edit mode for editable previews', async () => {
    const vm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeTextPreview() }));

    vm.startEditing();
    await nextTick();

    expect(vm.isEditing.value).toBe(true);
    expect(vm.draftText.value).toBe('enabled: true\nname: demo');
    expect(DiffPanel.template).toContain('<textarea');
  });

  it('does not forward markdown click actions while editing', () => {
    const emit = vi.fn();
    const vm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeMarkdownPreview() }), { emit });
    vm.startEditing();

    const fileRefNode = {
      getAttribute: vi.fn((name) => ({ 'data-file-path': 'src/a.js', 'data-file-line': '7', 'data-file-column': '2' }[name] || '')),
      textContent: 'src/a.js:7',
    };
    vm.onMarkdownPreviewClick({ target: { closest: vi.fn((selector) => (selector.includes('chat-md-file-ref') ? fileRefNode : null)) }, preventDefault: vi.fn(), stopPropagation: vi.fn() });

    // The isEditing guard prevents any action dispatch
    expect(emit).not.toHaveBeenCalledWith('file-ref-click', expect.anything());
    expect(emit).not.toHaveBeenCalledWith('citation-click', expect.anything());
  });

  it('saves edited preview content through ui/code/save with LF line endings', async () => {
    apiMock.callAPI.mockResolvedValueOnce({
      ok: true,
      filePath: '/repo/docs/readme.md',
      relative: 'docs/readme.md',
      totalLines: 2,
    });
    const vm = DiffPanel.setup(makeProps({
      diffText: '',
      project: '/repo',
      projects: ['/repo', '/repo-2'],
      markdownPreview: makeMarkdownPreview(),
    }));

    vm.startEditing();
    vm.draftText.value = '# Saved\r\n\r\nBody';
    const saved = await vm.savePreviewChanges();
    await nextTick();

    expect(saved).toBe(true);
    expect(apiMock.callAPI).toHaveBeenCalledWith('ui/code/save', {
      filePath: '/repo/docs/readme.md',
      content: '# Saved\n\nBody',
      project: '/repo',
      projects: ['/repo', '/repo-2'],
    });
    expect(vm.isEditing.value).toBe(false);
    expect(vm.markdownHtml.value).toBe('# Saved\n\nBody');
  });

  it('cancels editing and restores the original content', async () => {
    const vm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeTextPreview() }));

    vm.startEditing();
    vm.draftText.value = 'changed';
    vm.cancelEditing();
    await nextTick();

    expect(vm.isEditing.value).toBe(false);
    expect(vm.draftText.value).toBe('enabled: true\nname: demo');
    expect(vm.textPreviewPlainText.value).toBe('');
    expect(vm.textPreviewHtml.value).toContain('enabled: true');
  });

  it('reports dirty-state changes through preview-dirty-change emits', async () => {
    const emit = vi.fn();
    const vm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeTextPreview() }), { emit });
    emit.mockClear();

    vm.startEditing();
    vm.draftText.value = 'enabled: false';
    await nextTick();
    vm.cancelEditing();
    await nextTick();

    expect(emit).toHaveBeenCalledWith('preview-dirty-change', true);
    expect(emit).toHaveBeenCalledWith('preview-dirty-change', false);
  });

  it('restores editing state on save failure and displays error', async () => {
    apiMock.callAPI.mockRejectedValueOnce(new Error('权限不足'));
    const vm = DiffPanel.setup(makeProps({
      diffText: '',
      markdownPreview: makeTextPreview(),
    }));

    vm.startEditing();
    vm.draftText.value = 'changed content';
    const saved = await vm.savePreviewChanges();
    await nextTick();

    expect(saved).toBe(false);
    expect(vm.isEditing.value).toBe(true);
    expect(vm.saving.value).toBe(false);
    expect(vm.saveError.value).toBe('权限不足');
    expect(vm.draftText.value).toBe('changed content');
  });

  it('resets editing state when the underlying preview changes (file switch)', async () => {
    const emit = vi.fn();
    const props = makeProps({ diffText: '', markdownPreview: makeTextPreview() });
    const vm = DiffPanel.setup(props, { emit });

    vm.startEditing();
    vm.draftText.value = 'unsaved changes';
    await nextTick();
    emit.mockClear();

    // Simulate switching to a different file
    props.markdownPreview = makeTextPreview({
      path: 'config/other.yaml',
      filePath: '/repo/config/other.yaml',
      text: 'different: content',
    });
    await nextTick();

    expect(vm.isEditing.value).toBe(false);
    expect(vm.saveError.value).toBe('');
    // Should have emitted dirty=false to clean up parent state
    expect(emit).toHaveBeenCalledWith('preview-dirty-change', false);
  });

  it('guards onMarkdownPreviewClick with isEditing check at function level', () => {
    const emit = vi.fn();
    const vm = DiffPanel.setup(makeProps({ diffText: '', markdownPreview: makeMarkdownPreview() }), { emit });
    vm.startEditing();

    // Directly invoke the handler — template v-if would also prevent this,
    // but the function itself should be self-guarding.
    vm.onMarkdownPreviewClick({ target: { closest: () => null }, preventDefault: vi.fn(), stopPropagation: vi.fn() });

    expect(emit).not.toHaveBeenCalledWith('file-ref-click', expect.anything());
    expect(emit).not.toHaveBeenCalledWith('citation-click', expect.anything());
  });

  it('falls back to path-based preview kind when previewKind field is absent', () => {
    const vm = DiffPanel.setup(makeProps({
      diffText: '',
      markdownPreview: {
        path: 'docs/readme.md',
        filePath: '/repo/docs/readme.md',
        text: '# Fallback',
        language: '',
        startLine: 1,
        endLine: 1,
        totalLines: 1,
        editable: true,
        // previewKind intentionally omitted
      },
    }));

    expect(vm.hasMarkdownPreview.value).toBe(true);
    expect(vm.isMarkdownPreview.value).toBe(true);
    expect(vm.markdownHtml.value).toContain('# Fallback');
  });

  it('wires preview editing and per-file collapse handling in the template', () => {
    expect(DiffPanel.template).toContain('@click="toggleFileCollapsed(file, fileIndex)"');
    expect(DiffPanel.template).toContain('v-show="!isFileCollapsed(file, fileIndex)"');
    expect(DiffPanel.template).toContain(':key="fileKey(file, fileIndex)"');
    expect(DiffPanel.template).toContain('@click="onMarkdownPreviewClick"');
    expect(DiffPanel.template).toContain('@click="savePreviewChanges"');
    expect(DiffPanel.template).toContain('@click="cancelEditing"');
  });
});
