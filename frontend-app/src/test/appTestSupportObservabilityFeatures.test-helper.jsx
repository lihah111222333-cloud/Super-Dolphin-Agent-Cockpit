import React from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { expect, vi } from 'vitest';
import { normalizeMemorySnapshot as normalizeMemorySnapshotForFacade } from '../adapters/memoryAdapter.js';

let backend;
let App;
let formatParsedTimestampForTest;
let canonicalPromptRPCItem;
let waitForBackendThreadHeading;
function mockTraceDashboardQueryResult() {
  backend.listObservabilityRecent.mockResolvedValueOnce({
    source: 'mixed',
    total_duration_ms: 135,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [{
      ts: '2026-06-02T09:01:20.100Z',
      trace_id: 'trace-1',
      span_id: 'span-rpc',
      method: 'rpc.dispatch',
      status: 'slow',
      duration_ms: 120,
      thread_id: 'thread-1',
    }],
  });
  backend.getObservabilityTrace.mockResolvedValue({
    source: 'mixed',
    total_duration_ms: 135,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      { ts: '2026-06-02T09:01:19.000Z', trace_id: 'trace-1', span_id: 'span-begin', method: 'tool.call.begin', status: 'ok', thread_id: 'thread-1' },
      {
        ts: '2026-06-02T09:01:20.100Z',
        trace_id: 'trace-1',
        span_id: 'span-rpc',
        method: 'rpc.dispatch',
        status: 'slow',
        duration_ms: 120,
        thread_id: 'thread-1',
        parent_span_id: 'span-root',
        code: { file: 'internal/platform/rpc/server.go', function: '(*Server).Dispatch', line: 270 },
        stack: [{ file: 'internal/platform/rpc/server.go', function: '(*Server).Dispatch', line: 270 }],
        error: 'rpc dispatch exceeded slow threshold',
        metadata: { component: 'rpc', route: 'observability/trace/get' },
      },
      { ts: '2026-06-02T09:01:23.000Z', trace_id: 'trace-1', span_id: 'span-ui', method: 'ui/sidebar/get', status: 'ok', thread_id: 'thread-1' },
      { ts: '2026-06-02T09:01:24.000Z', trace_id: 'trace-1', span_id: 'span-noise', method: 'bus.event.lifecycle', kind: 'bus_event', status: 'dropped_summary', thread_id: 'thread-1' },
    ],
  });
}

async function openTraceDashboardForTraceId() {
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: '链路追踪' }));
  fireEvent.change(await screen.findByLabelText('Trace ID'), { target: { value: 'trace-1' } });
  fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
  const table = await screen.findByTestId('observability-recent-logs');
  fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-1' }));
  return table;
}

function expectTraceDashboardRpcCalls() {
  expect(backend.listObservabilityRecent).toHaveBeenCalledTimes(1);
  expect(backend.listObservabilityRecent).toHaveBeenCalledWith({
    limit: 50,
    status: '',
    component: '',
    method: '',
    traceId: 'trace-1',
    threadId: '',
    agentId: '',
    keyword: '',
    includeTail: true,
  });
  expect(backend.getObservabilityTrace).toHaveBeenCalledWith({ traceId: 'trace-1', limit: 50 });
}

