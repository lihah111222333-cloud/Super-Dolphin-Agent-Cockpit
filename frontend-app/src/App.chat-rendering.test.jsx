import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import App from './App.jsx';
import { resetClientStoreForTests } from "./entities/client/model/useClientStore.js";
import './test-utils/preloadAppRouteModules.js';
import mermaid from 'mermaid';
import { createAppTestSupport } from './test/appTestSupport.test-helper.jsx';

let bridgeCallback;
let appOverlayHost;

window.matchMedia = vi.fn(() => ({
  matches: false,
  addEventListener: vi.fn(),
  removeEventListener: vi.fn(),
}));

const backend = vi.hoisted(() => {
  const mockNames = `
	    readConfig getWindowBootstrap openNewWindow getProjects setActiveProject addProject removeProject
	    callBackend checkAppUpdate installLatestAppUpdate
    getSidebarState getThreadState getThreadMessages getBuildInfo getVideoApiKey getDashboardPage getObservabilityStatus
    getObservabilityTrace getObservabilityThreadRecent listObservabilityRecent listObservabilitySlow
    listObservabilityErrors listSharedFiles listPromptAssets getDashboardPrompts getPrompt writePrompt
    readLspPromptHint writeLspPromptHint readBuiltinTools writeBuiltinTool listDashboardLogs
    getPersonalizationProfile savePersonalizationProfile listPromptSections writePromptSection deletePromptSection
    deletePrompt draftPromptIntent commitPromptIntent discardPromptIntent dryRunPromptIntent getMemorySnapshot
    getMemoryEntry upsertMemoryEntry deleteMemoryEntry setMemoryAutoDreamIntent mergeMemoryEntries
    ignoreMemorySimilarity consolidateMemorySimilarities startConsolidateMemorySimilarities getMemoryConsolidationStatus
    listDags getDagDetail getDagRuns getDagRun startDag terminateDagRun deleteDag applyDagOps listWorkflowTemplates getWorkflowTemplate renderWorkflowTemplateDraft deleteSkill
    listCronJobs getCronJob createCronJob updateCronJob deleteCronJob runCronJobOnce setCronJobEnabled listCronJobRuns
    readSkill listSkillFiles createSkill writeSkill importSkillDirectories suggestSkillSummary selectProjectDir selectProjectDirs
    createSkillTool listSkillTools getSkillTool updateSkillTool deleteSkillTool
    listMCPServers listToolbridgeTools startSQLiteMCPServer stopSQLiteMCPServer startPlaywrightMCPServer stopPlaywrightMCPServer
    listSkillResolutions previewSkillResolution applySkillResolution readSharedFile deleteSharedFile getPreference
    forkThread startThread startTurn interruptTurn forceCompleteTurn compactThread recoverThread respondApproval resolveThreadIdentity archiveThread unarchiveThread
    deleteThread getThreadConfig setThreadConfig renameThread setPreference setVideoApiKey selectFiles saveClipboardImage saveTextFile
    locateCodeFile openCodeFile openPath saveCodeFile beginTextClipboardWrite copyTextToClipboard emitFrontendTraceEvent
  `.trim().split(/\s+/);
  return {
    ...Object.fromEntries(mockNames.map((name) => [name, vi.fn()])),
    onFilesDropped: vi.fn(() => () => {}),
    onRuntimeReconnect: vi.fn(() => () => {}),
    onBridgeEvent: vi.fn((callback) => {
      bridgeCallback = callback;
      return () => {
        if (bridgeCallback === callback) bridgeCallback = null;
      };
    }),
  };
});

vi.mock('./shared/api/backendApi.js', () => ({
  ...backend,
  registerBridgeLogStore: vi.fn(),
  sendFrontendLogBatch: vi.fn(),
}));

vi.mock('mermaid', () => ({
  default: {
    initialize: vi.fn(),
    render: vi.fn((_id, source) => Promise.resolve({
      svg: `<svg role="img" aria-label="mock mermaid"><text>${source}</text></svg>`,
    })),
  },
}));

