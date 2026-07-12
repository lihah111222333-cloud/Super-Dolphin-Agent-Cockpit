import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useRef, useState } from 'react';
import { expect, it, vi } from 'vitest';
import { SlashCommandPalette } from '../components/SlashCommandPalette.jsx';
import { useSlashCommandPalette } from './useSlashCommandPalette.js';

const copy = {
  ariaLabel: '命令与能力',
  categories: {
    builtin: '快捷命令',
    skill: '技能',
    prompt: '提示词',
    automation: '自动化',
    mcp_tool: 'MCP 工具',
  },
  loading: '正在加载',
  loadError: '加载失败',
  projectRequired: '请先选择项目',
  noResults: '没有匹配的命令',
  selecting: '正在应用命令',
  builtins: {
    newLabel: '新建对话',
    newDescription: '在当前项目新建对话',
    clearLabel: '清空输入',
    clearDescription: '清空文字、附件与能力',
  },
};

const reviewCapability = {
  kind: 'skill',
  key: 'skill:project::review:/repo/.agents/skills/review',
  name: 'review',
  label: 'Code Review',
  ref: {
    name: 'review',
    scope: 'project',
    personalType: '',
    path: '/repo/.agents/skills/review',
  },
};

const skillItem = {
  id: reviewCapability.key,
  kind: 'skill',
  name: 'review',
  label: 'Code Review',
  description: 'Review a change',
  keywords: ['review'],
  payload: { capability: reviewCapability },
  disabled: false,
  disabledReason: '',
};

const promptItem = {
  id: 'prompt:prompt',
  kind: 'prompt',
  name: 'prompt',
  label: 'Prompt template',
  description: 'Load a prompt body',
  keywords: ['prompt'],
  payload: { promptId: 'prompt' },
  disabled: false,
  disabledReason: '',
};

const automationItem = {
  id: 'automation:release',
  kind: 'automation',
  name: 'release',
  label: 'Release',
  description: 'Prepare a release',
  keywords: ['release'],
  payload: { title: 'Release', content: 'Run checks' },
  disabled: false,
  disabledReason: '',
};

const toolCapability = {
  kind: 'mcp_tool',
  key: 'mcp_tool:lsp:lsp_edit',
  name: 'lsp_edit',
  label: 'MCP Edit',
  serverName: 'lsp',
};

const toolItem = {
  id: toolCapability.key,
  kind: 'mcp_tool',
  name: 'lsp_edit',
  label: 'MCP Edit',
  description: 'Edit through LSP',
  keywords: ['lsp'],
  payload: { capability: toolCapability },
  disabled: false,
  disabledReason: '',
};

function createService() {
  return {
    loadSkills: vi.fn().mockResolvedValue([skillItem]),
    loadPrompts: vi.fn().mockResolvedValue([promptItem]),
    loadAutomations: vi.fn().mockResolvedValue([automationItem]),
    loadMCPTools: vi.fn().mockResolvedValue([toolItem]),
    loadPromptContent: vi.fn().mockResolvedValue('Loaded prompt body'),
  };
}

function createStore(overrides = {}) {
  return {
    composerCapabilities: [],
    addComposerCapability: vi.fn(),
    reconcileComposerCapabilities: vi.fn(),
    clearComposer: vi.fn(),
    newThread: vi.fn(),
    notifyAction: vi.fn(),
    ...overrides,
  };
}

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, gcTime: Infinity },
      mutations: { retry: false },
    },
  });
}

function PaletteHarness(props) {
  const {
    cwd = '/repo',
    initialDraft = '',
    service,
    store,
    setDraftSpy,
  } = props;
  const [draft, setDraftState] = useState(initialDraft);
  const textareaRef = useRef(null);
  const setDraft = (nextDraft) => {
    setDraftSpy(nextDraft);
    setDraftState(nextDraft);
  };
  const palette = useSlashCommandPalette({
    copy,
    cwd,
    draft,
    service,
    setDraft,
    store,
    textareaRef,
  });

  return (
    <>
      <textarea
        ref={textareaRef}
        aria-label="输入"
        value={draft}
        onChange={(event) => setDraft(event.target.value)}
        onKeyDown={(event) => palette.handleKeyDown(event, {
          isComposing: event.nativeEvent.isComposing,
        })}
      />
      <SlashCommandPalette {...palette} copy={copy} cwd={cwd} />
    </>
  );
}

function renderHarness(options = {}) {
  const service = options.service instanceof Object ? options.service : createService();
  const store = options.store instanceof Object ? options.store : createStore();
  const setDraftSpy = vi.fn();
  render(
    <QueryClientProvider client={createQueryClient()}>
      <PaletteHarness
        cwd={options.cwd}
        initialDraft={options.initialDraft}
        service={service}
        store={store}
        setDraftSpy={setDraftSpy}
      />
    </QueryClientProvider>,
  );
  return { service, setDraftSpy, store, textarea: screen.getByRole('textbox', { name: '输入' }) };
}