async function expectTraceDashboardRows(table) {
  const inlineTrace = await within(table).findByTestId('observability-inline-trace-trace-1');
  await waitFor(() => expect(inlineTrace).toHaveTextContent('source=mixed'));
  expect(screen.getAllByText(/internal\/platform\/rpc\/server.go:270/).length).toBeGreaterThan(0);
  let traceRows = [];
  await waitFor(() => {
    traceRows = within(inlineTrace).getAllByRole('listitem').filter((row) => row.classList.contains('observability-event-row'));
    expect(traceRows[0]).toHaveClass('observability-event-row');
  });
  expect(traceRows[0]).not.toHaveClass('settings-row');
  expect(traceRows[0]).toHaveTextContent('120ms');
  expect(traceRows[0]).toHaveTextContent('请求上下文');
  expect(traceRows[0]).toHaveTextContent('链路标识');
  expect(traceRows[0]).toHaveTextContent('失败原因');
  const zeroDurationRow = traceRows.find((row) => row.textContent.includes('ui/sidebar/get'));
  expect(zeroDurationRow).toBeTruthy();
  expect(zeroDurationRow).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:01:23.000Z'));
  expect(zeroDurationRow).toHaveTextContent('耗时未记录');
  expect(zeroDurationRow).not.toHaveTextContent('0ms');
  expect(zeroDurationRow).not.toHaveTextContent('code=-');
  expect(traceRows[0]).toHaveTextContent('trace');
  expect(traceRows[0]).toHaveTextContent('trace-1');
  expect(traceRows[0]).toHaveTextContent('span');
  expect(traceRows[0]).toHaveTextContent('span-rpc');
  expect(traceRows[0]).toHaveTextContent('parent');
  expect(traceRows[0]).toHaveTextContent('span-root');
}

function expectTraceDashboardDetails() {
  expect(screen.getByText('rpc dispatch exceeded slow threshold')).toBeInTheDocument();
  expect(screen.getByText(/"component": "rpc"/)).toBeInTheDocument();
  expect(screen.getByText(/"route": "observability\/trace\/get"/)).toBeInTheDocument();
  expect(screen.getByText(/默认显示关键事件 2\/4/)).toBeInTheDocument();
  expect(screen.getByText(/已折叠 2 条成功过程事件/)).toBeInTheDocument();
  expect(screen.queryByText('tool.call.begin')).not.toBeInTheDocument();
  expect(screen.queryByText('bus.event.lifecycle')).not.toBeInTheDocument();
}

async function showAllTraceDashboardEvents() {
  fireEvent.click(screen.getByRole('button', { name: '显示全部事件' }));
  await waitFor(() => expect(screen.getAllByText('tool.call.begin').length).toBeGreaterThan(0));
  expect(screen.getAllByText('bus.event.lifecycle').length).toBeGreaterThan(0);
}

function mockRecentSystemLogsResult() {
  backend.listObservabilityRecent.mockResolvedValue({
    source: 'mixed',
    total_duration_ms: 38,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [
      {
        ts: '2026-06-02T09:01:22.459Z',
        trace_id: 'trace-frontend-1',
        span_id: 'span-ui',
        method: 'thread/start',
        phase: 'frontend.rpc.failed',
        kind: 'frontend',
        status: 'error',
        duration_ms: 33,
        thread_id: 'thread-1',
        client_route: '/chat',
        error: 'thread start failed',
      },
      {
        ts: '2026-06-02T09:01:20.100Z',
        trace_id: 'trace-frontend-1',
        span_id: 'span-rpc',
        method: 'rpc.dispatch',
        kind: 'rpc',
        status: 'ok',
        duration_ms: 5,
        thread_id: 'thread-1',
      },
      {
        ts: '2026-06-02T09:02:03.000Z',
        trace_id: 'trace-frontend-2',
        span_id: 'span-ui-2',
        method: 'thread/config/get',
        phase: 'frontend.rpc.done',
        kind: 'frontend',
        status: 'ok',
        duration_ms: 7,
        thread_id: 'thread-2',
      },
      {
        ts: '2026-06-02T09:03:04.000Z',
        trace_id: '',
        span_id: 'span-provider',
        method: 'provider.session.acquire',
        kind: 'provider',
        status: 'ok',
        duration_ms: 3268,
        thread_id: 'thread-provider',
      },
    ],
  });
  backend.getObservabilityTrace.mockResolvedValue({
    source: 'mixed',
    total_duration_ms: 33,
    truncated: false,
    slowest_events: [],
    errors: [],
    events: [{
      trace_id: 'trace-frontend-1',
      span_id: 'span-ui',
      method: 'thread/start',
      status: 'error',
      duration_ms: 33,
    }],
  });
}

