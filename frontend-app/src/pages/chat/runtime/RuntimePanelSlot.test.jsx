import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { RuntimePanelSlot } from './RuntimePanelSlot.jsx';
import { solveWorkbenchGeometry } from '../../../shared/layout/workbenchGeometry.js';

const threadData = {
  diffText: 'diff --git a/readme.md b/readme.md\n+hello',
  tokenUsage: { usedTokens: 10, contextWindowTokens: 100, usedPercent: 10 },
  activityStats: { commands: 1 },
  warnings: [],
  runtimeResults: [],
};

function deferred() {
  const pending = {};
  pending.promise = new Promise((resolve, reject) => {
    pending.resolve = resolve;
    pending.reject = reject;
  });
  return pending;
}

function renderSlot(overrides = {}) {
  const geometrySnapshot = solveWorkbenchGeometry({
    activityHeight: 64,
    railOpen: false,
    railWidth: 340,
    rightDisplayWidth: 240,
    rightOpen: true,
    rightPreference: 240,
    viewportHeight: 640,
    viewportWidth: 800,
  });
  const props = {
    beginResize: vi.fn(),
    codeFileActions: {
      locateCodeFile: vi.fn(),
      openCodeFile: vi.fn(),
      saveCodeFile: vi.fn(),
    },
    formatTime: (value) => value,
    geometrySnapshot,
    handleKeyDown: vi.fn(),
    layoutActions: {
      activity: { begin: vi.fn(), keyDown: vi.fn() },
    },
    open: true,
    projectPath: '/repo/app',
    projects: ['/repo/app'],
    renderMarkdownPreview: (content) => <pre>{content}</pre>,
    threadData,
    ...overrides,
  };
  return { props, ...render(<RuntimePanelSlot {...props} />) };
}
  it('does not render when the right panel is closed', () => {
    renderSlot({ open: false });

    expect(screen.queryByTestId('right-panel-resizer')).toBeNull();
    expect(screen.queryByTestId('runtime-panel')).toBeNull();
  });

  it('renders the right panel shell and wires resize events', () => {
    const { props } = renderSlot();
    const resizer = screen.getByTestId('right-panel-resizer');

    expect(screen.getByTestId('runtime-panel')).toBeInTheDocument();
    expect(resizer).toHaveAttribute('aria-valuemin', '0');
    expect(resizer).toHaveAttribute('aria-valuemax', String(props.geometrySnapshot.aria.rightMax));
    expect(resizer).toHaveAttribute('aria-valuenow', '240');

    fireEvent.keyDown(resizer, { key: 'ArrowLeft' });
    fireEvent.pointerDown(resizer, { pointerId: 1, clientX: 0 });

    expect(props.handleKeyDown).toHaveBeenCalledTimes(1);
    expect(props.beginResize).toHaveBeenCalledTimes(1);
  });

  it('ignores stale code preview responses from earlier diff open actions', async () => {
    const firstOpen = deferred();
    const secondOpen = deferred();
    const codeFileActions = {
      locateCodeFile: vi.fn(),
      openCodeFile: vi.fn(({ filePath }) => {
        if (filePath === 'src/a.js') return firstOpen.promise;
        if (filePath === 'src/b.js') return secondOpen.promise;
        throw new Error(`unexpected open path ${filePath}`);
      }),
      saveCodeFile: vi.fn().mockResolvedValue({
        ok: true,
        filePath: 'src/b.js',
        relative: 'src/b.js',
        totalLines: 1,
        contentVersion: 'version-src-b-saved',
      }),
    };
    const diffText = [
      'diff --git a/src/a.js b/src/a.js',
      '+++ b/src/a.js',
      '@@ -0,0 +1 @@',
      '+a',
      'diff --git a/src/b.js b/src/b.js',
      '+++ b/src/b.js',
      '@@ -0,0 +1 @@',
      '+b',
    ].join('\n');
    renderSlot({
      codeFileActions,
      threadData: { ...threadData, diffText },
    });

    fireEvent.click(screen.getByRole('button', { name: '打开 src/a.js' }));
    await waitFor(() => expect(codeFileActions.openCodeFile).toHaveBeenCalledTimes(1));
    fireEvent.click(screen.getByRole('button', { name: '打开 src/b.js' }));
    await waitFor(() => expect(codeFileActions.openCodeFile).toHaveBeenCalledTimes(2));

    await act(async () => {
      secondOpen.resolve({
        ok: true,
        filePath: 'src/b.js',
        relative: 'src/b.js',
        snippet: [{ line: 1, text: 'const latest = true;' }],
        startLine: 1,
        endLine: 1,
        totalLines: 1,
        previewMode: 'full',
        contentVersion: 'version-src-b',
      });
    });
    const preview = await screen.findByRole('dialog', { name: '文件预览' });
    expect(within(preview).getByText('src/b.js')).toBeInTheDocument();
    expect(within(preview).getByLabelText('文件预览内容')).toHaveValue('const latest = true;');

    await act(async () => {
      firstOpen.resolve({
        ok: true,
        filePath: 'src/a.js',
        relative: 'src/a.js',
        snippet: [{ line: 1, text: 'const stale = true;' }],
        startLine: 1,
        endLine: 1,
        totalLines: 1,
      });
    });

    expect(within(preview).getByText('src/b.js')).toBeInTheDocument();
    const editor = within(preview).getByLabelText('文件预览内容');
    expect(editor).toHaveValue('const latest = true;');
    fireEvent.change(editor, { target: { value: 'const latest = false;' } });
    fireEvent.click(within(preview).getByRole('button', { name: '保存预览更改' }));

    await waitFor(() => expect(codeFileActions.saveCodeFile).toHaveBeenCalledTimes(1));
    expect(codeFileActions.saveCodeFile).toHaveBeenCalledWith(expect.objectContaining({
      filePath: 'src/b.js',
      content: 'const latest = false;',
      previewMode: 'full',
      contentVersion: 'version-src-b',
    }));

    fireEvent.change(editor, { target: { value: 'const latest = "again";' } });
    fireEvent.click(within(preview).getByRole('button', { name: '保存预览更改' }));

    await waitFor(() => expect(codeFileActions.saveCodeFile).toHaveBeenCalledTimes(2));
    expect(codeFileActions.saveCodeFile).toHaveBeenNthCalledWith(2, expect.objectContaining({
      filePath: 'src/b.js',
      content: 'const latest = "again";',
      previewMode: 'full',
      contentVersion: 'version-src-b-saved',
    }));
  }, 10_000);

  it('keeps edits typed while a code preview save is in flight marked as unsaved', async () => {
    const save = deferred();
    const codeFileActions = {
      locateCodeFile: vi.fn(),
      openCodeFile: vi.fn().mockResolvedValue({
        ok: true,
        filePath: 'docs/plan.md',
        relative: 'docs/plan.md',
        snippet: [{ line: 1, text: 'saved snapshot' }],
        startLine: 1,
        endLine: 1,
        totalLines: 1,
        previewMode: 'full',
        contentVersion: 'version-docs-plan',
      }),
      saveCodeFile: vi.fn(() => save.promise),
    };
    const diffText = [
      'diff --git a/docs/plan.md b/docs/plan.md',
      '+++ b/docs/plan.md',
      '@@ -0,0 +1 @@',
      '+saved snapshot',
    ].join('\n');
    renderSlot({
      codeFileActions,
      threadData: { ...threadData, diffText },
    });

    fireEvent.click(screen.getByRole('button', { name: '打开 docs/plan.md' }));
    const preview = await screen.findByRole('dialog', { name: '文件预览' });
    fireEvent.click(within(preview).getByRole('button', { name: '编辑预览' }));
    const editor = within(preview).getByLabelText('文件预览内容');
    fireEvent.change(editor, { target: { value: 'saved snapshot\nsaved before click' } });
    fireEvent.click(within(preview).getByRole('button', { name: '保存预览更改' }));
    await waitFor(() => expect(codeFileActions.saveCodeFile).toHaveBeenCalledTimes(1));
    fireEvent.change(editor, { target: { value: 'saved snapshot\nsaved before click\nnew unsaved edit' } });

    await act(async () => {
      save.resolve({
        ok: true,
        filePath: 'docs/plan.md',
        relative: 'docs/plan.md',
        totalLines: 2,
        contentVersion: 'version-docs-plan-saved',
      });
    });

    expect(editor).toHaveValue('saved snapshot\nsaved before click\nnew unsaved edit');
    expect(within(preview).getByText('已保存 docs/plan.md，仍有未保存更改')).toBeInTheDocument();
    fireEvent.click(within(preview).getByRole('button', { name: '关闭文件预览' }));
    expect(within(preview).getByRole('alert')).toHaveTextContent('请先保存或放弃预览更改');
  });
