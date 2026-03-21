// @ts-nocheck
import { beforeEach, describe, expect, it, vi } from 'vitest';

beforeEach(() => {
  globalThis.window = {
    ...(globalThis.window || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    setTimeout: vi.fn(() => 1),
    clearTimeout: vi.fn(),
    innerHeight: 800,
  };
  globalThis.document = {
    ...(globalThis.document || {}),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    querySelector: vi.fn(() => null),
    activeElement: null,
  };
});

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return {
    ...actual,
    onMounted: () => {},
    onUpdated: () => {},
    onBeforeUnmount: () => {},
  };
});

vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({})),
  copyTextToClipboard: vi.fn(async () => true),
  onFilesDropped: vi.fn(() => () => {}),
  resolveThreadIdentity: vi.fn(async () => ({})),
}));

vi.mock('./services/log.js', () => ({
  logDebug: vi.fn(),
  logInfo: vi.fn(),
  logWarn: vi.fn(),
}));

import { EFFORT_MODES, MODEL_OPTIONS } from './provider-config-options.js';
import { ComposerBar } from './components/ComposerBar.js';
import { reactive, ref } from '../lib/vue.esm-browser.prod.js';

function makeComposer() {
  return {
    state: reactive({ text: '', attachments: [], attaching: false }),
    canSend: ref(false),
    handlePaste: vi.fn(),
    handleDrop: vi.fn(),
    attachByPaths: vi.fn(() => 0),
    attachByPicker: vi.fn(),
    removeAttachment: vi.fn(),
  };
}

function createComposerVm(overrides = {}) {
  return ComposerBar.setup({
    composer: makeComposer(),
    disabled: false,
    threadId: 'thread-live',
    interruptible: false,
    compacting: false,
    compactResultText: '',
    compactResultTone: '',
    compactSuccessCount: 0,
    tokenInline: '',
    tokenTooltip: '',
    skillMatches: [],
    skillMatchesLoading: false,
    selectedSkillNames: [],
    isCmd: false,
    threadConfigProvider: 'codex',
    threadConfigSupportsOverride: true,
    threadConfigDraftModel: '',
    threadConfigDraftEffort: '',
    threadConfigLoading: false,
    threadConfigSaving: false,
    threadConfigMeta: {
      override: { model: '', effort: '' },
      effective: { model: 'gpt-5.4', effort: 'xhigh' },
    },
    ...overrides,
  }, { emit: vi.fn() });
}

describe('ComposerBar thread config behavior', () => {
  it('reuses shared option sources for editable codex threads', () => {
    const vm = createComposerVm({
      threadConfigMeta: {
        override: { model: 'gpt-5.2', effort: 'high' },
        effective: { model: 'gpt-5.2', effort: 'high' },
      },
    });

    expect(vm.threadConfigVisible.value).toBe(true);
    expect(vm.threadConfigEditable.value).toBe(true);
    expect(vm.threadConfigModelOptions).toBe(MODEL_OPTIONS);
    expect(vm.threadConfigEffortOptions).toBe(EFFORT_MODES);
  });

  it('marks claude threads as not visible when provider is claude', () => {
    const vm = createComposerVm({
      threadConfigProvider: 'claude',
      threadConfigSupportsOverride: false,
      threadConfigMeta: {
        override: { model: '', effort: '' },
        effective: { model: 'claude-3.7-sonnet', effort: 'high' },
      },
    });

    // ComposerBar only shows config for codex provider threads
    expect(vm.threadConfigVisible.value).toBe(false);
    expect(vm.threadConfigEditable.value).toBe(false);
  });
});
