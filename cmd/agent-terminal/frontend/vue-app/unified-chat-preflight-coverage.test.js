// @ts-nocheck
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const hooks = vi.hoisted(() => ({ mounted: [], unmounted: [], droppedCleanup: vi.fn() }));
const autoScroll = vi.hoisted(() => ({ schedule: vi.fn() }));
const provider = vi.hoisted(() => ({ load: vi.fn(async () => {}), toggle: vi.fn(async () => {}), useClaude: false }));
const diffMock = vi.hoisted(() => ({ last: null }));
const composer = vi.hoisted(() => {
  const state = { text: '', attachments: [] };
  return {
    state,
    clearComposer: vi.fn(() => { state.text = ''; state.attachments = []; }),
    attachByPaths: vi.fn(() => 0),
  };
});

vi.mock('../lib/vue.esm-browser.prod.js', async () => {
  const actual = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return { ...actual, onMounted: (fn) => hooks.mounted.push(fn), onBeforeUnmount: (fn) => hooks.unmounted.push(fn) };
});
vi.mock('./stores/composer.js', () => ({ useComposerStore: () => composer }));
vi.mock('./services/api.js', () => ({
  callAPI: vi.fn(async () => ({})),
  copyTextToClipboard: vi.fn(async () => true),
  onFilesDropped: vi.fn(() => hooks.droppedCleanup),
  resolveThreadIdentity: vi.fn(async () => ({})),
}));
vi.mock('./services/log.js', () => ({ logDebug: vi.fn(), logInfo: vi.fn(), logWarn: vi.fn() }));
vi.mock('./composables/useAutoScroll.js', () => ({ useAutoScroll: () => ({ scheduleScrollToBottom: autoScroll.schedule }) }));
vi.mock('./composables/useProviderMode.js', async () => {
  const { ref } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return { useProviderMode: () => ({ useClaudeProvider: ref(provider.useClaude), loadProviderPreference: provider.load, toggleProviderMode: provider.toggle }) };
});
vi.mock('./composables/useResizePanels.js', async () => {
  const { ref, computed } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return { useResizePanels: () => ({
    dragging: ref(false), threadRailDragging: ref(false), activityPanelDragging: ref(false), splitRatio: ref(60),
    threadRailStyle: computed(() => ({})), chatComposerShellStyle: computed(() => ({})), activityPanelRowStyle: computed(() => ({})),
    onResizeStart: vi.fn(), onThreadRailResizeStart: vi.fn(), onActivityResizeStart: vi.fn(),
  }) };
});
vi.mock('./composables/useDiffPreview.js', async () => {
  const { ref, computed } = await vi.importActual('../lib/vue.esm-browser.prod.js');
  return { useDiffPreview: (opts) => {
    const focusedDiffPath = ref('');
    const focusedDiffLine = ref(0);
    const pendingFileRefFocus = ref(null);
    const fallbackDiffText = ref('');
    const fallbackMediaPreview = ref(null);
    const fallbackMarkdownPreview = ref(null);
    diffMock.last = { focusedDiffPath, focusedDiffLine, pendingFileRefFocus, fallbackDiffText, fallbackMediaPreview, fallbackMarkdownPreview };
    return {
      focusedDiffPath, focusedDiffLine, pendingFileRefFocus, fallbackDiffText, fallbackMediaPreview, fallbackMarkdownPreview,
      activeMediaPreview: computed(() => fallbackMediaPreview.value),
      activeMarkdownPreview: computed(() => fallbackMarkdownPreview.value),
      activeDiffText: computed(() => fallbackDiffText.value || (opts.activeThreadDiffText.value || '')),
      activeDiffFocusFile: computed(() => focusedDiffPath.value),
      activeDiffFocusLine: computed(() => focusedDiffLine.value),
      timelinePreview: vi.fn(() => []),
      diffPreview: vi.fn(() => ''),
    };
  } };
});

import { nextTick, reactive, ref } from '../lib/vue.esm-browser.prod.js';
import { callAPI, copyTextToClipboard, resolveThreadIdentity } from './services/api.js';
import { PathChoiceModal } from './components/PathChoiceModal.js';
import { UnifiedChatPage } from './pages/UnifiedChatPage.js';