async function openRecentSystemLogs() {
  render(<App />);
  fireEvent.click(await screen.findByRole('button', { name: '链路追踪' }));
  fireEvent.change(await screen.findByLabelText('状态'), { target: { value: 'error' } });
  fireEvent.change(screen.getByLabelText('关键词'), { target: { value: 'thread/start' } });
  fireEvent.click(screen.getByRole('button', { name: '查询最新日志' }));
  return screen.findByTestId('observability-recent-logs');
}

function expectRecentSystemLogsTable(table) {
  expect(table).toHaveTextContent('3 条匹配 event 分组 · 4 个匹配 event');
  expect(table).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:01:22.459Z'));
  expect(table).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:02:03.000Z'));
  expect(table).toHaveTextContent(formatParsedTimestampForTest('2026-06-02T09:03:04.000Z'));
  expect(table).not.toHaveTextContent('2026-06-02T09:01:22.459Z');
  expect(table).toHaveTextContent('thread/start');
  expect(table).toHaveTextContent('trace-frontend-1');
  expect(table).toHaveTextContent('thread start failed');
  expect(table).toHaveTextContent('provider.session.acquire');
  expect(table).toHaveTextContent('trace=-');
  expect(within(table).getAllByRole('button', { name: /复制 Trace ID/ })).toHaveLength(3);
  expect(within(table).getAllByRole('button', { name: /打开 Trace/ })).toHaveLength(3);
  expect(within(table).getByRole('button', { name: '复制 Trace ID -' })).toBeDisabled();
  expect(within(table).getByRole('button', { name: '打开 Trace -' })).toBeDisabled();
  expect(table).toHaveTextContent('2 个匹配 event');
}

function expectRecentSystemLogsRpcCall() {
  expect(backend.listObservabilityRecent).toHaveBeenCalledWith({
    limit: 50,
    status: 'error',
    component: '',
    method: '',
    traceId: '',
    threadId: '',
    agentId: '',
    keyword: 'thread/start',
    includeTail: true,
  });
}

