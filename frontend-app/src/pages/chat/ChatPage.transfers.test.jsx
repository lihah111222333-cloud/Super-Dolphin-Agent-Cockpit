import React from 'react';
import { act, cleanup, createEvent, fireEvent, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { renderWithQueryClient as render } from '../../__tests__/reactQueryRender.jsx';
import { frontendHealthSnapshot, resetFrontendHealthForTest } from '../../shared/diagnostics/frontendHealthStore.js';
import { resetVisibleActionFailureForTest, visibleActionFailureSnapshot } from '../../shared/ui/actionFailureSink.js';
import { TestChatPageWrapper, createActiveThreadStore, onFilesDropped } from './__tests__/chatPageTestSupport.js';

function resetActionFailures() {
  window.localStorage.clear();
  resetFrontendHealthForTest();
  resetVisibleActionFailureForTest();
}

function expectSingleActionFailure(actionId) {
  const failures = frontendHealthSnapshot();
  expect(failures.map((failure) => failure.actionId)).toEqual([actionId]);
  expect(failures[0]).toEqual(expect.objectContaining({ actionId, occurrences: 1 }));
  expect(visibleActionFailureSnapshot()).toEqual(expect.objectContaining({ actionId }));
}

afterEach(async () => {
  cleanup();
  await new Promise((resolve) => setTimeout(resolve, 0));
  resetActionFailures();
});
  it('accepts direct file drops on the composer input', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '把文件拖进输入框即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const dropped = new File(['notes'], 'input-notes.txt', { type: 'text/plain' });
    Object.defineProperty(dropped, 'path', { value: '/tmp/input-notes.txt' });
    const input = screen.getByTestId('composer-input');
    const dropEvent = createEvent.drop(input, {
      bubbles: false,
      cancelable: true,
      dataTransfer: {
        files: [dropped],
        items: [],
        types: ['Files'],
      },
    });

    fireEvent(input, dropEvent);

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]);
    });
    expect(dropEvent.defaultPrevented).toBe(true);
  });

  it('reports rejected path paste through only the precise paste action', async () => {
    resetActionFailures();
    const store = createActiveThreadStore([
      { id: 'msg-paste-failure', role: 'assistant', text: '粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ], {
      attachPathsForComposer: vi.fn().mockRejectedValue(new Error('paste path rejected')),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [],
        items: [],
        types: ['text/uri-list'],
        getData: (type) => (type === 'text/uri-list' ? 'file:///tmp/rejected-paste.txt' : ''),
      },
    });

    await waitFor(() => expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/rejected-paste.txt']));
    await waitFor(() => expect(frontendHealthSnapshot()).not.toHaveLength(0));
    expectSingleActionFailure('composer.file.paste-paths');
  });

  it('reports rejected textarea drop through only the precise file-drop action', async () => {
    resetActionFailures();
    const store = createActiveThreadStore([
      { id: 'msg-textarea-drop-failure', role: 'assistant', text: '拖文件。', time: '2026-06-02T08:00:00Z' },
    ], {
      attachDroppedFilesForComposer: vi.fn().mockRejectedValue(new Error('textarea drop rejected')),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const dropped = new File(['notes'], 'rejected-input.txt', { type: 'text/plain' });
    Object.defineProperty(dropped, 'path', { value: '/tmp/rejected-input.txt' });
    fireEvent(screen.getByTestId('composer-input'), createEvent.drop(screen.getByTestId('composer-input'), {
      bubbles: false,
      cancelable: true,
      dataTransfer: { files: [dropped], items: [], types: ['Files'] },
    }));

    await waitFor(() => expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]));
    await waitFor(() => expect(frontendHealthSnapshot()).not.toHaveLength(0));
    expectSingleActionFailure('composer.file.drop');
  });

  it('reports rejected conversation drop through only the precise file-drop action', async () => {
    resetActionFailures();
    const store = createActiveThreadStore([
      { id: 'msg-conversation-drop-failure', role: 'assistant', text: '拖文件。', time: '2026-06-02T08:00:00Z' },
    ], {
      attachDroppedFilesForComposer: vi.fn().mockRejectedValue(new Error('conversation drop rejected')),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const dropped = new File(['notes'], 'rejected-conversation.txt', { type: 'text/plain' });
    Object.defineProperty(dropped, 'path', { value: '/tmp/rejected-conversation.txt' });
    fireEvent.drop(screen.getByTestId('conversation-drop-zone'), {
      dataTransfer: { files: [dropped], items: [], types: ['Files'] },
    });

    await waitFor(() => expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]));
    await waitFor(() => expect(frontendHealthSnapshot()).not.toHaveLength(0));
    expectSingleActionFailure('composer.file.drop');
  });

  it('falls back to transfer file paths when a dropped DOM File has no path', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ], {
      attachDroppedFilesForComposer: vi.fn(() => 0),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const dropped = new File(['notes'], 'browser-only-notes.txt', { type: 'text/plain' });
    const conversation = screen.getByTestId('conversation-drop-zone');
    const dataTransfer = {
      files: [dropped],
      items: [],
      types: ['Files', 'text/uri-list'],
      getData: (type) => (type === 'text/uri-list' ? 'file:///tmp/browser-only-notes.txt' : ''),
    };

    fireEvent.drop(conversation, { dataTransfer });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]);
    });
    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/browser-only-notes.txt']);
  });

  it('uses transfer file paths when DOM files attach partially', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ], {
      attachDroppedFilesForComposer: vi.fn(() => 1),
    });

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const dropped = new File(['notes'], 'partial-fallback.txt', { type: 'text/plain' });
    const conversation = screen.getByTestId('conversation-drop-zone');
    const dataTransfer = {
      files: [dropped],
      items: [],
      types: ['Files', 'text/uri-list'],
      getData: (type) => (type === 'text/uri-list' ? 'file:///tmp/partial-fallback.txt' : ''),
    };

    fireEvent.drop(conversation, { dataTransfer });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([dropped]);
    });
    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/partial-fallback.txt']);
  });


  it('accepts native Wails file drops when target details only contain chat-area classes', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const nativeDropHandler = onFilesDropped.mock.calls.at(-1)?.[0];

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-composer-class.txt'],
        details: { classList: ['composer-card'] },
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-composer-class.txt']);

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-composer-actions.txt'],
        details: { classList: ['composer-actions'] },
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-composer-actions.txt']);

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-timeline-class.txt'],
        details: { classList: ['timeline'] },
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-timeline-class.txt']);

    const attributeTargetDetails = { attributes: { class: 'timeline-shell' } };
    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-attribute-class.txt'],
        details: attributeTargetDetails,
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-attribute-class.txt']);
  });

  it('accepts native Wails file drops without target details only after entering a chat drop target', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const nativeDropHandler = onFilesDropped.mock.calls.at(-1)?.[0];
    const conversation = screen.getByTestId('conversation-drop-zone');

    fireEvent.dragEnter(conversation, {
      dataTransfer: {
        files: [new File(['notes'], 'native-missing-details.txt', { type: 'text/plain' })],
        items: [],
        types: ['Files'],
      },
    });

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-missing-details.txt'],
      });
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/native-missing-details.txt']);
    expect(conversation).not.toHaveClass('drop-active');
  });

  it('rejects native Wails file drops from clearly non-chat or unknown targets', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);
    const nativeDropHandler = onFilesDropped.mock.calls.at(-1)?.[0];

    act(() => {
      nativeDropHandler?.({
        files: ['/tmp/native-sidebar-drop.txt'],
        details: { id: 'sidebar-thread-item', classList: ['thread-card'] },
      });
      nativeDropHandler?.({
        files: ['/tmp/native-app-nav-drop.txt'],
        details: { classList: ['app-nav'] },
      });
      nativeDropHandler?.({
        files: ['/tmp/native-unknown-drop.txt'],
      });
    });

    expect(store.attachPathsForComposer).not.toHaveBeenCalled();
  });

  it('accepts external uri-list drops on the conversation window', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '拖文件进来即可。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const conversation = screen.getByTestId('conversation-drop-zone');
    const dataTransfer = {
      files: [],
      items: [],
      types: ['text/uri-list'],
      getData: (type) => (type === 'text/uri-list' ? 'file:///tmp/dropped-uri-notes.txt' : ''),
    };

    fireEvent.dragEnter(conversation, { dataTransfer });

    expect(conversation).toHaveClass('drop-active');

    fireEvent.drop(conversation, { dataTransfer });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/dropped-uri-notes.txt']);
    expect(conversation).not.toHaveClass('drop-active');
  });

  it('attaches copied desktop file paths instead of pasting them as composer text', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [],
        items: [],
        types: ['x-special/gnome-copied-files', 'text/uri-list', 'text/plain'],
        getData: (type) => {
          if (type === 'x-special/gnome-copied-files') return 'copy\nfile:///tmp/copied-notes.txt';
          if (type === 'text/uri-list') return 'file:///tmp/copied-notes.txt';
          if (type === 'text/plain') return '/tmp/copied-notes.txt';
          return '';
        },
      },
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/copied-notes.txt']);
    expect(store.setDraft).not.toHaveBeenCalled();
  });

  it('falls back to plain copied file paths when custom clipboard types cannot be read', () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [],
        items: [],
        types: ['x-special/gnome-copied-files', 'text/plain'],
        getData: (type) => {
          if (type === 'x-special/gnome-copied-files') throw new Error('clipboard type unavailable');
          if (type === 'text/plain') return "'/tmp/copied quoted.txt'";
          return '';
        },
      },
    });

    expect(store.attachPathsForComposer).toHaveBeenCalledWith(['/tmp/copied quoted.txt']);
    expect(store.setDraft).not.toHaveBeenCalled();
  });

  it('attaches ordinary clipboard.files File objects with a path instead of pasting text', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['notes'], 'copied-notes.txt', { type: 'text/plain' });
    Object.defineProperty(file, 'path', { value: '/tmp/copied-notes.txt' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [file],
        items: [],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
    expect(store.setDraft).not.toHaveBeenCalled();
  });

  it('attaches ordinary clipboard.items getAsFile File objects with a path', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['notes'], 'item-notes.txt', { type: 'text/plain' });
    Object.defineProperty(file, 'path', { value: '/tmp/item-notes.txt' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [],
        items: [
          { kind: 'file', type: 'text/plain', getAsFile: vi.fn(() => file) },
        ],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
  });

  it('routes PNG clipboard File objects with a path through dropped-file attachment handling', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴图片文件。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['png'], 'copied-image.png', { type: 'image/png' });
    Object.defineProperty(file, 'path', { value: '/tmp/copied-image.png' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [file],
        items: [],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
    expect(store.attachPastedImagesForComposer).not.toHaveBeenCalled();
  });

  it('keeps no-path image paste in attachment handling', async () => {
    const store = createActiveThreadStore([
      { id: 'msg-1', role: 'assistant', text: '可以直接粘贴截图。', time: '2026-06-02T08:00:00Z' },
    ]);

    render(<TestChatPageWrapper store={store} projectPath="/repo/app" />);

    const file = new File(['png'], 'screenshot.png', { type: 'image/png' });

    fireEvent.paste(screen.getByTestId('composer-input'), {
      clipboardData: {
        files: [file],
        items: [],
        types: ['Files'],
        getData: () => '',
      },
    });

    await waitFor(() => {
      expect(store.attachDroppedFilesForComposer).toHaveBeenCalledWith([file]);
    });
  });
