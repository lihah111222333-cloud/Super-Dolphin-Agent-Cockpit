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

import { EFFORT_MODES_BY_PROVIDER, MODEL_OPTIONS_BY_PROVIDER } from './provider-config-options.js';
import { ComposerBar } from './components/ComposerBar.js';
import { ChatToolbar, resolveProviderToggleLabel } from './components/unified-chat/ChatToolbar.js';
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
  const emit = vi.fn();
  const vm = ComposerBar.setup({
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
      effective: { model: 'gpt-5.5', effort: 'xhigh' },
    },
    ...overrides,
  }, { emit });
  return { vm, emit };
}

describe('ComposerBar thread config behavior', () => {
  it('reuses shared option sources for editable codex threads', () => {
    const { vm } = createComposerVm({
      threadConfigMeta: {
        override: { model: 'gpt-5.2', effort: 'high' },
        effective: { model: 'gpt-5.2', effort: 'high' },
      },
    });

    expect(vm.threadConfigVisible.value).toBe(true);
    expect(vm.threadConfigEditable.value).toBe(true);
    expect(vm.threadConfigModelOptions.value).toBe(MODEL_OPTIONS_BY_PROVIDER.codex);
    expect(vm.threadConfigEffortOptions.value).toBe(EFFORT_MODES_BY_PROVIDER.codex);
  });

  it('shows provider-aware claude options and hides max on non-opus models', () => {
    const { vm } = createComposerVm({
      threadConfigProvider: 'claude',
      threadConfigSupportsOverride: true,
      threadConfigDraftModel: 'sonnet',
      threadConfigMeta: {
        override: { model: '', effort: '' },
        effective: { model: 'sonnet', effort: 'high' },
      },
    });

    expect(vm.threadConfigVisible.value).toBe(true);
    expect(vm.threadConfigEditable.value).toBe(true);
    expect(vm.threadConfigModelOptions.value).toBe(MODEL_OPTIONS_BY_PROVIDER.claude);
    expect(vm.threadConfigEffortOptions.value.map((item) => item.value)).toEqual(['high', 'medium', 'low']);
  });

  it('normalizes claude max effort to high before auto-saving a non-opus model', async () => {
    const { vm, emit } = createComposerVm({
      threadConfigProvider: 'claude',
      threadConfigSupportsOverride: true,
      threadConfigDraftModel: 'best',
      threadConfigDraftEffort: 'max',
      threadConfigMeta: {
        override: { model: 'best', effort: 'max' },
        effective: { model: 'best', effort: 'max' },
      },
    });

    vm.onModelSelectChange('sonnet');
    await Promise.resolve();

    expect(emit).toHaveBeenCalledWith('update-thread-config-model', 'sonnet');
    expect(emit).toHaveBeenCalledWith('update-thread-config-effort', 'high');
    expect(emit).toHaveBeenCalledWith('save-thread-config');
  });
});

describe('ChatToolbar provider label behavior', () => {
  it('keeps the effective provider name while provider preferences are loading', () => {
    expect(resolveProviderToggleLabel(false)).toBe('Codex');
    expect(resolveProviderToggleLabel(true)).toBe('Claude');
  });

  it('keeps the provider toggle usable while preference loading is pending', () => {
    expect(ChatToolbar.template).toContain('class="provider-toggle-input"');
    expect(ChatToolbar.template).not.toContain(':disabled="!providerPreferenceReady"');
  });
});