async function copyTraceFromRecentLogs(table) {
  expect(screen.queryByText(/Trace 查询结果/)).not.toBeInTheDocument();
  expect(within(table).queryByTestId('observability-inline-trace-trace-frontend-1')).not.toBeInTheDocument();
  fireEvent.click(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' }));

  await waitFor(() => expect(backend.copyTextToClipboard).toHaveBeenCalledWith('trace-frontend-1'));
  expect(within(table).getByRole('button', { name: '复制 Trace ID trace-frontend-1' })).toHaveTextContent('已复制');
  expect(backend.getObservabilityTrace).not.toHaveBeenCalled();
}

async function toggleInlineTraceFromRecentLogs(table) {
  fireEvent.click(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' }));

  const inlineTrace = await within(table).findByTestId('observability-inline-trace-trace-frontend-1');
  await waitFor(() => {
    expect(inlineTrace).toHaveTextContent('Trace 结果');
    expect(inlineTrace).toHaveTextContent('source=mixed');
    expect(inlineTrace).toHaveTextContent('thread/start');
  });
  expect(within(table).getByRole('button', { name: '收起 Trace trace-frontend-1' })).toHaveAttribute('aria-expanded', 'true');
  expect(backend.getObservabilityTrace).toHaveBeenCalledWith({ traceId: 'trace-frontend-1', limit: 50 });
  expect(backend.listObservabilityRecent).toHaveBeenCalledTimes(1);
  expect(table).toHaveTextContent('trace-frontend-2');

  fireEvent.click(within(table).getByRole('button', { name: '收起 Trace trace-frontend-1' }));
  await waitFor(() => expect(within(table).queryByTestId('observability-inline-trace-trace-frontend-1')).not.toBeInTheDocument());
  expect(within(table).getByRole('button', { name: '打开 Trace trace-frontend-1' })).toHaveAttribute('aria-expanded', 'false');
  expect(backend.getObservabilityTrace).toHaveBeenCalledTimes(1);
}

function mockPromptAssetWorkflow() {
  backend.getPreference.mockImplementation(({ key }) => Promise.resolve({
    'settings.provider.active': 'codex',
    'settings.provider.codex.model': 'gpt-5.5',
    'settings.provider.codex.effort': 'xhigh',
    'settings.provider.codex.codexHome': '~/.codex',
    'settings.provider.codex.codexInstanceKey': 'default',
    'settings.provider.codex.codexModelProvider': 'openrouter',
  }[key] ?? null));
  let prompts = [canonicalPromptRPCItem({
    id: 'main/reviewer',
    name: '代码审查专家',
    content: '先检查阻塞问题',
    description: '审查代码质量',
    when_to_use: 'Use for code review.',
    agentType: 'coder',
    tags: ['intent:expert', 'review'],
    scope: 'project',
    enabled: true,
  }), canonicalPromptRPCItem({
    id: 'intent/recall/ready',
    draft_key: 'intent/recall/ready',
    name: '价格表资料',
    content: '价格资料内容',
    description: '待确认的资料',
    tags: ['intent:recall', 'pricing'],
    scope: 'project',
    enabled: false,
    state: 'pending_confirm',
    draft_status: 'ready_to_save',
    card: { kind: 'recall', title: '价格表资料', summary: '待确认的资料', output: '价格资料内容' },
  })];
  backend.listPromptAssets.mockImplementation(() => Promise.resolve({ prompts }));
  backend.writePrompt.mockImplementation(({ id, name, content }) => {
    prompts = prompts.map((item) => (item.id === id ? { ...item, name, content } : item));
    return Promise.resolve({ prompt: { id } });
  });
  backend.deletePrompt.mockImplementation(({ id }) => {
    prompts = prompts.filter((item) => item.id !== id);
    return Promise.resolve({ deleted: true });
  });
  backend.draftPromptIntent.mockResolvedValue({
    draft_key: 'intent/expert/review',
    kind: 'expert',
    scope: 'project',
    status: 'review',
    card: {
      kind: 'expert',
      title: '代码风险审查',
      summary: '识别阻塞风险',
      output: '先列阻塞问题，再给修改建议',
      hit_examples: ['审查这段代码'],
      miss_examples: ['解释一个概念'],
    },
    issues: [],
  });
  backend.commitPromptIntent.mockResolvedValue({ prompt: { id: 'main/code-risk-review' } });
}

async function openPromptAssetsPage() {
  render(<App />);
  await waitForBackendThreadHeading();
  fireEvent.click(screen.getByLabelText('提示词'));
  expect(await screen.findByText('代码审查专家')).toBeInTheDocument();
}

async function openPromptWizardFromPendingCard(cardName = '价格表资料') {
  const pendingCard = (await screen.findByText(cardName)).closest('article');
  const continueButton = within(pendingCard).getByRole('button', { name: '继续确认' });
  fireEvent.click(continueButton);
  const wizard = await screen.findByRole('dialog', { name: '添加给 AI 的内容' });
  return { continueButton, pendingCard, wizard };
}

async function editAndDeleteReviewerPrompt() {
  const card = screen.getByText('代码审查专家').closest('article');
  backend.getPrompt.mockResolvedValueOnce({ prompt: { content: '完整审查提示词' } });
  fireEvent.click(within(card).getByRole('button', { name: '复制' }));
  await waitFor(() => {
    expect(backend.getPrompt).toHaveBeenCalledWith({ cwd: '/repo/app', id: 'main/reviewer' });
    expect(backend.copyTextToClipboard).toHaveBeenCalledWith('完整审查提示词');
  });
  expect(await screen.findByText('已复制提示词内容')).toBeInTheDocument();
  fireEvent.click(within(card).getByRole('button', { name: '编辑' }));
  const editor = await screen.findByRole('dialog', { name: '编辑提示词' });
  expect(editor).toBeInTheDocument();
  expect(within(editor).getByText('可用范围：这个项目')).toBeInTheDocument();
  expect(within(editor).getByLabelText('保存后 AI 会看到什么')).toHaveValue('先检查阻塞问题');
  expect(within(editor).queryByLabelText('Agent Key')).not.toBeInTheDocument();
  expect(within(editor).queryByLabelText('场景标签')).not.toBeInTheDocument();
  expect(within(editor).queryByLabelText('排序权重')).not.toBeInTheDocument();
  fireEvent.change(screen.getByLabelText('名称'), { target: { value: '代码风险审查' } });
  fireEvent.change(screen.getByLabelText('AI 使用时怎么做'), { target: { value: '先列阻塞问题，再给修改建议' } });
  fireEvent.click(screen.getByRole('button', { name: '保存' }));
  await waitFor(() => {
    expect(backend.writePrompt).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      id: 'main/reviewer',
      name: '代码风险审查',
      agentType: 'coder',
      content: '先列阻塞问题，再给修改建议',
      scope: 'project',
      enabled: true,
    }));
  });

  await screen.findByText('代码风险审查');
}