const flush = async () => { await Promise.resolve(); await Promise.resolve(); await nextTick(); };
function createDeferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}
async function runMountedHooks() {
  for (const hook of hooks.mounted.splice(0)) {
    await hook();
  }
}
function makeStores(opts = {}) {
  const current = ref(opts.selectedId ?? 'thread-active');
  const threads = reactive(opts.threads ?? [{ id: 'thread-active', name: 'Active' }, { id: 'thread-2', name: 'Second' }]);
  const map = (value) => reactive(value ?? {});
  const status = map(opts.status); const header = map(opts.header); const details = map(opts.details); const interruptible = map(opts.interruptible);
  const timeline = reactive(opts.timeline ?? { 'thread-active': [], 'thread-2': [] }); const diff = map(opts.diff); const token = map(opts.token);
  let layout = opts.layout ?? 'focus'; let cols = opts.cols ?? 3;
  const store = {
    state: reactive({ pinnedThreadAtById: opts.pinned ?? {}, archivedThreadAtById: opts.archived ?? {}, agentRuntimeById: opts.runtime ?? {}, skillRevision: 0, agentMetaById: opts.meta ?? {}, diffTextByThread: diff }),
    getLayout: vi.fn(() => layout), setLayout: vi.fn((_, v) => { layout = v; }), getCmdCardCols: vi.fn(() => cols), setCmdCardCols: vi.fn((v) => { cols = v; }), getSplitRatio: () => 60, setSplitRatio() {}, getThreadRailWidth: () => 232, setThreadRailWidth() {}, getActivityPanelHeight: () => 220, setActivityPanelHeight() {},
    getCurrentThreadId: vi.fn(() => current.value), saveActiveThread: vi.fn((v) => { current.value = v || ''; }), saveActiveCmdThread: vi.fn((v) => { current.value = v || ''; }),
    getThreadsByMode: vi.fn(() => threads), displayName: vi.fn((thread) => thread?.name || thread?.id || ''), getThreadStatus: vi.fn((id) => status[id] || 'idle'), getThreadStatusHeader: vi.fn((id) => header[id] || ''), getThreadInterruptible: vi.fn((id) => Boolean(interruptible[id])), getThreadTimeline: vi.fn((id) => timeline[id] || []), getThreadDiff: vi.fn((id) => diff[id] || ''), getThreadStatusDetails: vi.fn((id) => details[id] || ''), getThreadTokenUsage: vi.fn((id) => token[id] ?? null), getThreadCompacting: vi.fn(() => false), getThreadCompactResult: vi.fn(() => null), getThreadCompactSuccessCount: vi.fn(() => 0), getThreadActivityStats: vi.fn(() => ({})), getThreadAlerts: vi.fn(() => []),
    startThread: vi.fn(async () => opts.startThreadId ?? 'thread-started'), sendMessage: vi.fn(async () => ({})), stopThread: vi.fn(async () => ({ confirmed: true, settled: true, mode: 'interrupt_confirmed' })), compactThread: vi.fn(async () => ({})), forceCompleteThread: vi.fn(async () => ({})), recoverThread: vi.fn(async () => ({})), loadMessages: vi.fn(async () => ({})),
    renameThread: vi.fn(async (id, name) => { const thread = threads.find((item) => item.id === id); if (thread) thread.name = name; }), promptRenameThread: vi.fn(), toggleThreadPin: vi.fn(), toggleThreadArchive: vi.fn(async () => ({})),
  };
  const projectStore = { state: reactive({ active: opts.active ?? '.', showModal: false, projects: opts.projects ?? ['.'] }), projectOptions: { value: [] }, setActive: vi.fn(), quickAdd: vi.fn(), removeProject: vi.fn() };
  return { store, projectStore, current };
}
const createVm = async (opts = {}) => { const { store, projectStore, current } = makeStores(opts); const vm = UnifiedChatPage.setup({ threadStore: store, projectStore, mode: opts.mode ?? 'chat' }); await flush(); return { vm, store, projectStore, current }; };

