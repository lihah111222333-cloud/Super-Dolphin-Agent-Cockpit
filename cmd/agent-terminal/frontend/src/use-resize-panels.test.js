// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { nextTick, ref } from '../lib/vue.esm-browser.prod.js';

import { useResizePanels } from './composables/useResizePanels.js';

function createResizePanels(overrides = {}) {
  const listeners = {};
  vi.stubGlobal('window', {
    innerHeight: 1000,
    addEventListener: vi.fn((type, cb) => {
      listeners[type] = cb;
    }),
    removeEventListener: vi.fn(),
  });
  vi.stubGlobal('document', {
    body: {
      classList: {
        add: vi.fn(),
        remove: vi.fn(),
      },
    },
  });

  const threadStore = {
    getSplitRatio: vi.fn(() => 62),
    setSplitRatio: vi.fn(),
    getThreadRailWidth: vi.fn(() => 240),
    setThreadRailWidth: vi.fn(),
    ...overrides.threadStore,
  };
  const opts = {
    isCmd: ref(overrides.isCmd ?? false),
    modeKey: ref(overrides.modeKey ?? 'chat'),
    showWorkspace: ref(overrides.showWorkspace ?? true),
    threadStore,
    workspaceRef: ref(overrides.workspace ?? {
      getBoundingClientRect: () => ({ left: 0, width: 1000 }),
    }),
  };
  const vm = useResizePanels(opts);
  return { vm, opts, threadStore, listeners };
}

beforeEach(() => {
  vi.unstubAllGlobals();
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('useResizePanels', () => {
  it('derives chat layout styles from split ratio and thread rail width', () => {
    const { vm } = createResizePanels();

    expect(vm.threadRailStyle.value).toEqual({ width: '240px', flex: '0 0 240px' });
    expect(vm.chatComposerShellStyle.value).toEqual({ width: 'calc(62% - 6px)' });
    expect(vm.activityPanelRowStyle.value).toEqual({
      '--activity-panel-base-height': '124px',
      '--activity-panel-overlay-height': '124px',
      '--activity-panel-fixed-height': '124px',
    });
  });

  it('returns empty layout styles in cmd mode', () => {
    const { vm } = createResizePanels({ isCmd: true });
    expect(vm.threadRailStyle.value).toEqual({});
    expect(vm.chatComposerShellStyle.value).toEqual({});
    expect(vm.activityPanelRowStyle.value).toEqual({});
  });

  it('updates split ratio during workspace drag and persists the new value', async () => {
    const { vm, listeners, threadStore } = createResizePanels();
    const event = { button: 0, clientX: 100, preventDefault: vi.fn(), stopPropagation: vi.fn() };

    vm.onResizeStart(event);
    listeners.mousemove({ clientX: 840 });
    await nextTick();

    expect(vm.splitRatio.value).toBe(75);
    expect(threadStore.setSplitRatio).toHaveBeenCalledWith('chat', 75);

    listeners.mouseup();
    expect(vm.dragging.value).toBe(false);
  });

  it('updates thread rail width during drag and persists clamped values', async () => {
    const { vm, listeners, threadStore } = createResizePanels();
    const event = { button: 0, clientX: 100, preventDefault: vi.fn(), stopPropagation: vi.fn() };

    vm.onThreadRailResizeStart(event);
    listeners.mousemove({ clientX: 500 });
    await nextTick();

    expect(vm.threadRailWidth.value).toBe(420);
    expect(threadStore.setThreadRailWidth).toHaveBeenCalledWith(420);

    listeners.mouseup();
    expect(vm.threadRailDragging.value).toBe(false);
  });

  it('updates activity panel height during vertical drag', () => {
    const { vm, listeners } = createResizePanels();
    const event = { button: 0, clientY: 500, preventDefault: vi.fn(), stopPropagation: vi.fn() };

    vm.onActivityResizeStart(event);
    listeners.mousemove({ clientY: 300 });

    expect(vm.activityPanelHeight.value).toBe(324);

    listeners.mouseup();
    expect(vm.activityPanelDragging.value).toBe(false);
  });
});