async function handlePendingPromptDraft() {
  const { pendingCard, wizard: pendingDialog } = await openPromptWizardFromPendingCard('价格表资料');
  expect(screen.getAllByText('价格表资料').length).toBeGreaterThanOrEqual(1);
  fireEvent.click(within(pendingDialog).getAllByRole('button', { name: '关闭' }).at(-1));

  fireEvent.click(within(pendingCard).getByRole('button', { name: '丢弃' }));
  await waitFor(() => {
    expect(backend.discardPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/recall/ready' });
  });
}

async function createGeneratedPromptIntent() {
  const { wizard } = await openPromptWizardFromPendingCard('价格表资料');
  fireEvent.click(within(wizard).getByRole('tab', { name: '专家能力' }));
  fireEvent.change(screen.getByLabelText('写下希望 AI 记住或使用的内容'), {
    target: { value: '当用户要求代码审查时，先检查阻塞问题。' },
  });
  expect(screen.queryByRole('button', { name: '整理草稿' })).not.toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '帮我生成' }));
  expect(await screen.findByText('代码风险审查')).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: '确认保存' }));
  await waitFor(() => {
    expect(backend.draftPromptIntent).toHaveBeenCalledWith({
      cwd: '/repo/app',
      kind: 'expert',
      rawInput: '当用户要求代码审查时，先检查阻塞问题。',
      sourceType: 'user_input',
      scope: 'project',
      provider: 'codex',
      model: 'gpt-5.5',
      codexModelProvider: 'openrouter',
    });
    expect(backend.commitPromptIntent).toHaveBeenCalledWith({ cwd: '/repo/app', draftKey: 'intent/expert/review', scope: 'project' });
  });
}

function createSimilaritySnapshots() {
  const group = {
    nameA: 'A', targetA: 'private', pathA: 'feedback/a.md',
    nameB: 'B', targetB: 'team', pathB: 'feedback/b.md',
    score: 0.88,
  };
  // 与真实 facade 输出一致：parse + transform 后的扁平 { overview, entries } 形态。
  const snapshotWithSimilar = normalizeMemorySnapshotForFacade({
    overview: {
      enabled: true,
      autoDreamEnabled: true,
      projectRoot: '/repo/app', writeAvailable: true,
      health: {
        preferenceCount: 2,
        projectCount: 0,
        maxPerCategory: 15,
        similarGroups: [group],
      },
    },
    private: { entries: [] },
    team: { entries: [] },
  });
  const snapshotWithoutSimilar = {
    ...snapshotWithSimilar,
    overview: {
      ...snapshotWithSimilar.overview,
      health: {
        ...snapshotWithSimilar.overview.health,
        similarGroups: [],
      },
    },
  };
  return { snapshotWithSimilar, snapshotWithoutSimilar };
}

async function openMemoryCenterWithSimilarity() {
  render(<App />);
  await waitForBackendThreadHeading();
  await waitFor(() => {
    expect(screen.getByLabelText('记忆中心').querySelector('i')).toHaveAttribute('title', '1 条待整合相似记忆');
  });

  fireEvent.click(screen.getByLabelText('记忆中心'));
  expect(await screen.findByText('1 组条目内容相似')).toBeInTheDocument();
}

