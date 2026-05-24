// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const lifecycleState = vi.hoisted(() => ({
  mounted: [],
  beforeUnmount: [],
  updated: [],
  watches: [],
}));

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: (fn) => {
      lifecycleState.mounted.push(fn);
      fn();
    },
    onBeforeUnmount: (fn) => {
      lifecycleState.beforeUnmount.push(fn);
    },
    onUpdated: (fn) => {
      lifecycleState.updated.push(fn);
    },
    watch: (source, callback, options) => {
      lifecycleState.watches.push({ source, callback, options });
      if (options?.immediate) callback();
      return () => {};
    },
  };
});

const apiMock = vi.hoisted(() => ({
  onFilesDropped: vi.fn(),
}));

vi.mock('./services/api.js', () => ({
  onFilesDropped: apiMock.onFilesDropped,
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import {
  applyComposerTextareaAutoHeight,
  ComposerBar,
} from './components/ComposerBar.js';
import { useComposerTextarea } from './composables/useComposerTextarea.js';
import { useComposerThreadConfig } from './composables/useComposerThreadConfig.js';

let registeredNativeDropHandler = null;
let registeredNativeDropDispose = vi.fn();

function createComposerBar(overrides = {}, emit = vi.fn()) {
  const props = {
    composer: {
      canSend: { value: overrides.canSend ?? true },
      state: {
        text: overrides.text ?? '',
        attaching: overrides.attaching ?? false,
        attachments: overrides.attachments ?? [],
      },
      handlePaste: vi.fn(),
      handleDrop: vi.fn().mockResolvedValue(undefined),
      attachByPaths: vi.fn(() => 1),
      attachByPicker: vi.fn(),
      removeAttachment: vi.fn(),
    },
    disabled: overrides.disabled ?? false,
    threadId: Object.prototype.hasOwnProperty.call(overrides, 'threadId') ? overrides.threadId : 'thread-1',
    interruptible: overrides.interruptible ?? false,
    compacting: overrides.compacting ?? false,
    canCompact: overrides.canCompact ?? true,
    compactResultText: overrides.compactResultText ?? '',
    compactResultTone: overrides.compactResultTone ?? '',
    compactSuccessCount: overrides.compactSuccessCount ?? 0,
    tokenInline: '',
    tokenTooltip: '',
    isCmd: overrides.isCmd ?? false,
    threadConfigProvider: overrides.threadConfigProvider ?? '',
    threadConfigSupportsOverride: overrides.threadConfigSupportsOverride ?? false,
    threadConfigDraftModel: overrides.threadConfigDraftModel ?? '',
    threadConfigDraftEffort: overrides.threadConfigDraftEffort ?? '',
    threadConfigLoading: overrides.threadConfigLoading ?? false,
    threadConfigSaving: overrides.threadConfigSaving ?? false,
    threadConfigNotice: overrides.threadConfigNotice ?? '',
    threadConfigNoticeLevel: overrides.threadConfigNoticeLevel ?? 'info',
    threadConfigMeta: overrides.threadConfigMeta ?? { override: {}, effective: {} },
    threadIsTask: overrides.threadIsTask ?? false,
    promotingTask: overrides.promotingTask ?? false,
    threadTaskId: overrides.threadTaskId ?? '',
    promoteTaskError: overrides.promoteTaskError ?? '',
  };
  const vm = ComposerBar.setup(props, { emit });
  return { props, emit, vm };
}

async function flushPromises() {
  await Promise.resolve();
  await Promise.resolve();
}

beforeEach(() => {
  lifecycleState.mounted.length = 0;
  lifecycleState.beforeUnmount.length = 0;
  lifecycleState.updated.length = 0;
  lifecycleState.watches.length = 0;
  registeredNativeDropHandler = null;
  registeredNativeDropDispose = vi.fn();
  apiMock.onFilesDropped.mockReset().mockImplementation((handler) => {
    registeredNativeDropHandler = handler;
    return registeredNativeDropDispose;
  });
  vi.stubGlobal('window', {
    innerHeight: 1000,
    setTimeout: vi.fn(() => 1),
    clearTimeout: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  });
  vi.stubGlobal('document', {
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('ComposerBar behavior', () => {
  it('emits send only when input is ready and not blocked by composition', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({}, emit);

    vm.onSend({ type: 'keydown', keyCode: 229, key: 'Process', preventDefault: vi.fn(), isComposing: true });
    expect(emit).not.toHaveBeenCalled();

    const preventDefault = vi.fn();
    vm.onSend({ type: 'keydown', key: 'Enter', preventDefault, isComposing: false });
    expect(preventDefault).toHaveBeenCalled();
    expect(emit).toHaveBeenCalledWith('send');
  });

  it('skips send when the composer has no ready input', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ canSend: false }, emit);

    const preventDefault = vi.fn();
    vm.onSend({ type: 'keydown', key: 'Enter', preventDefault, isComposing: false });

    expect(preventDefault).not.toHaveBeenCalled();
    expect(emit).not.toHaveBeenCalled();
  });

  it('emits interrupt payloads and resolves pause acknowledgement on confirm', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ interruptible: true, canSend: false }, emit);

    vm.onPrimaryAction({ type: 'click' });

    const [, payload] = emit.mock.calls[0];
    expect(emit.mock.calls[0][0]).toBe('interrupt');
    expect(vm.interruptPending.value).toBe(true);

    payload.confirm({ mode: 'confirmed' });
    expect(vm.interruptPending.value).toBe(false);
    expect(vm.pauseAcknowledged.value).toBe(true);
  });

  it('ignores interrupt callbacks when the active thread has changed', () => {
    const emit = vi.fn();
    const { vm, props } = createComposerBar({ interruptible: true, canSend: false, threadId: 'thread-1' }, emit);

    vm.onPrimaryAction({ type: 'click' });
    const [, payload] = emit.mock.calls[0];

    props.threadId = 'thread-2';
    payload.confirm({ mode: 'confirmed' });
    expect(vm.interruptPending.value).toBe(true);
    expect(vm.pauseAcknowledged.value).toBe(false);

    payload.reject({ reason: 'busy', mode: 'reject' });
    expect(vm.interruptPending.value).toBe(true);
    expect(vm.interruptRequestThreadId.value).toBe('thread-1');
  });

  it('ignores duplicate primary actions and auto rejects pending interrupts on timeout', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ interruptible: true, canSend: false }, emit);

    vm.onPrimaryAction({ type: 'click' });
    vm.onPrimaryAction({ type: 'click' });
    expect(emit).toHaveBeenCalledTimes(1);

    const timeoutCallback = window.setTimeout.mock.calls[0][0];
    timeoutCallback();

    expect(vm.interruptPending.value).toBe(false);
    expect(vm.pauseAcknowledged.value).toBe(false);
  });

  it('emits interrupt payloads from escape and blocks duplicate pending escapes', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ interruptible: true, canSend: false, threadId: 'thread-escape' }, emit);

    const firstPreventDefault = vi.fn();
    vm.onEscape({ preventDefault: firstPreventDefault });
    expect(firstPreventDefault).toHaveBeenCalled();
    expect(emit).toHaveBeenCalledWith('interrupt', expect.objectContaining({
      threadId: 'thread-escape',
      confirm: expect.any(Function),
      reject: expect.any(Function),
    }));
    expect(vm.interruptPending.value).toBe(true);

    const secondPreventDefault = vi.fn();
    vm.onEscape({ preventDefault: secondPreventDefault });
    expect(secondPreventDefault).toHaveBeenCalled();
    expect(emit).toHaveBeenCalledTimes(1);

    const [, payload] = emit.mock.calls[0];
    payload.reject({ reason: 'busy', mode: 'reject' });
    expect(vm.interruptPending.value).toBe(false);
  });

  it('skips escape interrupt when there is no active thread id', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ interruptible: true, threadId: '' }, emit);

    const preventDefault = vi.fn();
    vm.onEscape({ preventDefault });

    expect(preventDefault).not.toHaveBeenCalled();
    expect(emit).not.toHaveBeenCalled();
    expect(vm.interruptPending.value).toBe(false);
  });

  it('emits compact only when the composer is enabled, idle, supported and bound to a thread', () => {
    const blockedEmit = vi.fn();
    createComposerBar({ disabled: true }, blockedEmit).vm.onCompact({ type: 'click' });
    createComposerBar({ compacting: true }, blockedEmit).vm.onCompact({ type: 'click' });
    createComposerBar({ canCompact: false }, blockedEmit).vm.onCompact({ type: 'click' });
    createComposerBar({ threadId: '' }, blockedEmit).vm.onCompact({ type: 'click' });
    expect(blockedEmit).not.toHaveBeenCalled();

    const emit = vi.fn();
    const { vm } = createComposerBar({}, emit);
    vm.onCompact({ type: 'click' });

    expect(emit).toHaveBeenCalledWith('compact');
  });

  it('delegates attachment actions without leaking click-event payloads into composer methods', () => {
    const { vm, props } = createComposerBar({
      attachments: [{ path: '/tmp/a.txt', name: 'a.txt', kind: 'file' }],
    });

    vm.onAttach({ type: 'click', target: {} });
    vm.onRemoveAttachment(0, { type: 'click', target: {} });

    expect(props.composer.attachByPicker).toHaveBeenCalledTimes(1);
    expect(props.composer.attachByPicker).toHaveBeenCalledWith();
    expect(props.composer.removeAttachment).toHaveBeenCalledWith(0);
  });

  it('tracks file drag state and delegates drops to composer.handleDrop', async () => {
    const { vm, props } = createComposerBar();
    const transfer = { files: ['a.txt'], types: ['Files'], dropEffect: 'none' };

    vm.onDragEnter({ dataTransfer: transfer, preventDefault: vi.fn() });
    expect(vm.dropActive.value).toBe(true);
    expect(vm.dropDepth.value).toBe(1);

    vm.onDragOver({ dataTransfer: transfer, preventDefault: vi.fn() });
    expect(transfer.dropEffect).toBe('copy');

    await vm.onDrop({ dataTransfer: transfer, preventDefault: vi.fn() });
    expect(props.composer.handleDrop).toHaveBeenCalled();
    expect(vm.dropActive.value).toBe(false);
    expect(vm.dropDepth.value).toBe(0);
  });

  it('registers native file drop handlers during mount and ignores unrelated targets', () => {
    const { vm, props } = createComposerBar();
    expect(apiMock.onFilesDropped).toHaveBeenCalledTimes(1);
    expect(typeof registeredNativeDropHandler).toBe('function');

    const transfer = { files: ['a.txt'], types: ['Files'] };
    vm.onDragEnter({ dataTransfer: transfer, preventDefault: vi.fn() });
    expect(vm.dropActive.value).toBe(true);
    expect(vm.dropDepth.value).toBe(1);

    registeredNativeDropHandler({ files: ['/tmp/ignore.txt'], details: { id: 'sidebar-drop' } });
    expect(props.composer.attachByPaths).not.toHaveBeenCalled();

    registeredNativeDropHandler({ files: ['/tmp/keep.txt'], details: { id: 'chat-input-bar' } });
    expect(props.composer.attachByPaths).toHaveBeenCalledWith(['/tmp/keep.txt'], 'wails-drop');
    expect(vm.dropActive.value).toBe(false);
    expect(vm.dropDepth.value).toBe(0);
  });

  it('skips drag-drop and native drop handlers while disabled', async () => {
    const { vm, props } = createComposerBar({ disabled: true });
    const transfer = { files: ['a.txt'], types: ['Files'], dropEffect: 'none' };
    const preventDefault = vi.fn();

    vm.onDragEnter({ dataTransfer: transfer, preventDefault });
    vm.onDragOver({ dataTransfer: transfer, preventDefault });
    await vm.onDrop({ dataTransfer: transfer, preventDefault });
    registeredNativeDropHandler({ files: ['/tmp/keep.txt'], details: { id: 'chat-input-bar' } });

    expect(preventDefault).toHaveBeenCalledTimes(1);
    expect(vm.dropActive.value).toBe(false);
    expect(vm.dropDepth.value).toBe(0);
    expect(props.composer.handleDrop).not.toHaveBeenCalled();
    expect(props.composer.attachByPaths).not.toHaveBeenCalled();
  });

  it('tracks nested drag leave transitions and ignores non-file drag payloads', () => {
    const { vm } = createComposerBar();
    const fileTransfer = { files: ['a.txt'], types: ['Files'] };
    const leavePreventDefault = vi.fn();

    vm.onDragEnter({ dataTransfer: fileTransfer, preventDefault: vi.fn() });
    vm.onDragEnter({ dataTransfer: fileTransfer, preventDefault: vi.fn() });
    expect(vm.dropDepth.value).toBe(2);
    expect(vm.dropActive.value).toBe(true);

    vm.onDragLeave({ preventDefault: leavePreventDefault });
    expect(vm.dropDepth.value).toBe(1);
    expect(vm.dropActive.value).toBe(true);

    vm.onDragLeave({ preventDefault: leavePreventDefault });
    expect(vm.dropDepth.value).toBe(0);
    expect(vm.dropActive.value).toBe(false);
    expect(leavePreventDefault).toHaveBeenCalledTimes(2);

    const nonFilePreventDefault = vi.fn();
    vm.onDragEnter({ dataTransfer: { files: [], types: ['text/plain'] }, preventDefault: nonFilePreventDefault });
    expect(nonFilePreventDefault).not.toHaveBeenCalled();
    expect(vm.dropActive.value).toBe(false);
    expect(vm.dropDepth.value).toBe(0);
  });

  it('derives compact tone class', () => {
    const { vm } = createComposerBar({
      compactResultTone: 'success',
      compactResultText: '压缩完成',
    });

    expect(vm.compactResultToneClass()).toBe('is-success');
  });

  it('auto grows composer textarea and clamps overflow', () => {
    const textarea = { style: {}, scrollHeight: 512 };

    expect(applyComposerTextareaAutoHeight(textarea, 240)).toBe(240);
    expect(textarea.style.height).toBe('240px');
    expect(textarea.style.overflowY).toBe('auto');

    textarea.scrollHeight = 88;
    expect(applyComposerTextareaAutoHeight(textarea, 240)).toBe(88);
    expect(textarea.style.height).toBe('88px');
    expect(textarea.style.overflowY).toBe('hidden');
  });

  it('syncs textarea height from input events', () => {
    const { vm } = createComposerBar({ text: '**bold**' });
    const textarea = { style: {}, scrollHeight: 92 };

    vm.setComposerInputRef(textarea);
    vm.onInput();

    expect(textarea.style.height).toBe('92px');
    expect(textarea.style.overflowY).toBe('hidden');
  });

  it('uses available top space instead of a fixed max height', () => {
    const { vm } = createComposerBar({ text: 'long text' });
    const boundary = {
      getBoundingClientRect: () => ({ top: 120 }),
    };
    const textarea = {
      style: {},
      scrollHeight: 800,
      getBoundingClientRect: () => ({ bottom: 700 }),
      closest: () => boundary,
    };

    vm.setComposerInputRef(textarea);
    vm.onInput();

    expect(textarea.style.height).toBe('388px');
    expect(textarea.style.overflowY).toBe('auto');
  });

  it('preserves readable chat space when long input reaches the workspace boundary', () => {
    const { vm } = createComposerBar({ text: 'very long text' });
    const boundary = {
      getBoundingClientRect: () => ({ top: 120 }),
    };
    const textarea = {
      style: {},
      scrollHeight: 1400,
      getBoundingClientRect: () => ({ bottom: 700 }),
      closest: () => boundary,
    };

    vm.setComposerInputRef(textarea);
    vm.onInput();

    expect(textarea.style.height).toBe('388px');
    expect(textarea.style.overflowY).toBe('auto');
  });

  it('caps long input to half of the application height', () => {
    const { vm } = createComposerBar({ text: 'very long text in a tall workspace' });
    const boundary = {
      getBoundingClientRect: () => ({ top: 20 }),
    };
    const textarea = {
      style: {},
      scrollHeight: 1400,
      getBoundingClientRect: () => ({ bottom: 900 }),
      closest: () => boundary,
    };

    vm.setComposerInputRef(textarea);
    vm.onInput();

    expect(textarea.style.height).toBe('500px');
    expect(textarea.style.overflowY).toBe('auto');
  });

  it('reserves readable chat space after composer chrome outside the textarea', () => {
    const { vm } = createComposerBar({ text: 'very long text with attachments' });
    const boundary = {
      getBoundingClientRect: () => ({ top: 100 }),
    };
    const composerShell = {
      getBoundingClientRect: () => ({ top: 420, bottom: 700, height: 280 }),
    };
    const textarea = {
      style: {},
      scrollHeight: 1400,
      getBoundingClientRect: () => ({ top: 620, bottom: 700, height: 80 }),
      closest: (selector) => {
        if (selector === '.chat-composer-shell') return composerShell;
        return boundary;
      },
    };

    vm.setComposerInputRef(textarea);
    vm.onInput();

    expect(textarea.style.height).toBe('208px');
    expect(textarea.style.overflowY).toBe('auto');
  });

  it('falls back to the minimum input height when chrome leaves little workspace room', () => {
    const { vm } = createComposerBar({ text: 'very long text in a short window' });
    const boundary = {
      getBoundingClientRect: () => ({ top: 520 }),
    };
    const composerShell = {
      getBoundingClientRect: () => ({ top: 640, bottom: 700, height: 60 }),
    };
    const textarea = {
      style: {},
      scrollHeight: 1200,
      getBoundingClientRect: () => ({ top: 660, bottom: 700, height: 40 }),
      closest: (selector) => {
        if (selector === '.chat-composer-shell') return composerShell;
        return boundary;
      },
    };

    vm.setComposerInputRef(textarea);
    vm.onInput();

    expect(textarea.style.height).toBe('38px');
    expect(textarea.style.overflowY).toBe('auto');
  });

  it('preserves thread config composable contract before setup extraction', async () => {
    const inheritedEmit = vi.fn();
    const inheritedVm = useComposerThreadConfig({
      isCmd: false,
      threadId: 'thread-1',
      threadConfigProvider: 'codex',
      threadConfigSupportsOverride: true,
      threadConfigMeta: {
        effective: { model: 'openai/gpt-5', effort: 'high' },
        override: {},
      },
    }, inheritedEmit);

    expect(inheritedVm.threadConfigVisible.value).toBe(true);
    expect(inheritedVm.threadConfigEditable.value).toBe(true);
    expect(inheritedVm.threadConfigInherited.value).toBe(true);
    expect(inheritedVm.threadConfigSummaryLabel.value).toBe('openai/gpt-5 · high');

    inheritedVm.toggleThreadConfig();
    expect(inheritedVm.threadConfigOpen.value).toBe(true);
    expect(inheritedVm.threadConfigDropdownStyle.value).toEqual(expect.objectContaining({
      position: 'absolute',
      minWidth: '240px',
    }));

    inheritedVm.onThreadConfigClickOutside({ target: { tagName: 'OPTION' } });
    expect(inheritedVm.threadConfigOpen.value).toBe(true);
    inheritedVm.onThreadConfigClickOutside({ target: { tagName: 'DIV' } });
    expect(inheritedVm.threadConfigOpen.value).toBe(false);

    inheritedVm.saveThreadConfig();
    inheritedVm.onModelSelectChange('openai/gpt-5-mini');
    inheritedVm.onEffortSelectChange('low');
    await flushPromises();

    expect(inheritedEmit).toHaveBeenCalledWith('update-thread-config-model', 'openai/gpt-5-mini');
    expect(inheritedEmit).toHaveBeenCalledWith('update-thread-config-effort', 'low');
    expect(inheritedEmit.mock.calls.filter(([name]) => name === 'save-thread-config')).toHaveLength(3);

    const overrideEmit = vi.fn();
    const overrideVm = useComposerThreadConfig({
      isCmd: false,
      threadId: 'thread-2',
      threadConfigProvider: 'codex',
      threadConfigSupportsOverride: true,
      threadConfigMeta: {
        effective: { model: 'openai/gpt-5', effort: 'high' },
        override: { model: 'custom-model', effort: 'medium' },
      },
    }, overrideEmit);

    overrideVm.toggleThreadConfig();
    overrideVm.restoreThreadConfig();
    expect(overrideVm.threadConfigInherited.value).toBe(false);
    expect(overrideVm.threadConfigSummaryLabel.value).toBe('custom-model · medium');
    expect(overrideEmit).toHaveBeenCalledWith('restore-thread-config-inherit');
    expect(overrideVm.threadConfigOpen.value).toBe(false);
  });

  it('keeps inherited full-model values visible even when they are outside the shortlist', () => {
    const vm = useComposerThreadConfig({
      isCmd: false,
      threadId: 'thread-claude',
      threadConfigProvider: 'claude',
      threadConfigSupportsOverride: true,
      threadConfigMeta: {
        effective: { model: 'claude-sonnet-4-6-20260401', effort: 'high' },
        override: { model: '', effort: '' },
      },
    }, vi.fn());

    expect(vm.threadConfigInherited.value).toBe(true);
    expect(vm.threadConfigModelOptions.value.at(-1)).toEqual({
      value: 'claude-sonnet-4-6-20260401',
      label: 'claude-sonnet-4-6-20260401',
    });
    expect(vm.threadConfigInheritModelLabel.value).toContain('claude-sonnet-4-6-20260401');
    expect(vm.threadConfigInheritEffortLabel.value).toContain('high');
  });

  it('exposes thread config notice text in the composer area', () => {
    const { vm } = createComposerBar({
      threadId: 'thread-1',
      threadConfigProvider: 'claude',
      threadConfigSupportsOverride: true,
      threadConfigNotice: '某个通知消息',
      threadConfigNoticeLevel: 'info',
      threadConfigMeta: {
        override: { model: 'sonnet', effort: 'high' },
        effective: { model: 'sonnet', effort: 'high' },
      },
    });

    expect(vm.threadConfigInlineNotice.value).toBe('某个通知消息');
    expect(ComposerBar.template).toContain('composer-thread-config-notice');
  });

  it('preserves textarea composable contract before setup extraction', async () => {
    const vm = useComposerTextarea();
    const boundary = {
      getBoundingClientRect: () => ({ top: 120 }),
    };
    const textarea = {
      style: {},
      scrollHeight: 800,
      getBoundingClientRect: () => ({ bottom: 700 }),
      closest: () => boundary,
    };

    vm.setComposerInputRef(textarea);
    await flushPromises();
    expect(textarea.style.height).toBe('388px');
    expect(textarea.style.overflowY).toBe('auto');

    vm.onCompositionStart();
    expect(vm.isComposing.value).toBe(true);
    vm.onCompositionEnd();
    expect(vm.isComposing.value).toBe(false);

    textarea.scrollHeight = 92;
    vm.onInput();
    expect(textarea.style.height).toBe('92px');
    expect(textarea.style.overflowY).toBe('hidden');
  });

  it('resets pause acknowledgement during updated when ready input returns', () => {
    const { vm } = createComposerBar({ canSend: true });
    const textarea = { style: {}, scrollHeight: 92 };
    vm.setComposerInputRef(textarea);
    vm.pauseAcknowledged.value = true;

    expect(lifecycleState.updated).toHaveLength(1);
    lifecycleState.updated[0]();

    expect(vm.pauseAcknowledged.value).toBe(false);
    expect(textarea.style.height).toBe('92px');
  });

  it('resets interrupt and drag state when thread id changes', () => {
    const { vm } = createComposerBar({ interruptible: true, canSend: false, threadId: 'thread-1' });
    const transfer = { files: ['a.txt'], types: ['Files'] };

    vm.onCompositionStart();
    vm.pauseAcknowledged.value = true;
    vm.interruptPending.value = true;
    vm.interruptRequestThreadId.value = 'thread-1';
    vm.interruptTimeoutId.value = 99;
    vm.onDragEnter({ dataTransfer: transfer, preventDefault: vi.fn() });

    const threadWatch = lifecycleState.watches[0];
    threadWatch.callback('thread-2', 'thread-1');

    expect(vm.isComposing.value).toBe(false);
    expect(vm.pauseAcknowledged.value).toBe(false);
    expect(vm.interruptPending.value).toBe(false);
    expect(vm.interruptRequestThreadId.value).toBe('');
    expect(vm.dropActive.value).toBe(false);
    expect(vm.dropDepth.value).toBe(0);
    expect(window.clearTimeout).toHaveBeenCalled();
  });

  it('cleans up native drop handlers and DOM listeners on unmount', () => {
    const { vm } = createComposerBar({ interruptible: true, canSend: false });
    const transfer = { files: ['a.txt'], types: ['Files'] };

    vm.onPrimaryAction({ type: 'click' });
    vm.onDragEnter({ dataTransfer: transfer, preventDefault: vi.fn() });

    expect(lifecycleState.beforeUnmount).toHaveLength(1);
    lifecycleState.beforeUnmount[0]();

    expect(registeredNativeDropDispose).toHaveBeenCalledTimes(1);
    expect(window.removeEventListener).toHaveBeenCalledWith('resize', expect.any(Function));
    expect(document.removeEventListener).toHaveBeenCalledWith('click', expect.any(Function), true);
    expect(window.clearTimeout).toHaveBeenCalledWith(1);
    expect(vm.dropActive.value).toBe(false);
    expect(vm.dropDepth.value).toBe(0);
  });

  it('preserves exposed setup surface and template action bindings', () => {
    const { vm } = createComposerBar();

    expect(vm).toEqual(expect.objectContaining({
      onPrimaryAction: expect.any(Function),
      onEscape: expect.any(Function),
      onSend: expect.any(Function),
      onDragEnter: expect.any(Function),
      onDrop: expect.any(Function),
      pauseAcknowledged: expect.anything(),
      dropActive: expect.anything(),
      threadConfigOpen: expect.anything(),
      threadConfigSummaryLabel: expect.anything(),
      threadConfigInlineNotice: expect.anything(),
    }));
    expect(ComposerBar.template).toContain('@keydown.esc.exact="onEscape"');
    expect(ComposerBar.template).toContain('@click="onPrimaryAction"');
    expect(ComposerBar.template).toContain('@click="onCompact"');
    expect(ComposerBar.template).toContain('@click="onAttach"');
    expect(ComposerBar.template).toContain('@click="onRemoveAttachment(idx)"');
  });

  it('binds textarea ref as a callback ref in the template', () => {
    expect(ComposerBar.template).toContain(':ref="setComposerInputRef"');
  });

  // Phase 2.2a · 配置面板加「升级为自动化任务」单向按钮
  it('declares promote-task in emits + threadIsTask / promotingTask / threadTaskId props', () => {
    expect(ComposerBar.emits).toContain('promote-task');
    expect(ComposerBar.props.threadIsTask.default).toBe(false);
    expect(ComposerBar.props.promotingTask.default).toBe(false);
    expect(ComposerBar.props.threadTaskId.default).toBe('');
  });

  it('template renders dropdown already-task row but no longer the duplicate promote button', () => {
    // Phase 2.4 去重：dropdown 内「升级为自动化任务」按钮跟主条 chip 完全重复，
    // 已删除；dropdown 只保留「状态查看」（已是任务 / 升级失败）这些不重复的信息。
    expect(ComposerBar.template).not.toContain('thread-config-promote-btn');
    expect(ComposerBar.template).toContain('thread-config-promote-already');
    expect(ComposerBar.template).toContain('v-if="threadIsTask"');
    // 主条 chip 仍然是 onPromoteTask 唯一调用点
    expect(ComposerBar.template).toContain('@click="onPromoteTask"');
  });

  it('onPromoteTask emits when not already a task and not in flight', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ threadIsTask: false, promotingTask: false }, emit);
    vm.onPromoteTask();
    expect(emit).toHaveBeenCalledWith('promote-task');
  });

  it('onPromoteTask does NOT emit when threadIsTask=true', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ threadIsTask: true }, emit);
    vm.onPromoteTask();
    const calls = emit.mock.calls.filter(([name]) => name === 'promote-task');
    expect(calls).toHaveLength(0);
  });

  it('onPromoteTask does NOT emit when promotingTask=true (in-flight)', () => {
    const emit = vi.fn();
    const { vm } = createComposerBar({ promotingTask: true }, emit);
    vm.onPromoteTask();
    const calls = emit.mock.calls.filter(([name]) => name === 'promote-task');
    expect(calls).toHaveLength(0);
  });

  it('declares promoteTaskError prop with empty default', () => {
    expect(ComposerBar.props.promoteTaskError.default).toBe('');
  });

  it('template renders promote error row with proper testid', () => {
    // Phase 2.4 去重后：error span 跟「已是任务」在同一 section 里互斥（v-else-if），
    // 主要为了 dropdown 空状态时整个 section 隐藏。
    expect(ComposerBar.template).toContain('thread-config-promote-error');
    expect(ComposerBar.template).toContain('v-else-if="promoteTaskError"');
  });

  // ComposerBar 主条独立 chip 入口（解决 Phase 2.2a dropdown 内入口
  // discoverability 差的问题）：跟时钟图标按钮同级，普通对话直接可见。
  it('template renders main-bar composer-promote-chip alongside thread-config-btn', () => {
    expect(ComposerBar.template).toContain('composer-promote-chip');
    // Phase 2.4 文案从「作为任务运行」（被动陈述）改为「转为自动任务」（主动动作），
    // 同时 chip 加上蓝色 outlined-primary 视觉跟 token chip / 模型 chip 区开。
    expect(ComposerBar.template).toContain('转为自动任务');
    // 必须共用 onPromoteTask，避免双入口逻辑分叉。
    const chipBlock = ComposerBar.template.split('data-testid="composer-promote-chip"')[1] || '';
    expect(chipBlock.split('</button>')[0]).toContain('@click="onPromoteTask"');
  });

  it('main-bar promote chip is gated by !isCmd && threadId && !threadIsTask', () => {
    // 普通对话（非命令模式 + 有 threadId + 还没升级）才显示；其他三种状态隐藏，
    // 避免在不该出现的场景把 UI 撑乱（命令模式 / 空 thread / 已是任务）。
    expect(ComposerBar.template).toContain('v-if="!isCmd && threadId && !threadIsTask"');
  });

  it('main-bar promote chip disabled bound to promotingTask', () => {
    // 跟 dropdown 内按钮同样靠 promotingTask 防双击，busy 时按钮 disable + 文案改「升级中…」。
    const chipBlock = ComposerBar.template.split('data-testid="composer-promote-chip"')[1] || '';
    const upTo = chipBlock.split('</button>')[0];
    expect(upTo).toContain(':disabled="promotingTask"');
    expect(upTo).toContain("promotingTask ? '升级中…'");
  });
});