beforeEach(() => {
  hooks.mounted.length = 0; hooks.unmounted.length = 0; hooks.droppedCleanup.mockClear(); diffMock.last = null; autoScroll.schedule.mockClear(); provider.load.mockClear(); provider.toggle.mockClear(); provider.useClaude = false;
  composer.state.text = ''; composer.state.attachments = []; composer.clearComposer.mockClear(); composer.attachByPaths.mockClear();
  composer.forkDraft = reactive({ active: false, sharedFilePaths: [], origin: '' });
  composer.openForkDraft = vi.fn((options = {}) => {
    composer.forkDraft.active = true;
    composer.forkDraft.origin = (options?.origin || '').toString().trim();
    const path = (options?.sharedFilePath || '').toString().trim();
    if (path && !composer.forkDraft.sharedFilePaths.includes(path)) composer.forkDraft.sharedFilePaths.push(path);
  });
  composer.closeForkDraft = vi.fn(() => { composer.forkDraft.active = false; composer.forkDraft.sharedFilePaths = []; composer.forkDraft.origin = ''; });
  composer.addForkSharedFile = vi.fn((path) => { const value = (path || '').toString().trim(); if (value) composer.forkDraft.sharedFilePaths.push(value); });
  composer.removeForkSharedFile = vi.fn((path) => { const idx = composer.forkDraft.sharedFilePaths.indexOf(path); if (idx >= 0) composer.forkDraft.sharedFilePaths.splice(idx, 1); });
  vi.mocked(callAPI).mockReset().mockResolvedValue({}); vi.mocked(copyTextToClipboard).mockReset().mockResolvedValue(true); vi.mocked(resolveThreadIdentity).mockReset().mockResolvedValue({});
  try { sessionStorage.removeItem('__plan_dismissed_v2__'); } catch {}
  globalThis.window = { addEventListener: vi.fn(), removeEventListener: vi.fn(), setTimeout: (...args) => setTimeout(...args), clearTimeout: (id) => clearTimeout(id), setInterval: (...args) => setInterval(...args), clearInterval: (id) => clearInterval(id), alert: vi.fn(), confirm: vi.fn(() => true) };
  globalThis.document = { addEventListener: vi.fn(), removeEventListener: vi.fn(), querySelector: vi.fn(() => null), activeElement: null };
});
afterEach(() => { for (const fn of hooks.unmounted.splice(0)) fn(); hooks.mounted.length = 0; vi.useRealTimers(); });