async function runConsolidationUntilSimilaritiesClear(clearSimilarities) {
  vi.useFakeTimers();
  try {
    fireEvent.click(screen.getByRole('button', { name: '一键整合全部' }));
    await act(async () => {
      await Promise.resolve();
      await Promise.resolve();
    });

    expect(backend.startConsolidateMemorySimilarities).toHaveBeenCalledWith(expect.objectContaining({
      cwd: '/repo/app',
      provider: 'codex',
      codexModelProvider: 'openai',
    }));
    expect(backend.getMemoryConsolidationStatus).toHaveBeenCalledWith({ cwd: '/repo/app', jobId: 'memory-job-live' });
    expect(screen.getByRole('button', { name: '后台整合中' })).toBeDisabled();

    clearSimilarities();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(2000);
      await Promise.resolve();
    });
  }
  finally {
    vi.useRealTimers();
  }
}

function expectSimilarityWarningCleared() {
  expect(screen.queryByText('1 组条目内容相似')).not.toBeInTheDocument();
  expect(screen.queryByRole('button', { name: '一键整合全部' })).not.toBeInTheDocument();
  expect(screen.getByText('已整合 1 组')).toBeInTheDocument();
  expect(screen.getByLabelText('记忆中心').querySelector('i')).toBeNull();
}

export function createAppTestObservabilityFeatures(nextContext) {
  backend = nextContext.backend;
  App = nextContext.App;
  formatParsedTimestampForTest = nextContext.formatParsedTimestampForTest;
  canonicalPromptRPCItem = nextContext.canonicalPromptRPCItem;
  waitForBackendThreadHeading = nextContext.waitForBackendThreadHeading;
  return {
    mockTraceDashboardQueryResult: (...args) => mockTraceDashboardQueryResult(...args),
    openTraceDashboardForTraceId: (...args) => openTraceDashboardForTraceId(...args),
    expectTraceDashboardRpcCalls: (...args) => expectTraceDashboardRpcCalls(...args),
    expectTraceDashboardRows: (...args) => expectTraceDashboardRows(...args),
    expectTraceDashboardDetails: (...args) => expectTraceDashboardDetails(...args),
    showAllTraceDashboardEvents: (...args) => showAllTraceDashboardEvents(...args),
    mockRecentSystemLogsResult: (...args) => mockRecentSystemLogsResult(...args),
    openRecentSystemLogs: (...args) => openRecentSystemLogs(...args),
    expectRecentSystemLogsTable: (...args) => expectRecentSystemLogsTable(...args),
    expectRecentSystemLogsRpcCall: (...args) => expectRecentSystemLogsRpcCall(...args),
    copyTraceFromRecentLogs: (...args) => copyTraceFromRecentLogs(...args),
    toggleInlineTraceFromRecentLogs: (...args) => toggleInlineTraceFromRecentLogs(...args),
    mockPromptAssetWorkflow: (...args) => mockPromptAssetWorkflow(...args),
    openPromptAssetsPage: (...args) => openPromptAssetsPage(...args),
    openPromptWizardFromPendingCard: (...args) => openPromptWizardFromPendingCard(...args),
    editAndDeleteReviewerPrompt: (...args) => editAndDeleteReviewerPrompt(...args),
    handlePendingPromptDraft: (...args) => handlePendingPromptDraft(...args),
    createGeneratedPromptIntent: (...args) => createGeneratedPromptIntent(...args),
    createSimilaritySnapshots: (...args) => createSimilaritySnapshots(...args),
    openMemoryCenterWithSimilarity: (...args) => openMemoryCenterWithSimilarity(...args),
    runConsolidationUntilSimilaritiesClear: (...args) => runConsolidationUntilSimilaritiesClear(...args),
    expectSimilarityWarningCleared: (...args) => expectSimilarityWarningCleared(...args),
  };
}