const appTestSupport = createAppTestSupport({
  App,
  backend,
  resetClientStoreForTests,
  state: {
    get bridgeCallback() { return bridgeCallback; },
    set bridgeCallback(value) { bridgeCallback = value; },
    get appOverlayHost() { return appOverlayHost; },
    set appOverlayHost(value) { appOverlayHost = value; },
  },
});
const {
  decodedSvgDataUrl,
  waitForBackendThreadHeading,
  installAppOverlayHost,
  resetConnectedShellTestState,
  mockBootstrapBackendDefaults,
  mockDashboardPageDefaults,
  mockObservabilityDefaults,
  mockPromptDefaults,
  mockMemoryDefaults,
  mockWorkflowDefaults,
  mockCronDefaults,
  mockSkillDefaults,
  mockSharedFileDefaults,
  mockSettingsAndThreadDefaults,
  resetFrontendHealthForTest,
  cleanupAppTest
} = appTestSupport;

beforeEach(installAppOverlayHost);
beforeEach(resetConnectedShellTestState);
beforeEach(mockBootstrapBackendDefaults);
beforeEach(mockDashboardPageDefaults);
beforeEach(mockObservabilityDefaults);
beforeEach(mockPromptDefaults);
beforeEach(mockMemoryDefaults);
beforeEach(mockWorkflowDefaults);
beforeEach(mockCronDefaults);
beforeEach(mockSkillDefaults);
beforeEach(mockSharedFileDefaults);
beforeEach(mockSettingsAndThreadDefaults);
beforeEach(resetFrontendHealthForTest);
afterEach(cleanupAppTest);

  it('renders assistant markdown messages as formatted content', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-md',
          kind: 'assistant',
          text: [
            '## 结果汇总',
            '',
            '| 工具 | 结果 |',
            '| --- | --- |',
            '| edit | 可用 |',
            '',
            '> 这是一条引用',
            '',
            '- [x] 已完成',
            '- [ ] 待处理',
            '',
            '访问 [官网](https://example.com)，这是 ~~旧内容~~。',
            '',
            '---',
            '',
            '![图例](https://example.com/chart.png)',
            '',
            '<script>alert(1)</script>',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    expect(await screen.findByRole('heading', { name: '结果汇总', level: 2 })).toBeInTheDocument();
    expect(screen.getByRole('table')).toBeInTheDocument();
    expect(screen.getByRole('columnheader', { name: '工具' })).toBeInTheDocument();
    expect(screen.getByRole('cell', { name: '可用' })).toBeInTheDocument();
    expect(screen.getByText('这是一条引用').closest('blockquote')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: '官网' })).toHaveAttribute('href', 'https://example.com/');
    expect(screen.getByText('旧内容').tagName.toLowerCase()).toBe('del');
    expect(screen.getByRole('checkbox', { name: '已完成' })).toBeChecked();
    expect(screen.getByRole('checkbox', { name: '待处理' })).not.toBeChecked();
    expect(container.querySelector('hr')).toBeInTheDocument();
    expect(screen.getByRole('img', { name: '图例' })).toHaveAttribute('src', 'https://example.com/chart.png');
    expect(screen.getByText('<script>alert(1)</script>')).toBeInTheDocument();
    expect(screen.queryByText('## 结果汇总')).not.toBeInTheDocument();
  });

  it('copies completed AI output from the assistant message action', async () => {
    const text = [
      '这是 AI 输出。',
      '',
      '```js',
      'console.log("copy me");',
      '```',
    ].join('\n');
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-copyable', kind: 'assistant', text, ts: '2026-05-30T00:00:00Z' }],
      },
    });

    render(<App />);

    await screen.findByText('这是 AI 输出。');
    fireEvent.click(screen.getByRole('button', { name: '复制 AI 输出' }));

    await waitFor(() => expect(backend.copyTextToClipboard).toHaveBeenCalledWith(text));
    expect(screen.getByRole('button', { name: '复制 AI 输出' })).toHaveTextContent('已复制');
  });

  it('renders mermaid code fences as diagrams instead of plain code blocks', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-mermaid',
          kind: 'assistant',
          text: [
            '总体结构如下：',
            '```mermaid',
            'flowchart TD',
            '  User[用户] --> App[前端]',
            '  App --> API[后端]',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    expect(await screen.findByLabelText('Mermaid 图表')).toBeInTheDocument();
    const image = await screen.findByRole('img', { name: 'Mermaid 图表' });
    expect(decodedSvgDataUrl(image)).toContain('flowchart TD');
    expect(container.querySelector('.mermaid-diagram')).toHaveTextContent('点击放大');
  });

  it('does not render Mermaid diagrams from unmaterialized older timeline history', async () => {
    const messages = Array.from({ length: 85 }, (_, index) => {
      if (index === 0) {
        return {
          id: 'older-mermaid',
          kind: 'assistant',
          text: [
            '旧 Mermaid 图表：',
            '```mermaid',
            'flowchart TD',
            '  Old[旧历史] --> Hidden[首屏隐藏]',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        };
      }
      return {
        id: `recent-${index}`,
        kind: index % 2 === 0 ? 'user' : 'assistant',
        text: `最近 timeline 消息 ${index}`,
        ts: '2026-05-30T00:00:00Z',
      };
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': messages,
      },
    });

    render(<App />);

    expect(await screen.findByText('最近 timeline 消息 84')).toBeInTheDocument();
    expect(screen.queryByText('旧 Mermaid 图表：')).not.toBeInTheDocument();
    expect(mermaid.render).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('button', { name: '显示更早的消息（5 条）' }));

    await waitFor(() => expect(mermaid.render).toHaveBeenCalledTimes(1));
    expect(screen.getByText('旧 Mermaid 图表：')).toBeInTheDocument();
  });

  it('sanitizes rendered mermaid SVG before rendering it as an image data URL', async () => {
    mermaid.render.mockResolvedValueOnce({
      svg: [
        '<svg role="img" aria-label="unsafe mermaid" onload="alert(1)">',
        '<script>alert(1)</script>',
        '<foreignObject><div>unsafe html</div></foreignObject>',
        '<a href="javascript:alert(1)"><text>unsafe link</text></a>',
        '<rect style="background: url( javascript:alert(1) )" />',
        '<text>safe mermaid</text>',
        '</svg>',
      ].join(''),
    });
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-mermaid-sanitized',
          kind: 'assistant',
          text: [
            '```mermaid',
            'flowchart TD',
            '  A-->B',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByLabelText('Mermaid 图表');
    const image = await screen.findByRole('img', { name: 'Mermaid 图表' });
    const svg = decodedSvgDataUrl(image);
    expect(svg).toContain('safe mermaid');
    expect(svg).not.toContain('<script');
    expect(svg).not.toContain('foreignObject');
    expect(svg).not.toContain('onload');
    expect(svg).not.toContain('javascript:alert');
    expect(container.querySelector('script')).toBeNull();
    expect(container.querySelector('foreignObject')).toBeNull();
    expect(container.querySelector('[onload]')).toBeNull();
    expect(container.querySelector('[href^="javascript:"]')).toBeNull();
  });

  it('opens rendered mermaid diagrams in the enlarged preview with an external link', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-mermaid-lightbox',
          kind: 'assistant',
          text: [
            '```mermaid',
            'flowchart TD',
            '  A[开始] --> B[完成]',
            '```',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    fireEvent.click(await screen.findByRole('button', { name: '放大 Mermaid 图表' }));

    const dialog = screen.getByRole('dialog', { name: '图片预览：Mermaid 图表' });
    expect(dialog).toHaveClass('image-lightbox-dialog');
    expect(within(dialog).getByRole('img', { name: 'Mermaid 图表' })).toBeInTheDocument();
    expect(within(dialog).queryByRole('link', { name: '外部打开' })).not.toBeInTheDocument();
  });

  it('keeps assistant output from the thread snapshot when thread message history is stale', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          { id: 'user-stale-history', kind: 'user', text: '我要图片。', ts: '2026-05-30T00:00:00Z' },
          { id: 'assistant-visible-output', kind: 'assistant', text: '这是 AI 输出。', ts: '2026-05-30T00:00:02Z' },
        ],
      },
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [{
        id: 1,
        role: 'user',
        content: '我要图片。',
        createdAt: '2026-05-30T00:00:00Z',
      }],
      total: 1,
    });

    render(<App />);

    expect(await screen.findByText('这是 AI 输出。')).toBeInTheDocument();
  });

  it('hides injected AGENTS instructions from restored chat history', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {},
    });
    backend.getThreadMessages.mockResolvedValue({
      messages: [
        {
          id: 1,
          role: 'user',
          content: [
            '# AGENTS.md instructions for /home/ai01@f666.com/桌面/project/Super-Dolphin',
            '',
            '<INSTRUCTIONS>',
            '# Super Dolphin Agent Agent Context Policy',
            '',
            '## Scope',
            'This file defines how agents should load context.',
            '</INSTRUCTIONS>',
          ].join('\n'),
          createdAt: '2026-05-30T00:00:00Z',
        },
        {
          id: 2,
          role: 'user',
          content: '请修复前端渲染问题',
          createdAt: '2026-05-30T00:01:00Z',
        },
        {
          id: 3,
          role: 'assistant',
          content: '已完成修复。',
          createdAt: '2026-05-30T00:02:00Z',
        },
      ],
      total: 3,
    });

    render(<App />);

    expect(await screen.findByText('请修复前端渲染问题')).toBeInTheDocument();
    expect(screen.getByText('已完成修复。')).toBeInTheDocument();
    expect(screen.queryByText(/AGENTS\.md instructions/)).not.toBeInTheDocument();
    expect(screen.queryByText(/Super Dolphin Agent Agent Context Policy/)).not.toBeInTheDocument();
  });

  it('renders malformed inline markdown fences as readable code blocks', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-inline-fence',
          kind: 'assistant',
          text: [
            '下面是当前仓库结构： ```textSuper-Dolphin/',
            '├── cmd/#可执行入口',
            '├── frontend-app/#当前前端',
            '└── README.md',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByText('下面是当前仓库结构：', {}, { timeout: 5000 });
    await waitFor(() => expect(container.querySelector('.message-markdown pre')).toBeInTheDocument());
    const codeBlock = container.querySelector('.message-markdown pre');
    expect(codeBlock).toHaveTextContent('Super-Dolphin/');
    expect(codeBlock).toHaveTextContent('frontend-app/#当前前端');
    expect(codeBlock).not.toHaveTextContent('```');
    expect(screen.queryByText(/```textSuper-Dolphin/)).not.toBeInTheDocument();
  });

  it('renders common markdown code fence variants without leaking fence metadata', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-common-code-fences',
          kind: 'assistant',
          text: [
            '常见代码块：',
            '',
            '~~~bash',
            'npm run lint',
            '~~~',
            '',
            '```bash title="frontend test"',
            'npm test',
            '```',
            '',
            '```js {1,3}',
            'const value = 1;',
            'console.log(value);',
            '```',
            '',
            '缩进代码：',
            '    pnpm install',
            '    pnpm test',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByText('常见代码块：', {}, { timeout: 5000 });
    await waitFor(() => expect(container.querySelectorAll('.message-markdown pre code')).toHaveLength(4));
    const codeBlocks = Array.from(container.querySelectorAll('.message-markdown pre code'));
    expect(codeBlocks).toHaveLength(4);
    expect(codeBlocks[0]).toHaveTextContent('npm run lint');
    expect(codeBlocks[1]).toHaveTextContent('npm test');
    expect(codeBlocks[1]).not.toHaveTextContent('title="frontend test"');
    expect(codeBlocks[2]).toHaveTextContent('const value = 1;');
    expect(codeBlocks[2]).not.toHaveTextContent('{1,3}');
    expect(codeBlocks[3]).toHaveTextContent('pnpm install');
    expect(codeBlocks[3]).toHaveTextContent('pnpm test');
    expect(screen.queryByText(/~~~bash/)).not.toBeInTheDocument();
  });

  it('renders unfenced terminal transcripts as code blocks instead of markdown quotes', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-terminal-transcript',
          kind: 'assistant',
          text: [
            '执行结果：',
            '$ npm test',
            '> super-dolphin-frontend-app@0.1.0 test',
            '> vitest run',
            'PASS src/App.test.jsx',
          ].join('\n'),
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    const { container } = render(<App />);

    await screen.findByText('执行结果：', {}, { timeout: 5000 });
    await waitFor(() => expect(container.querySelector('.message-markdown pre code')).toBeInTheDocument());
    const codeBlock = container.querySelector('.message-markdown pre code');
    expect(codeBlock).toHaveTextContent('$ npm test');
    expect(codeBlock).toHaveTextContent('> vitest run');
    expect(codeBlock).toHaveTextContent('PASS src/App.test.jsx');
    expect(container.querySelector('.message-markdown blockquote')).toBeNull();
  });

  it('renders generated local image paths from assistant replies as image previews', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-image-path',
          kind: 'assistant',
          text: `已展示。图片文件路径：\`${imagePath}\``,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const image = await screen.findByRole('img', { name: 'ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png' });
    expect(image).toHaveAttribute('src', `/generated-image?path=${encodeURIComponent(imagePath)}`);
    expect(screen.getByRole('button', { name: '放大图片 ig_088272cb55a587f8016a1d3d9660148191951c218f7b0b6c1.png' })).toBeInTheDocument();
    expect(screen.queryByText(imagePath)).not.toBeInTheDocument();
  });

  it('opens assistant image previews in an enlarged lightbox with an external link', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_lightbox.png';
    const routedSrc = `/generated-image?path=${encodeURIComponent(imagePath)}`;
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-image-lightbox',
          kind: 'assistant',
          text: `图片已生成：${imagePath}`,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    await screen.findByRole('button', { name: '放大图片 ig_lightbox.png' });
    fireEvent.click(screen.getByRole('button', { name: '放大图片 ig_lightbox.png' }));

    const dialog = await screen.findByRole('dialog', { name: '图片预览：ig_lightbox.png' });
    expect(dialog).toHaveClass('image-lightbox-dialog');
    expect(within(dialog).getByRole('img', { name: 'ig_lightbox.png' })).toHaveAttribute('src', routedSrc);
    expect(within(dialog).queryByRole('link', { name: '外部打开' })).not.toBeInTheDocument();
  });

  it('shows a readable fallback when a generated image preview cannot load', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_missing.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-missing-image-path',
          kind: 'assistant',
          text: `图片文件路径：\`${imagePath}\``,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    await screen.findByRole('img', { name: 'ig_missing.png' });
    const image = screen.getByRole('img', { name: 'ig_missing.png' });
    fireEvent.error(image);

    const note = await screen.findByRole('note');
    expect(note).toHaveTextContent('图片无法加载');
    expect(note).toHaveTextContent('ig_missing.png');
  });

  it('renders bare generated local image paths from assistant replies as image previews', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_bare_path.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-bare-image-path',
          kind: 'assistant',
          text: `图片已生成：${imagePath}`,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const image = await screen.findByRole('img', { name: 'ig_bare_path.png' });
    expect(image).toHaveAttribute('src', `/generated-image?path=${encodeURIComponent(imagePath)}`);
    expect(screen.queryByText(imagePath)).not.toBeInTheDocument();
  });

  it('renders local image paths in markdown image syntax through the generated image route', async () => {
    const imagePath = '/Users/ai/.codex/generated_images/019e8195-2f77-7aa1-96bd-63f784e87ac4/ig_markdown_path.png';
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{
          id: 'assistant-markdown-image-path',
          kind: 'assistant',
          text: `![生成图](${imagePath})`,
          ts: '2026-05-30T00:00:00Z',
        }],
      },
    });

    render(<App />);

    const image = await screen.findByRole('img', { name: '生成图' });
    expect(image).toHaveAttribute('src', `/generated-image?path=${encodeURIComponent(imagePath)}`);
  });

  it('renders common llm output forms with dedicated formatting', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          {
            id: 'assistant-json',
            kind: 'assistant',
            text: '{"status":"ok","items":[{"name":"edit","count":2}]}',
            ts: '2026-05-30T00:00:00Z',
          },
          {
            id: 'assistant-diff',
            kind: 'assistant',
            text: [
              'diff --git a/src/a.js b/src/a.js',
              '--- a/src/a.js',
              '+++ b/src/a.js',
              '@@ -1 +1 @@',
              '-old',
              '+new',
            ].join('\n'),
            ts: '2026-05-30T00:00:01Z',
          },
          {
            id: 'assistant-log',
            kind: 'assistant',
            text: [
              '[ERROR] api.rpc.failed',
              'Error: boom',
              '    at run (app.js:10:2)',
            ].join('\n'),
            ts: '2026-05-30T00:00:02Z',
          },
          {
            id: 'assistant-config',
            kind: 'assistant',
            text: [
              'provider: codex',
              'model: gpt-5',
              'sandbox: workspace-write',
            ].join('\n'),
            ts: '2026-05-30T00:00:03Z',
          },
        ],
      },
    });

    render(<App />);

    expect(await screen.findByText(/"status": "ok"/)).toBeInTheDocument();
    const jsonBlock = document.querySelector('[data-output-kind="json"]');
    expect(jsonBlock).toBeInTheDocument();
    expect(jsonBlock).toHaveTextContent('"count": 2');

    const diffBlock = document.querySelector('[data-output-kind="diff"]');
    expect(diffBlock).toBeInTheDocument();
    expect(diffBlock.querySelector('.diff-line--deleted')).toHaveTextContent('-old');
    expect(diffBlock.querySelector('.diff-line--added')).toHaveTextContent('+new');
    expect(diffBlock.querySelector('.diff-line--hunk')).toHaveTextContent('@@ -1 +1 @@');

    const logBlock = document.querySelector('[data-output-kind="log"]');
    expect(logBlock).toBeInTheDocument();
    expect(logBlock).toHaveTextContent('[ERROR] api.rpc.failed');
    expect(logBlock).toHaveTextContent('at run (app.js:10:2)');

    const configBlock = document.querySelector('[data-output-kind="config"]');
    expect(configBlock).toBeInTheDocument();
    expect(configBlock).toHaveTextContent('sandbox: workspace-write');
  });

  it('[regression] renders streaming code blocks without showing opening code fences', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [
          {
            id: 'assistant-streaming-log',
            kind: 'assistant',
            text: [
              '```log',
              '[INFO] starting server...',
              '[INFO] server listening on port 8080',
            ].join('\n'),
            ts: '2026-05-30T00:00:00Z',
          },
        ],
      },
    });

    render(<App />);

    expect(await screen.findByText(/\[INFO\] starting server\.\.\./)).toBeInTheDocument();
    const logBlock = document.querySelector('[data-output-kind="log"]');
    expect(logBlock).toBeInTheDocument();
    expect(logBlock).toHaveTextContent('[INFO] starting server...');
    expect(logBlock).not.toHaveTextContent('```log');
  });

  it('derives runtime code-change metrics from the backend diff for the selected thread', async () => {
    backend.getThreadState.mockResolvedValue({
      activeThreadId: 'thread-1',
      timelinesByThread: {
        'thread-1': [{ id: 'assistant-1', kind: 'assistant', text: '来自后端的消息', ts: '2026-05-30T00:00:00Z' }],
      },
      diffTextByThread: {
        'thread-1': [
          'diff --git a/src/a.js b/src/a.js',
          '--- a/src/a.js',
          '+++ b/src/a.js',
          '@@ -1,2 +1,3 @@',
          ' keep',
          '-old',
          '+new',
          '+extra',
          'diff --git a/src/b.js b/src/b.js',
          '--- a/src/b.js',
          '+++ b/src/b.js',
          '@@ -4,2 +4,2 @@',
          '-removed',
          '+added',
        ].join('\n'),
      },
    });

    render(<App />);
    await waitForBackendThreadHeading();

    act(() => {
      bridgeCallback({
        type: 'bridge.call/failed',
        payload: { method: 'turn/start', threadId: 'thread-1', error: 'backend failed' },
      });
    });
    fireEvent.click(screen.getByRole('button', { name: '显示侧边栏' }));

    const fileCountMetric = screen.getByLabelText('代码变更文件数：2 个');
    const changedLineMetric = screen.getByLabelText('代码变更行数：5 行');
    expect(fileCountMetric).toHaveTextContent('2');
    expect(fileCountMetric.querySelector('svg')).toHaveClass('lucide-file-text');
    expect(changedLineMetric).toHaveTextContent('5');
    expect(changedLineMetric.querySelector('svg')).toHaveClass('lucide-code-xml');
    expect(screen.getByLabelText('代码新增行数：+3 行')).toHaveTextContent('+3');
    expect(screen.getByLabelText('代码删除行数：-2 行')).toHaveTextContent('-2');
    expect(screen.getByLabelText('代码新增行数：+3 行')).not.toHaveTextContent('+0');
    expect(screen.getByLabelText('代码删除行数：-2 行')).not.toHaveTextContent('-1');
  });