it('opens the catalog for a slash trigger and binds a selected Skill', async () => {
  const { setDraftSpy, store, textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/rev' } });
  expect(await screen.findByRole('listbox', { name: '命令与能力' })).toBeVisible();
  await screen.findByRole('option', { name: /Code Review/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });

  await waitFor(() => expect(store.addComposerCapability).toHaveBeenCalledWith(
    expect.objectContaining({ kind: 'skill', name: 'review' }),
  ));
  expect(setDraftSpy).toHaveBeenCalledWith('');
});

it.each(['Enter', 'Tab'])('selects the active option with %s', async (key) => {
  const { store, textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/rev' } });
  await screen.findByRole('option', { name: /Code Review/ });
  fireEvent.keyDown(textarea, { key });

  await waitFor(() => expect(store.addComposerCapability).toHaveBeenCalledTimes(1));
});

it('resets the active option when a new slash query opens', async () => {
  const { store, textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/rev' } });
  await screen.findByRole('option', { name: /Code Review/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });
  await waitFor(() => expect(store.addComposerCapability).toHaveBeenCalledTimes(1));

  fireEvent.change(textarea, { target: { value: '/' } });
  const newThread = await screen.findByRole('option', { name: /新建对话/ });
  expect(newThread).toHaveAttribute('aria-selected', 'true');
  fireEvent.keyDown(textarea, { key: 'Enter' });

  expect(store.newThread).toHaveBeenCalledTimes(1);
  expect(store.addComposerCapability).toHaveBeenCalledTimes(1);
});

it('dismisses with Escape without mutating the trigger', async () => {
  const { setDraftSpy, textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/rev' } });
  expect(await screen.findByRole('listbox')).toBeVisible();
  fireEvent.keyDown(textarea, { key: 'Escape' });

  expect(screen.queryByRole('listbox')).not.toBeInTheDocument();
  expect(setDraftSpy).not.toHaveBeenCalledWith('');
  expect(textarea).toHaveValue('/rev');
});

it.each([
  { isComposing: true, keyCode: 13 },
  { isComposing: false, keyCode: 229 },
])('does not consume IME key events: %o', async ({ isComposing, keyCode }) => {
  const { store, textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/rev' } });
  await screen.findByRole('option', { name: /Code Review/ });
  fireEvent.keyDown(textarea, { key: 'Enter', keyCode, isComposing });

  expect(store.addComposerCapability).not.toHaveBeenCalled();
});

it('loads prompt content before replacing the trigger', async () => {
  const { service, setDraftSpy, textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/prompt' } });
  await screen.findByRole('option', { name: /Prompt template/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });

  await waitFor(() => expect(service.loadPromptContent).toHaveBeenCalledWith('/repo', 'prompt'));
  await waitFor(() => expect(setDraftSpy).toHaveBeenCalledWith('Loaded prompt body'));
});

it('preserves the prompt trigger and reports a lazy-load failure', async () => {
  const service = createService();
  service.loadPromptContent.mockRejectedValueOnce(new Error('prompt unavailable'));
  const { setDraftSpy, store, textarea } = renderHarness({ service });

  fireEvent.change(textarea, { target: { value: '/prompt' } });
  await screen.findByRole('option', { name: /Prompt template/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });

  await waitFor(() => expect(store.notifyAction).toHaveBeenCalledWith(
    expect.stringContaining('prompt unavailable'),
    'error',
  ));
  expect(setDraftSpy).not.toHaveBeenCalledWith('');
  expect(textarea).toHaveValue('/prompt');
});

it('applies built-ins, automation text, and canonical MCP capability semantics', async () => {
  const { setDraftSpy, store, textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/new' } });
  await screen.findByRole('option', { name: /新建对话/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });
  expect(store.newThread).toHaveBeenCalledTimes(1);

  fireEvent.change(textarea, { target: { value: '/clear' } });
  await screen.findByRole('option', { name: /清空输入/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });
  expect(store.clearComposer).toHaveBeenCalledTimes(1);

  fireEvent.change(textarea, { target: { value: '/release' } });
  await screen.findByRole('option', { name: /Release/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });
  expect(setDraftSpy).toHaveBeenCalledWith('Release\n\nRun checks');

  fireEvent.change(textarea, { target: { value: '/lsp' } });
  await screen.findByRole('option', { name: /MCP Edit/ });
  fireEvent.keyDown(textarea, { key: 'Enter' });
  expect(store.addComposerCapability).toHaveBeenLastCalledWith(
    expect.objectContaining({ kind: 'mcp_tool', name: 'lsp_edit' }),
  );
});

it('wraps keyboard navigation across enabled options', async () => {
  const { textarea } = renderHarness();

  fireEvent.change(textarea, { target: { value: '/' } });
  const last = await screen.findByRole('option', { name: /MCP Edit/ });
  const first = screen.getByRole('option', { name: /新建对话/ });
  fireEvent.keyDown(textarea, { key: 'ArrowUp' });
  expect(last).toHaveAttribute('aria-selected', 'true');
  fireEvent.keyDown(textarea, { key: 'ArrowDown' });
  expect(first).toHaveAttribute('aria-selected', 'true');
  fireEvent.keyDown(textarea, { key: 'ArrowUp' });
  expect(last).toHaveAttribute('aria-selected', 'true');
});

it('reconciles selected capability categories even while the palette is closed', async () => {
  const store = createStore({ composerCapabilities: [{ ...reviewCapability, availability: 'unverified' }] });
  const { service } = renderHarness({ initialDraft: 'ordinary text', store });

  await waitFor(() => expect(service.loadSkills).toHaveBeenCalledWith('/repo'));
  await waitFor(() => expect(store.reconcileComposerCapabilities).toHaveBeenCalledWith({
    kind: 'skill',
    status: 'success',
    items: [skillItem],
  }));
});