describe('UnifiedChatPage preflight coverage', () => {
  it('covers format helpers through derived status and activity branches', async () => {
    vi.useFakeTimers(); vi.setSystemTime(new Date('2026-03-09T00:00:00Z'));
    const { vm, projectStore } = await createVm({ layout: 'mix', status: { 'thread-active': 'running' }, header: { 'thread-active': '处理中' }, details: { 'thread-active': '执行命令' }, interruptible: { 'thread-active': true }, token: { 'thread-active': { usedTokens: 1234567, contextWindowTokens: 2000000, usedPercent: 140 } }, timeline: { 'thread-active': [{ id: 'think-1', kind: 'thinking', done: false, ts: '2026-03-09T00:00:00Z' }, { id: 'cmd-1', kind: 'command', status: 'failed', command: 'npm test', output: 'x'.repeat(421), ts: 'bad-ts' }] } });
    const items = vm.activeProcessActivity.value; expect(items.find((item) => item.kind === 'command').time).toBe(''); expect(items.find((item) => item.kind === 'command').output.endsWith('...[truncated]')).toBe(true); expect(items.find((item) => item.kind === 'thinking').time).toMatch(/\d{1,2}:\d{2}/);
    expect(vm.activeTokenInline.value).toBe('100% · 1.2m / 2.0m'); expect(vm.activeTokenTooltip.value).toContain('100% used (0% left)'); await vi.advanceTimersByTimeAsync(3661000); expect(vm.activeStatusMeta.value).toContain('1h 01m 01s'); projectStore.state.showModal = true; await flush(); const paused = vm.activeStatusMeta.value; await vi.advanceTimersByTimeAsync(5000); expect(vm.activeStatusMeta.value).toBe(paused); projectStore.state.showModal = false; await flush(); await vi.advanceTimersByTimeAsync(5000); expect(vm.activeStatusMeta.value).toContain('1h 01m 06s');
    const { vm: noLimitVm } = await createVm({ token: { 'thread-active': { usedTokens: 999, contextWindowTokens: 0 } } }); expect(noLimitVm.activeTokenInline.value).toBe('999'); expect(noLimitVm.activeTokenTooltip.value).toContain('999 tokens used');
  });

  it('covers plan key and card derivation branches', async () => {
    const { vm: idVm } = await createVm({ timeline: { 'thread-active': [{ id: 'plan-1', kind: 'plan', done: true, text: '已完成计划' }] } }); expect(idVm.activePinnedPlan.value.key).toBe('id:plan-1');
    const specText = '先执行\n```json-render\n{"type":"Text","text":"已解析"}\n```';
    const { vm: specVm } = await createVm({ timeline: { 'thread-active': [{ kind: 'plan', ts: '2026-03-09T00:00:00Z', done: false, text: specText }] } }); expect(specVm.activePinnedPlan.value.key).toBe('ts:2026-03-09T00:00:00Z'); const specCard = specVm.pinnedPlanCardSpec(specVm.activePinnedPlan.value); expect(specCard.children.some((item) => item.type === 'Markdown')).toBe(true); expect(specCard.children.some((item) => item.type === 'Text')).toBe(true); specVm.dismissPinnedPlan(); expect(specVm.activePinnedPlan.value).toBeNull();
    try { sessionStorage.removeItem('__plan_dismissed_v2__'); } catch {}
    const { vm: textVm } = await createVm({ timeline: { 'thread-active': [{ kind: 'plan', done: true, text: '纯文本计划' }] } }); expect(textVm.activePinnedPlan.value.key).toBe('纯文本计划'); expect(textVm.pinnedPlanCardSpec({ text: '', done: false }).children.at(-1)).toEqual({ type: 'Text', text: '(empty plan)' });
  });

  it('covers send early return, selected thread reuse and thread bootstrap', async () => {
    const { vm: emptyVm, store: emptyStore } = await createVm(); await emptyVm.send(); expect(emptyStore.startThread).not.toHaveBeenCalled(); expect(emptyStore.sendMessage).not.toHaveBeenCalled();
    composer.state.text = 'hello'; const { vm, store } = await createVm({ selectedId: 'thread-active', active: '/repo' }); await vm.send(); expect(store.startThread).not.toHaveBeenCalled(); expect(store.sendMessage).toHaveBeenCalledWith('thread-active', 'hello', [], { manualSkillSelection: false }); expect(store.sendMessage.mock.calls[0][3]).not.toHaveProperty('selectedSkills'); expect(store.sendMessage.mock.calls[0][3]).not.toHaveProperty('selectedSkillRefs'); expect(composer.clearComposer).toHaveBeenCalled(); expect(autoScroll.schedule).toHaveBeenCalledWith(true);
    composer.state.text = 'boot'; composer.state.attachments = [{ name: 'a.txt' }]; const { vm: bootVm, store: bootStore } = await createVm({ selectedId: '', active: '/repo' }); await bootVm.send(); expect(bootStore.startThread).toHaveBeenCalledWith('/repo', expect.objectContaining({ focusMode: 'chat', deferSpawn: true, launchIntentId: expect.stringMatching(/^launch_/) })); expect(bootStore.sendMessage).toHaveBeenCalledWith('thread-started', 'boot', [{ name: 'a.txt' }], { manualSkillSelection: false, cwd: '/repo' }); expect(bootStore.sendMessage.mock.calls[0][3]).not.toHaveProperty('selectedSkills'); expect(bootStore.sendMessage.mock.calls[0][3]).not.toHaveProperty('selectedSkillRefs');
  });

  it('covers interruptCurrent reject and error branches', async () => {
    const { vm: noThreadVm } = await createVm({ selectedId: '' }); const noThreadReject = vi.fn(); await noThreadVm.interruptCurrent({ reject: noThreadReject }); expect(noThreadReject).toHaveBeenCalledWith({ reason: 'no_thread' });
    const { vm: rejectVm, store: rejectStore } = await createVm(); rejectStore.stopThread.mockResolvedValueOnce({ confirmed: false, settled: false, mode: 'needs_confirm' }); const reject = vi.fn(); await rejectVm.interruptCurrent({ threadId: 'thread-active', reject }); expect(reject).toHaveBeenCalledWith({ reason: 'needs_confirm', mode: 'needs_confirm', threadId: 'thread-active' });
    const { vm: errorVm, store: errorStore } = await createVm(); errorStore.stopThread.mockRejectedValueOnce(new Error('boom')); const errorReject = vi.fn(); await expect(errorVm.interruptCurrent({ threadId: 'thread-active', reject: errorReject })).rejects.toThrow('boom'); expect(errorReject).toHaveBeenCalledWith({ reason: 'error', threadId: 'thread-active' });
  });

  it('covers keyboard escape guards and dedupe handling', async () => {
    const { store, projectStore } = await createVm({ status: { 'thread-active': 'running' }, interruptible: { 'thread-active': true } }); hooks.mounted.splice(0).forEach((fn) => fn()); const handler = globalThis.window.addEventListener.mock.calls.find(([type]) => type === 'keydown')[1];
    const event = { key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'TEXTAREA', id: 'chatInput', closest: () => null } }; handler(event); await flush(); expect(event.preventDefault).toHaveBeenCalled(); expect(store.stopThread).toHaveBeenCalledWith('thread-active', { source: 'ui_stop' }); store.stopThread.mockClear(); handler(event); expect(store.stopThread).not.toHaveBeenCalled();
    handler({ key: 'Escape', repeat: true, preventDefault: vi.fn(), target: { tagName: 'TEXTAREA', id: 'chatInput', closest: () => null } }); handler({ key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'INPUT', closest: () => null } }); expect(store.stopThread).not.toHaveBeenCalled(); projectStore.state.showModal = true; await flush(); handler({ key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'TEXTAREA', id: 'chatInput', closest: () => null } }); expect(store.stopThread).not.toHaveBeenCalled();
    const { store: noInterruptStore } = await createVm({ status: { 'thread-active': 'running' }, interruptible: { 'thread-active': false } }); hooks.mounted.splice(0).forEach((fn) => fn()); const noInterruptHandler = globalThis.window.addEventListener.mock.calls.find(([type]) => type === 'keydown')[1]; noInterruptHandler({ key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'TEXTAREA', id: 'chatInput', closest: () => null } }); expect(noInterruptStore.stopThread).not.toHaveBeenCalled();
  });

  it('pauses status timers and lets modal Escape close the path chooser first', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-03-09T00:00:00Z'));
    let intervalCallback = () => {};
    globalThis.window.setInterval = vi.fn((cb) => { intervalCallback = cb; return 7; });
    globalThis.window.clearInterval = vi.fn();
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/code/locate') {
        return { ok: true, paths: ['/repo/src/a.js', '/repo/lib/src/a.js'], truncated: false };
      }
      return {};
    });
    const { vm, store } = await createVm({
      selectedId: 'thread-active',
      active: '/repo',
      projects: ['/repo'],
      status: { 'thread-active': 'running' },
      interruptible: { 'thread-active': true },
      diff: { 'thread-active': '' },
    });
    await runMountedHooks();

    vi.setSystemTime(new Date('2026-03-09T00:00:05Z'));
    intervalCallback();
    expect(vm.activeStatusMeta.value).toContain('5s');

    const pending = vm.onTimelineFileRefClick({ path: 'src/a.js', line: 3, column: 1 });
    await flush();
    expect(vm.showPathChoiceModal.value).toBe(true);
    expect(vm.isStatusTimerModalPaused.value).toBe(true);
    expect(globalThis.window.clearInterval).toHaveBeenCalled();

    const globalEscape = globalThis.window.addEventListener.mock.calls.find(([type]) => type === 'keydown')[1];
    globalEscape({ key: 'Escape', preventDefault: vi.fn(), target: { tagName: 'TEXTAREA', id: 'chatInput', closest: () => null } });
    await flush();
    expect(store.stopThread).not.toHaveBeenCalled();

    vi.setSystemTime(new Date('2026-03-09T00:00:12Z'));
    const modalVm = PathChoiceModal.setup({
      show: true,
      options: vm.pathChoiceOptions.value,
      title: vm.pathChoiceTitle.value,
      truncated: vm.pathChoiceTruncated.value,
      onConfirm: vm.confirmPathChoice,
      onCancel: vm.cancelPathChoice,
    });
    const escEvent = { preventDefault: vi.fn() };
    modalVm.onEscapeKey(escEvent);
    await pending;

    expect(escEvent.preventDefault).toHaveBeenCalled();
    expect(vm.showPathChoiceModal.value).toBe(false);
    expect(vm.isStatusTimerModalPaused.value).toBe(false);

    vi.setSystemTime(new Date('2026-03-09T00:00:14Z'));
    intervalCallback();
    expect(vm.activeStatusMeta.value).toContain('7s');
  });

  it('confirms dirty preview abandonment before switching files', async () => {
    const { vm } = await createVm({
      selectedId: 'thread-active',
      active: '/repo',
      projects: ['/repo'],
      diff: { 'thread-active': '' },
    });
    diffMock.last.fallbackMarkdownPreview.value = {
      previewKind: 'markdown',
      path: 'docs/current.md',
      filePath: '/repo/docs/current.md',
      text: '# Current',
      language: 'markdown',
      startLine: 1,
      endLine: 1,
      totalLines: 1,
      editable: true,
    };
    vm.onPreviewDirtyChange(true);
    expect(vm.isPreviewDirty.value).toBe(true);

    vi.mocked(callAPI).mockReset();
    vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'ui/code/locate') {
        return { ok: true, paths: ['/repo/docs/next.md'], truncated: false };
      }
      if (method === 'ui/code/open') {
        return {
          ok: true,
          language: 'markdown',
          relative: 'docs/next.md',
          filePath: '/repo/docs/next.md',
          snippet: '# Next',
          startLine: 1,
          endLine: 1,
          totalLines: 1,
        };
      }
      return {};
    });

    globalThis.window.confirm.mockReturnValueOnce(false);
    await vm.onTimelineFileRefClick({ path: 'docs/next.md', line: 1, column: 1 });
    expect(globalThis.window.confirm).toHaveBeenCalledWith('当前文件有未保存的修改，是否放弃？ (切换到 docs/next.md)');
    expect(vi.mocked(callAPI)).not.toHaveBeenCalled();
    expect(vm.isPreviewDirty.value).toBe(true);
    expect(diffMock.last.fallbackMarkdownPreview.value.filePath).toBe('/repo/docs/current.md');

    globalThis.window.confirm.mockReturnValueOnce(true);
    await vm.onTimelineFileRefClick({ path: 'docs/next.md', line: 1, column: 1 });
    expect(vi.mocked(callAPI).mock.calls.map(([method]) => method)).toEqual(['ui/code/locate', 'ui/code/open']);
    expect(diffMock.last.fallbackMarkdownPreview.value.filePath).toBe('/repo/docs/next.md');
    expect(vm.isPreviewDirty.value).toBe(false);
  });

  it('resets dirty state on thread switch', async () => {
    const { vm } = await createVm({
      selectedId: 'thread-active',
      active: '/repo',
      projects: ['/repo'],
    });
    vm.onPreviewDirtyChange(true);
    expect(vm.isPreviewDirty.value).toBe(true);

    vm.selectThread('thread-2');
    await flush();
    expect(vm.isPreviewDirty.value).toBe(false);
  });

  it('covers inline rename enter submit, compose and blur guards', async () => {
    vi.useFakeTimers();
    const { vm, store } = await createVm({ selectedId: 'thread-active' });
    const input = { focus: vi.fn(), select: vi.fn() };
    vm.setRenameInputRef('thread-active', input);
    vm.beginInlineRename('thread-active');
    await nextTick();
    expect(input.focus).toHaveBeenCalled();
    expect(input.select).toHaveBeenCalled();

    vm.editingAlias.value = 'Renamed';
    vm.handleInlineRenameEnter({ preventDefault: vi.fn() }, 'thread-active');
    await flush();
    expect(store.renameThread).toHaveBeenCalledWith('thread-active', 'Renamed');

    vm.beginInlineRename('thread-active');
    vm.handleInlineRenameEnter({ isComposing: true, preventDefault: vi.fn() }, 'thread-active');
    await flush();
    expect(store.renameThread).toHaveBeenCalledTimes(1);
    vm.handleInlineRenameBlur({ relatedTarget: { dataset: { renameSaveButtonFor: 'thread-active' } } }, 'thread-active');
    expect(vm.editingThreadId.value).toBe('thread-active');
    vm.handleInlineRenameBlur({ relatedTarget: null }, 'thread-active');
    expect(vm.editingThreadId.value).toBe('');

    vm.beginInlineRename('thread-active');
    vm.editingAlias.value = 'Renamed';
    await vm.submitInlineRename('thread-active');
    expect(store.renameThread).toHaveBeenCalledTimes(1);

    const { vm: cmdVm, store: cmdStore } = await createVm({ mode: 'cmd', selectedId: 'thread-active' });
    cmdVm.renameCard('thread-active');
    expect(cmdStore.promptRenameThread).toHaveBeenCalledWith('thread-active');
  });


  it('covers file ref same-thread, current-thread-only fallback, no-thread and code-open fallback branches', async () => {
    const diff = ['diff --git a/src/a.js b/src/a.js', '--- a/src/a.js', '+++ b/src/a.js', '@@ -1,2 +1,3 @@', ' line1', '+added', ' line2'].join('\n');
    const { vm: directVm } = await createVm({ selectedId: 'thread-active', diff: { 'thread-active': diff, 'thread-2': '' } });
    await directVm.onTimelineFileRefClick({ path: 'src/a.js', line: 7 });
    expect(diffMock.last.focusedDiffPath.value).toBe('src/a.js');
    expect(diffMock.last.focusedDiffLine.value).toBe(7);

    const { vm: crossVm } = await createVm({ selectedId: 'thread-active', diff: { 'thread-active': '', 'thread-2': diff } });
    await crossVm.onTimelineFileRefClick({ path: 'src/a.js', line: 9 });
    expect(diffMock.last.focusedDiffPath.value).toBe('src/a.js');
    expect(diffMock.last.focusedDiffLine.value).toBe(9);
    expect(crossVm.selectedThreadId.value).toBe('thread-active');

    const { vm: noThreadVm } = await createVm({ selectedId: '', diff: { 'thread-active': diff } });
    await noThreadVm.onTimelineFileRefClick({ path: 'src/a.js', line: 1 });
    expect(diffMock.last.focusedDiffPath.value).toBe('');

    const { vm: fallbackVm } = await createVm({ selectedId: 'thread-active', active: '/repo', projects: ['/repo'], diff: { 'thread-active': '' } });
    vi.mocked(callAPI).mockReset();
    vi.mocked(callAPI).mockRejectedValueOnce(new Error('open failed'));
    await fallbackVm.onTimelineFileRefClick({ path: 'app.log', line: 3 });
    const openPaths = vi.mocked(callAPI).mock.calls.filter(([method]) => method === 'ui/code/open').map(([, payload]) => payload.filePath);
    expect(openPaths).toEqual(['app.log', 'logs/app.log']);
    expect(diffMock.last.focusedDiffPath.value).toBe('app.log');
    expect(diffMock.last.focusedDiffLine.value).toBe(3);
  });

  it('covers copySelectedThreadId fallback identity, provider and log-path branches', async () => {
    vi.useFakeTimers(); provider.useClaude = true; vi.setSystemTime(new Date('2026-03-09T01:02:03Z')); vi.mocked(resolveThreadIdentity).mockResolvedValueOnce({ providerThreadId: 'provider-2', port: 9911 }); vi.mocked(callAPI).mockImplementation(async (method) => {
      if (method === 'thread/config/get') return { effective: { model: 'gpt-5.5', effort: 'high' } };
      if (method === 'ui/preferences/get') throw new Error('pref failed');
      return {};
    });
    const { vm } = await createVm({ selectedId: 'thread-active', active: '/Users/mima0000/Desktop/wj/go-agent-v2', runtime: { 'thread-active': { providerThreadId: '', port: undefined, provider: '', cwd: '', logPath: '' } } }); await vm.copySelectedThreadId();
    expect(resolveThreadIdentity).toHaveBeenCalledWith('thread-active'); const payload = JSON.parse(vi.mocked(copyTextToClipboard).mock.calls[0][0]); expect(payload.providerThreadId).toBe('provider-2'); expect(payload.uuid).toBe('provider-2'); expect(payload.port).toBe(9911); expect(payload.provider).toBe('claude'); expect(payload.model).toBe('gpt-5.5'); expect(payload.effort).toBe('high'); expect(payload.cwd).toBe('/Users/mima0000/Desktop/wj/go-agent-v2'); expect(payload['log-path']).toBe('~/.multi-agent/log/go-agent-v2/'); expect(payload.copiedAt).toBe('2026-03-09 09:02:03 UTC+8');
  });

  it('copies raw thread name without display fallback', async () => {
    const { vm } = await createVm({
      selectedId: 'thread-active',
      threads: [{ id: 'thread-active', name: '' }],
      runtime: { 'thread-active': { providerThreadId: '019e21e0-a4cb-7ea1-be81-c48ae16054d8', provider: 'codex' } },
    });
    await vm.copySelectedThreadId();
    const payload = JSON.parse(vi.mocked(copyTextToClipboard).mock.calls[0][0]);
    expect(payload.name).toBe('');
  });

  it('resolves placeholder provider thread id before copying', async () => {
    const placeholder = 'agent_1778322950141345000';
    const uuid = '58d9a5b6-f622-409b-82ae-4c4c42224311';
    provider.useClaude = true;
    vi.mocked(resolveThreadIdentity).mockResolvedValueOnce({ providerThreadId: uuid });
    const { vm } = await createVm({
      selectedId: 'thread-active',
      runtime: { 'thread-active': { providerThreadId: placeholder, provider: 'claude' } },
    });
    await vm.copySelectedThreadId();
    expect(resolveThreadIdentity).toHaveBeenCalledWith('thread-active');
    const payload = JSON.parse(vi.mocked(copyTextToClipboard).mock.calls[0][0]);
    expect(payload.providerThreadId).toBe(uuid);
    expect(payload.uuid).toBe(uuid);
  });

  it('covers copySelectedThreadId clipboard failure state reset', async () => {
    vi.useFakeTimers(); vi.mocked(copyTextToClipboard).mockRejectedValueOnce(new Error('copy failed')); const { vm } = await createVm({ selectedId: 'thread-active' }); await vm.copySelectedThreadId(); expect(vm.copyButtonLabel.value).toBe('复制失败'); await vi.advanceTimersByTimeAsync(1200); expect(vm.copyButtonLabel.value).toBe('复制信息');
  });

  it('covers recoverSelected fallback API, guard, failure and finally reset', async () => {
    const { vm, store } = await createVm({ selectedId: 'thread-active' }); vm.recoveringSelected.value = true; await vm.recoverSelected(); expect(store.recoverThread).not.toHaveBeenCalled();
    vm.recoveringSelected.value = false; store.recoverThread = undefined; await vm.recoverSelected(); expect(callAPI).toHaveBeenCalledWith('thread/recover', { threadId: 'thread-active' }); expect(globalThis.window.alert).toHaveBeenCalledWith('已触发进程恢复，请等待连接重建。'); expect(vm.recoveringSelected.value).toBe(false);
    store.recoverThread = vi.fn(async () => { throw new Error('boom'); }); globalThis.window.alert.mockClear(); await vm.recoverSelected(); expect(globalThis.window.alert).toHaveBeenCalledWith('进程恢复失败: boom'); expect(vm.recoveringSelected.value).toBe(false);
  });

  it('covers openNewWindow cancel/failure', async () => {
    const { vm, store } = await createVm({ selectedId: 'thread-active' }); vi.mocked(callAPI).mockResolvedValueOnce({}); await vm.openNewWindow(); expect(vi.mocked(callAPI).mock.calls.some(([method]) => method === 'ui/openNewWindow')).toBe(false);
    vi.mocked(callAPI).mockReset().mockResolvedValueOnce({ path: '/tmp/child' }).mockRejectedValueOnce(new Error('open failed')); await expect(vm.openNewWindow()).rejects.toThrow('open failed'); expect(vi.mocked(callAPI).mock.calls.map(([method]) => method)).toEqual(['ui/selectProjectDir', 'ui/openNewWindow']);
  });

  it('covers file ref markdown, image, synthetic diff and empty path branches', async () => {
    const { vm: emptyPathVm } = await createVm({ selectedId: 'thread-active', diff: { 'thread-active': '' } }); await emptyPathVm.onTimelineFileRefClick({ path: '', line: 9 }); expect(diffMock.last.focusedDiffPath.value).toBe('');
    const { vm: markdownVm } = await createVm({ selectedId: 'thread-active', active: '/repo', projects: ['/repo'], diff: { 'thread-active': '' } }); vi.mocked(callAPI).mockResolvedValueOnce({}).mockResolvedValueOnce({ ok: true, language: 'markdown', relative: 'docs/readme.md', filePath: '/repo/docs/readme.md', snippet: '# Title', startLine: 4, endLine: 4, totalLines: 20 }); await markdownVm.onTimelineFileRefClick({ path: 'docs/readme.md', line: 0, column: -9 }); expect(vi.mocked(callAPI).mock.calls.at(-1)[1]).toMatchObject({ filePath: 'docs/readme.md', line: 1, column: 0 }); expect(diffMock.last.fallbackMarkdownPreview.value.path).toBe('docs/readme.md'); expect(diffMock.last.focusedDiffPath.value).toBe('docs/readme.md'); expect(diffMock.last.focusedDiffLine.value).toBe(1);
    const { vm: imageVm } = await createVm({ selectedId: 'thread-active', active: '/repo', projects: ['/repo'], diff: { 'thread-active': '' } }); vi.mocked(callAPI).mockResolvedValueOnce({}).mockResolvedValueOnce({ ok: true, relative: 'images/a.png', filePath: '/repo/images/a.png', mediaType: 'image/png', previewURL: 'file:///repo/images/a.png', sizeBytes: 55 }); await imageVm.onTimelineFileRefClick({ path: 'images/a.png', line: 8 }); expect(diffMock.last.fallbackMediaPreview.value.path).toBe('images/a.png'); expect(diffMock.last.focusedDiffPath.value).toBe('images/a.png'); expect(diffMock.last.focusedDiffLine.value).toBe(0);
    const { vm: syntheticVm } = await createVm({ selectedId: 'thread-active', active: '/repo', projects: ['/repo'], diff: { 'thread-active': '' } }); vi.mocked(callAPI).mockResolvedValueOnce({}).mockResolvedValueOnce({ ok: true, relative: 'src/b.js', filePath: '/repo/src/b.js', snippet: 'console.log(1);', startLine: 7 }); await syntheticVm.onTimelineFileRefClick({ path: 'src/b.js', line: 5 }); expect(diffMock.last.fallbackDiffText.value).toContain('diff --git a/src/b.js b/src/b.js'); expect(diffMock.last.focusedDiffPath.value).toBe('src/b.js'); expect(diffMock.last.focusedDiffLine.value).toBe(5);
  });
});
