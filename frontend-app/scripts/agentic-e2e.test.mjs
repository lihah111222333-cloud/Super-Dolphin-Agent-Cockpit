import { describe, expect, it } from 'vitest';
import { chromium } from 'playwright';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';

import { agenticE2EConfig, collectPageFacts, mergeDiscoveredFlows, normalizeDOMSummaryItem, readinessForAction, writeFailureEvidence, writeFinalEvidence } from './agentic-e2e.mjs';
import { prepareAgenticE2ESandbox, snapshotAgenticE2ESandbox } from './agentic-e2e-sandbox.mjs';
import {
  BLOCKED_ACTION_KEYWORDS,
  businessActionsFromDOMSummary,
  discoverBusinessFlows,
  safetyForAction,
} from './agentic-e2e-discovery.mjs';
import { AGENTIC_GOAL_IDS, decideNextAction, normalizeGoal } from './agentic-e2e-planner.mjs';
import { renderDiscoveryMarkdown, summarizeDiscovery } from './agentic-e2e-reporter.mjs';
import { assertAgenticE2EMockWailsClean, installAgenticE2EMockWails, readAgenticE2EMockWailsState } from './agentic-e2e-wails-mock.mjs';

describe('agentic e2e planner', () => {
  it('starts by opening the app root when the page is blank', () => {
    expect(decideNextAction({ url: 'about:blank' })).toEqual(expect.objectContaining({
      type: 'goto',
      path: '/',
    }));
  });

  it('fills the composer once the chat page is visible', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: '',
    }, { composerText: 'probe text' });

    expect(action).toEqual(expect.objectContaining({
      type: 'fill',
      value: 'probe text',
      target: { type: 'testId', value: 'composer-input' },
    }));
  });

  it('navigates to observability after filling the composer', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: 'probe text',
      chatActionsMenuVisible: false,
      runtimePanelVisible: false,
    }, { composerText: 'probe text' });

    expect(action.type).toBe('click');
    expect(action.target).toEqual({
      type: 'nestedRole',
      parentTestId: 'sidebar-secondary-nav',
      role: 'button',
      name: '链路追踪',
    });
  });

  it('returns to the chat route without clicking the new conversation action', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      hasChatPage: false,
    });

    expect(action).toEqual(expect.objectContaining({
      type: 'goto',
      path: '/',
      expectRoute: '/',
      reason: expect.stringContaining('without invoking new conversation'),
    }));
  });

  it('finishes only after observability logs are visible', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/observability',
      hasFrontendApp: true,
      observabilityPageVisible: true,
      recentLogsVisible: true,
    }, { id: 'observability-latest-logs', composerText: 'probe text' }).type).toBe('done');
  });

  it('queries latest observability logs for the observability goal', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/observability',
      hasFrontendApp: true,
      observabilityPageVisible: true,
      recentLogsVisible: false,
    }, { id: 'observability-latest-logs' });

    expect(action).toEqual(expect.objectContaining({
      type: 'click',
      target: { type: 'role', role: 'button', name: '查询最新日志' },
    }));
  });

  it('opens stable business navigation goals without filling the composer', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: '',
    }, { id: 'plugins-skills-open' });

    expect(action).toEqual(expect.objectContaining({
      type: 'click',
      expectRoute: '/skills',
      target: {
        type: 'nestedRole',
        parentTestId: 'sidebar-nav',
        role: 'button',
        name: '插件与技能',
      },
    }));
  });

  it('finishes a stable open goal once its route is active', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
    }, { id: 'settings-open' })).toEqual(expect.objectContaining({
      type: 'done',
      reason: expect.stringContaining('settings-open'),
    }));
  });

  it('finishes chat-composer after the composer text is filled', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: 'probe text',
    }, { id: 'chat-composer', composerText: 'probe text' })).toEqual(expect.objectContaining({
      type: 'done',
      reason: expect.stringContaining('chat-composer'),
    }));
  });

  it('fails fast on console errors', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      consoleErrors: ['boom'],
    });

    expect(action.type).toBe('fail');
    expect(action.reason).toContain('console errors detected');
  });

  it('clicks the real send button for the mocked chat send danger goal', () => {
    const action = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: 'probe text',
      mockWailsCallMethods: [],
    }, { id: 'chat-send-mocked', composerText: 'probe text' });

    expect(action).toEqual(expect.objectContaining({
      type: 'click',
      target: { type: 'role', role: 'button', name: '发送消息', exact: true },
    }));
  });

  it('finishes the mocked chat send goal only after thread and turn RPCs are observed', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      composerVisible: true,
      composerValue: '',
      mockWailsCallMethods: ['thread/start', 'turn/start'],
    }, { id: 'chat-send-mocked', composerText: 'probe text' })).toEqual(expect.objectContaining({
      type: 'done',
      reason: expect.stringContaining('chat-send-mocked'),
    }));
  });

  it('drives the project picker through the real sidebar add-project button', () => {
    const addProject = decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      mockWailsCallMethods: [],
    }, { id: 'project-add-sandbox' });
    expect(addProject).toEqual(expect.objectContaining({
      type: 'click',
      target: { type: 'nestedRole', parentTestId: 'app-sidebar', role: 'button', name: '添加项目目录' },
    }));
  });

  it('finishes the sandbox project add goal after selection and project RPCs', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      mockWailsCallMethods: ['ui/selectProjectDir', 'ui/projects/add'],
    }, { id: 'project-add-sandbox' })).toEqual(expect.objectContaining({
      type: 'done',
      reason: expect.stringContaining('project-add-sandbox'),
    }));
  });

  it('clicks the real composer add-file action and waits for the file picker RPC', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      mockWailsCallMethods: [],
    }, { id: 'file-attach-sandbox' })).toEqual(expect.objectContaining({
      type: 'click',
      target: { type: 'role', role: 'button', name: '添加文件', exact: true },
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/',
      hasFrontendApp: true,
      hasChatPage: true,
      attachmentCount: 1,
      mockWailsCallMethods: ['ui/selectFiles'],
    }, { id: 'file-attach-sandbox' })).toEqual(expect.objectContaining({
      type: 'done',
      reason: expect.stringContaining('file-attach-sandbox'),
    }));
  });

  it('saves the settings video key through the real settings form', () => {
    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsApiKeyVisible: true,
      settingsApiKeyValue: '',
    }, { id: 'settings-video-key-save-mocked' })).toEqual(expect.objectContaining({
      type: 'fill',
      target: { type: 'css', value: '#settings-sf-key' },
      value: 'agentic-e2e-video-key',
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      settingsApiKeyVisible: true,
      settingsApiKeyValue: 'agentic-e2e-video-key',
      mockWailsCallMethods: [],
    }, { id: 'settings-video-key-save-mocked' })).toEqual(expect.objectContaining({
      type: 'click',
      target: { type: 'nestedRole', parentTestId: 'settings-video-card', role: 'button', name: '保存' },
    }));

    expect(decideNextAction({
      url: 'http://127.0.0.1:5176/settings',
      hasFrontendApp: true,
      settingsPageVisible: true,
      mockWailsCallMethods: ['ui/video/setApiKey'],
    }, { id: 'settings-video-key-save-mocked' })).toEqual(expect.objectContaining({
      type: 'done',
      reason: expect.stringContaining('settings-video-key-save-mocked'),
    }));
  });
});

describe('agentic e2e business discovery', () => {
  it('discovers sidebar entries and safe query actions from DOM summary', () => {
    const flows = discoverBusinessFlows({
      url: 'http://127.0.0.1:5176/',
      title: '燧元',
      domSummary: [
        { tag: 'button', role: '', testId: '', ariaLabel: '链路追踪', text: '', disabled: false, sourceTestId: 'sidebar-secondary-nav' },
        { tag: 'button', role: '', testId: '', ariaLabel: 'Settings', text: '', disabled: false, sourceTestId: 'app-sidebar' },
        { tag: 'button', role: '', testId: '', ariaLabel: '', text: '查询最新日志', disabled: false },
      ],
    });

    expect(flows.map((flow) => flow.entry.label)).toContain('链路追踪');
    expect(flows.map((flow) => flow.entry.label)).toContain('Settings');
    expect(flows.flatMap((flow) => flow.actions).some((action) => action.label === '查询最新日志' && action.safety === 'allowed')).toBe(true);
  });

  it('discovers known navigation entries from baseline DOM summary shape', () => {
    const flows = discoverBusinessFlows({
      url: 'http://127.0.0.1:5176/',
      title: '燧元',
      domSummary: [
        { tag: 'button', role: '', testId: '', ariaLabel: '链路追踪', text: '', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: 'Settings', text: '', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: '', text: '查询最新日志', disabled: false },
      ],
    });

    expect(flows.map((flow) => flow.entry)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: '链路追踪', source: 'visible-navigation' }),
      expect.objectContaining({ label: 'Settings', source: 'visible-navigation' }),
    ]));
  });

  it('discovers Chinese navigation entries from aria labels and visible text', () => {
    const flows = discoverBusinessFlows({
      url: 'http://127.0.0.1:5176/',
      title: '燧元',
      domSummary: [
        { tag: 'button', role: '', testId: '', ariaLabel: '插件与技能', text: '插件', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: '设置', text: '设置', disabled: false },
        { tag: 'button', role: '', testId: '', ariaLabel: '链路追踪', text: '', disabled: false },
      ],
    });
    const labels = flows.map((flow) => flow.entry.label);

    expect(labels.some((label) => label === '插件与技能' || label === '插件')).toBe(true);
    expect(labels).toContain('设置');
    expect(labels).toContain('链路追踪');
    expect(flows).toEqual(expect.arrayContaining([
      expect.objectContaining({ entry: expect.objectContaining({ label: '插件与技能', targetRoute: '/skills' }) }),
      expect.objectContaining({ entry: expect.objectContaining({ label: '设置', targetRoute: '/settings' }) }),
      expect.objectContaining({ entry: expect.objectContaining({ label: '链路追踪', targetRoute: '/observability' }) }),
    ]));
  });

  it('marks known navigation buttons as safe route changes without allowing shell controls', () => {
    const actions = businessActionsFromDOMSummary([
      { tag: 'button', role: '', testId: '', ariaLabel: '插件与技能', text: '插件', disabled: false, sourceTestId: 'sidebar-nav' },
      { tag: 'button', role: '', testId: '', ariaLabel: '自动化', text: '自动化', disabled: false, sourceTestId: 'sidebar-nav' },
      { tag: 'button', role: '', testId: '', ariaLabel: '设置', text: '设置', disabled: false, sourceTestId: 'app-sidebar' },
      { tag: 'button', role: '', testId: '', ariaLabel: '新对话', text: '', disabled: false, sourceTestId: 'app-sidebar' },
      { tag: 'button', role: '', testId: '', ariaLabel: '切换到 English', text: '', disabled: false, sourceTestId: 'app-sidebar' },
      { tag: 'button', role: '', testId: '', ariaLabel: '发送消息', text: '', disabled: false, sourceTestId: 'sidebar-nav' },
    ]);

    expect(actions).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: '插件与技能', safety: 'allowed', reason: 'navigation entry', targetRoute: '/skills' }),
      expect.objectContaining({ label: '自动化', safety: 'allowed', reason: 'navigation entry', targetRoute: '/dags' }),
      expect.objectContaining({ label: '设置', safety: 'allowed', reason: 'navigation entry', targetRoute: '/settings' }),
      expect.objectContaining({ label: '新对话', safety: 'blocked', reason: 'action is not recognized as read-only', targetRoute: '/' }),
      expect.objectContaining({ label: '切换到 English', safety: 'blocked', reason: 'action is not recognized as read-only' }),
      expect.objectContaining({ label: '发送消息', safety: 'blocked', reason: 'mutating or provider action keyword: 发送' }),
    ]));
  });

  it('does not emit disabled controls as candidate actions', () => {
    const actions = businessActionsFromDOMSummary([
      { tag: 'button', role: '', testId: '', ariaLabel: '', text: '查询最新日志', disabled: true },
    ]);

    expect(actions.some((action) => action.label === '查询最新日志')).toBe(false);
  });

  it('blocks mutating and provider-turn actions', () => {
    expect(BLOCKED_ACTION_KEYWORDS).toContain('删除');
    expect(safetyForAction({ label: '发送', type: 'click' })).toEqual(expect.objectContaining({
      safety: 'blocked',
      reason: expect.stringContaining('mutating or provider action'),
    }));
    expect(safetyForAction({ label: '查询最新日志', type: 'click' })).toEqual(expect.objectContaining({
      safety: 'allowed',
    }));
  });
});

describe('agentic e2e readiness', () => {
  it('waits for observability page after clicking its sidebar entry', () => {
    expect(readinessForAction({
      type: 'click',
      target: { type: 'nestedRole', parentTestId: 'sidebar-secondary-nav', role: 'button', name: '链路追踪' },
    })).toEqual({ type: 'testId', value: 'observability-page' });
  });

  it('waits for recent logs after querying observability', () => {
    expect(readinessForAction({
      type: 'click',
      target: { type: 'role', role: 'button', name: '查询最新日志' },
    })).toEqual({ type: 'testId', value: 'observability-recent-logs' });
  });
});

describe('agentic e2e discovery aggregation', () => {
  it('merges discovered flows by id without losing blocked actions', () => {
    const merged = mergeDiscoveredFlows([
      { id: 'flow-a', actions: [{ type: 'click', label: '查询', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }] },
    ], [
      { id: 'flow-a', actions: [{ type: 'click', label: '删除', safety: 'blocked', reason: 'mutating or provider action keyword: 删除' }], result: { status: 'candidate', summary: 'second sample' } },
      { id: 'flow-b', actions: [], result: { status: 'candidate', summary: 'new flow' } },
    ]);

    expect(merged).toHaveLength(2);
    expect(merged.find((flow) => flow.id === 'flow-a').actions).toHaveLength(2);
    expect(merged.find((flow) => flow.id === 'flow-b').result.summary).toBe('new flow');
  });

  it('keeps target route separate from the latest observed page when merging repeated flows', () => {
    const merged = mergeDiscoveredFlows([
      {
        id: 'visible-sidebar-nav-插件与技能',
        entry: { route: '/', label: '插件与技能', source: 'sidebar-nav', targetRoute: '/skills' },
        page: { route: '/' },
        actions: [{ type: 'click', label: '插件与技能', safety: 'allowed', reason: 'navigation entry' }],
      },
    ], [
      {
        id: 'visible-sidebar-nav-插件与技能',
        entry: { route: '/observability', label: '插件与技能', source: 'sidebar-nav', targetRoute: '/skills' },
        page: { route: '/observability' },
        actions: [{ type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
      },
    ]);

    expect(merged).toHaveLength(1);
    expect(merged[0].entry).toEqual(expect.objectContaining({
      route: '/',
      targetRoute: '/skills',
    }));
    expect(merged[0].page.route).toBe('/observability');
  });

  it('fails fast for discovered flows without non-empty ids', () => {
    expect(() => mergeDiscoveredFlows([{ id: '', actions: [] }], [])).toThrow(/invalid discovered flow/);
    expect(() => mergeDiscoveredFlows([], [{ actions: [] }])).toThrow(/invalid discovered flow/);
  });

  it('fails fast for discovered actions without semantic fields', () => {
    expect(() => mergeDiscoveredFlows([
      { id: 'flow-a', actions: [{ label: '查询', safety: 'allowed' }] },
    ], [])).toThrow(/invalid discovered action/);
  });
});

describe('agentic e2e evidence writing', () => {
  const validDiscoveryFlows = [{
    id: 'flow-a',
    actions: [{ type: 'click', label: '查询', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
  }];

  it('preserves the original failure when discovery report validation fails', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await expect(writeFailureEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        new Error('original runner failure'),
        [{ id: 'flow-a', actions: [null] }],
      )).rejects.toThrow(/original runner failure/);

      const result = JSON.parse(await readFile(path.join(outputDir, 'result.json'), 'utf8'));
      expect(result.success).toBe(false);
      expect(result.error).toBe('original runner failure');
      expect(result.discovery.error).toMatch(/invalid discovery action/);
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });

  it('records discovery report write errors without replacing the original failure', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await mkdir(path.join(outputDir, 'business-flow-discovery.json'));

      await expect(writeFailureEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        new Error('original runner failure'),
        validDiscoveryFlows,
      )).rejects.toThrow(/original runner failure/);

      const result = JSON.parse(await readFile(path.join(outputDir, 'result.json'), 'utf8'));
      expect(result.error).toBe('original runner failure');
      expect(result.discoveryReportError).toMatch(/EISDIR|illegal operation|directory/);
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });

  it('keeps success evidence fail-fast for malformed discovery flows', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await expect(writeFinalEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        [{ id: 'flow-a', actions: [null] }],
      )).rejects.toThrow(/invalid discovery action/);
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });

  it('keeps success evidence fail-fast for discovery report write errors after result is written', async () => {
    const outputDir = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-'));
    const page = { screenshot: async () => {} };
    try {
      await mkdir(path.join(outputDir, 'business-flow-discovery.json'));

      await expect(writeFinalEvidence(
        outputDir,
        page,
        [],
        [],
        [],
        validDiscoveryFlows,
      )).rejects.toThrow(/EISDIR|illegal operation on a directory|is a directory/);

      const result = JSON.parse(await readFile(path.join(outputDir, 'result.json'), 'utf8'));
      expect(result.success).toBe(true);
      expect(result.discovery).toEqual({
        totalFlows: 1,
        allowedActions: 1,
        blockedActions: 0,
        uniqueAllowedActions: 1,
        uniqueBlockedActions: 0,
        reviewReadyFlows: 1,
        stableGoalCandidates: 0,
        shellControlFlows: 0,
        contextualFlows: 0,
      });
    }
    finally {
      await rm(outputDir, { recursive: true, force: true });
    }
  });
});

describe('agentic e2e discovery report', () => {
  it('summarizes empty discovery by default', () => {
    expect(summarizeDiscovery()).toEqual({
      totalFlows: 0,
      allowedActions: 0,
      blockedActions: 0,
      uniqueAllowedActions: 0,
      uniqueBlockedActions: 0,
      reviewReadyFlows: 0,
      stableGoalCandidates: 0,
      shellControlFlows: 0,
      contextualFlows: 0,
    });
  });

  it('summarizes discovered, executed, and blocked flows', () => {
    const summary = summarizeDiscovery({
      flows: [{
        id: 'visible-sidebar-secondary-nav-链路追踪',
        entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [
          { type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' },
          { type: 'click', label: '删除日志', safety: 'blocked', reason: 'mutating or provider action keyword: 删除' },
        ],
        result: { status: 'discovered', summary: 'Recent log table became visible' },
      }],
    });

    expect(summary.totalFlows).toBe(1);
    expect(summary.allowedActions).toBe(1);
    expect(summary.blockedActions).toBe(1);
    expect(summary.uniqueAllowedActions).toBe(1);
    expect(summary.uniqueBlockedActions).toBe(1);
    expect(summary.reviewReadyFlows).toBe(1);
    expect(summary.stableGoalCandidates).toBe(1);
  });

  it('separates review-ready entries from shell and contextual controls', () => {
    const summary = summarizeDiscovery({
      flows: [
        {
          id: 'visible-sidebar-secondary-nav-链路追踪',
          entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
          actions: [],
        },
        {
          id: 'visible-app-sidebar-切换到-english',
          entry: { route: '/', label: '切换到 English', source: 'app-sidebar' },
          actions: [],
        },
        {
          id: 'visible-app-sidebar-新对话-agentic-e2e-harness',
          entry: { route: '/', label: '新对话 agentic-e2e-harness', source: 'app-sidebar' },
          actions: [],
        },
      ],
    });

    expect(summary.reviewReadyFlows).toBe(1);
    expect(summary.stableGoalCandidates).toBe(1);
    expect(summary.shellControlFlows).toBe(1);
    expect(summary.contextualFlows).toBe(1);
  });

  it('deduplicates dynamic trace actions in discovery summaries', () => {
    const summary = summarizeDiscovery({
      flows: [{
        id: 'visible-sidebar-secondary-nav-链路追踪',
        entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav' },
        actions: [
          { type: 'click', label: '打开 Trace 8a7a506fc528263963f9234b0f45d581', safety: 'allowed', reason: 'read-oriented action keyword: 打开' },
          { type: 'click', label: '打开 Trace f4af9f8d6d21b49ed9cf0a62cf9d2c81', safety: 'allowed', reason: 'read-oriented action keyword: 打开' },
        ],
      }],
    });

    expect(summary.allowedActions).toBe(2);
    expect(summary.uniqueAllowedActions).toBe(1);
  });

  it('renders a human-readable markdown report', () => {
    const markdown = renderDiscoveryMarkdown({
      flows: [{
        id: 'visible-sidebar-secondary-nav-链路追踪',
        entry: { route: '/', label: '链路追踪', source: 'sidebar-secondary-nav', targetRoute: '/observability' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [{ type: 'click', label: '查询最新日志', safety: 'allowed', reason: 'read-oriented action keyword: 查询' }],
        result: { status: 'discovered', summary: 'Recent log table became visible' },
      }, {
        id: 'visible-sidebar-nav-自动化',
        entry: { route: '/', label: '自动化', source: 'sidebar-nav', targetRoute: '/dags' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [{ type: 'click', label: '自动化', safety: 'allowed', reason: 'navigation entry', targetRoute: '/dags' }],
        result: { status: 'candidate', summary: 'Discovered from visible page structure' },
      }, {
        id: 'visible-sidebar-nav-插件与技能',
        entry: { route: '/', label: '插件与技能', source: 'sidebar-nav', targetRoute: '/skills' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [{ type: 'click', label: '插件与技能', safety: 'allowed', reason: 'navigation entry', targetRoute: '/skills' }],
        result: { status: 'candidate', summary: 'Discovered from visible page structure' },
      }, {
        id: 'visible-sidebar-nav-提示词',
        entry: { route: '/', label: '提示词', source: 'sidebar-nav', targetRoute: '/prompts' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [{ type: 'click', label: '提示词', safety: 'allowed', reason: 'navigation entry', targetRoute: '/prompts' }],
        result: { status: 'candidate', summary: 'Discovered from visible page structure' },
      }, {
        id: 'visible-sidebar-nav-共享文件',
        entry: { route: '/', label: '共享文件', source: 'sidebar-nav', targetRoute: '/files' },
        page: { route: '/observability', heading: '链路追踪', testIds: ['observability-page'] },
        actions: [{ type: 'click', label: '共享文件', safety: 'allowed', reason: 'navigation entry', targetRoute: '/files' }],
        result: { status: 'candidate', summary: 'Discovered from visible page structure' },
      }],
    });

    expect(markdown).toContain('# Agentic E2E Business Flow Discovery');
    expect(markdown).toContain('- Total flows: 5');
    expect(markdown).toContain('- Stable goal candidates: 5');
    expect(markdown).toContain('| Entry | Category | Source | Target Route | Observed Page | Result | Suggested Goal |');
    expect(markdown).toContain('链路追踪');
    expect(markdown).toContain('| 自动化 | business-candidate | sidebar-nav | /dags | /observability | candidate - Discovered from visible page structure | automation-open |');
    expect(markdown).toContain('| 插件与技能 | business-candidate | sidebar-nav | /skills | /observability | candidate - Discovered from visible page structure | plugins-skills-open |');
    expect(markdown).toContain('| 提示词 | business-candidate | sidebar-nav | /prompts | /observability | candidate - Discovered from visible page structure | prompts-open |');
    expect(markdown).toContain('| 共享文件 | business-candidate | sidebar-nav | /files | /observability | candidate - Discovered from visible page structure | shared-files-open |');
    expect(markdown).toContain('Stable Goal Candidates');
    expect(markdown).toContain('observability-latest-logs');
    expect(markdown).toContain('查询最新日志');
  });

  it('escapes markdown table cells in action labels and reasons', () => {
    const markdown = renderDiscoveryMarkdown({
      flows: [{
        id: 'x',
        actions: [{
          type: 'click',
          label: '查询|最新\n日志',
          safety: 'allowed',
          reason: 'read|oriented\nreason',
        }],
      }],
    });

    expect(markdown).toContain('| click | 查询\\|最新 日志 | 1 | read\\|oriented reason |');
  });

  it('fails fast with context for malformed discovery flows', () => {
    expect(() => summarizeDiscovery({ flows: [null] })).toThrow(/invalid discovery flow/);
    expect(() => renderDiscoveryMarkdown({ flows: [null] })).toThrow(/invalid discovery flow/);
  });

  it('fails fast with context for malformed discovery actions', () => {
    expect(() => summarizeDiscovery({
      flows: [{ id: 'x', actions: [null] }],
    })).toThrow(/invalid discovery action/);
  });
});

describe('agentic e2e DOM facts', () => {
  it('normalizes discovery fields from DOM summary items', () => {
    expect(normalizeDOMSummaryItem({
      tag: 'button',
      role: '',
      testId: '',
      parentTestId: 'sidebar-secondary-nav',
      ariaLabel: '链路追踪',
      text: '',
      disabled: false,
    })).toEqual({
      tag: 'button',
      role: '',
      testId: '',
      parentTestId: 'sidebar-secondary-nav',
      sourceTestId: 'sidebar-secondary-nav',
      ariaLabel: '链路追踪',
      text: '',
      disabled: false,
    });
  });

  it('collects and normalizes DOM summary items from a browser page', async () => {
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      await page.setContent(`
        <main data-testid="frontend-app">
          <nav data-testid="sidebar-secondary-nav">
            <button aria-label="链路追踪"></button>
          </nav>
          <h1>观测面板</h1>
          <label>
            筛选
            <select aria-label="日志级别">
              <option>info</option>
            </select>
          </label>
        </main>
      `);

      const facts = await collectPageFacts(page, []);
      const button = facts.domSummary.find((item) => item.tag === 'button' && item.ariaLabel === '链路追踪');

      expect(button).toEqual(expect.objectContaining({
        parentTestId: 'sidebar-secondary-nav',
        sourceTestId: 'sidebar-secondary-nav',
      }));
      expect(facts.domSummary).toEqual(expect.arrayContaining([
        expect.objectContaining({ tag: 'h1', text: '观测面板' }),
        expect.objectContaining({ tag: 'select', ariaLabel: '日志级别' }),
      ]));
    }
    finally {
      await browser.close();
    }
  });
});

describe('agentic e2e config', () => {
  it('builds a bounded default config', () => {
    const config = agenticE2EConfig({
      SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'probe run',
    }, '/repo/app');

    expect(config.baseURL).toBe('http://127.0.0.1:5176/');
    expect(config.outputDir).toBe('/repo/app/.tmp/agentic-e2e/probe-run/frontend_navigation_probe');
    expect(config.runID).toBe('probe-run');
    expect(config.sandbox).toEqual({
      rootDir: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run',
      homeDir: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/home',
      projectDir: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/project',
      uploadFile: '/repo/app/.tmp/agentic-e2e/sandbox/probe-run/project/files/sample.txt',
    });
    expect(config.maxSteps).toBe(12);
    expect(config.headless).toBe(true);
    expect(config.mockWails).toBe(false);
  });

  it('normalizes explicit goal text', () => {
    expect(normalizeGoal({ id: ' chat-composer ', composerText: ' hello ' })).toEqual(expect.objectContaining({
      id: 'chat-composer',
      composerText: 'hello',
    }));
  });

  it('exposes the stable goal runner candidates', () => {
    expect(AGENTIC_GOAL_IDS).toEqual(expect.arrayContaining([
      'chat-composer',
      'observability-latest-logs',
      'plugins-skills-open',
      'automation-open',
      'prompts-open',
      'shared-files-open',
      'memory-open',
      'settings-open',
      'chat-send-mocked',
      'project-add-sandbox',
      'file-attach-sandbox',
      'settings-video-key-save-mocked',
    ]));
  });

  it('fails fast for unsupported goals', () => {
    expect(() => normalizeGoal({ id: 'missing-goal' })).toThrow(/unsupported agentic e2e goal/);
  });

  it('uses command line goal selection and goal-scoped output directories', () => {
    const config = agenticE2EConfig({
      SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'probe run',
      SUPER_DOLPHIN_AGENTIC_E2E_GOAL: 'plugins-skills-open',
    }, '/repo/app', ['--goal=memory-open']);

    expect(config.goal.id).toBe('memory-open');
    expect(config.outputDir).toBe('/repo/app/.tmp/agentic-e2e/probe-run/memory-open');
  });

  it('enables strict mock Wails from env or command line', () => {
    expect(agenticE2EConfig({
      SUPER_DOLPHIN_AGENTIC_E2E_MOCK_WAILS: '1',
    }, '/repo/app').mockWails).toBe(true);
    expect(agenticE2EConfig({}, '/repo/app', ['--mock-wails']).mockWails).toBe(true);
  });

  it('exposes the opt-in desktop wide e2e script without changing desktop smoke', async () => {
    const pkg = JSON.parse(await readFile(path.join(process.cwd(), 'package.json'), 'utf8'));
    expect(pkg.scripts?.['test:e2e:desktop-wide']).toBe('playwright test --config playwright.desktop-wide.config.js');
    expect(pkg.scripts?.['smoke:desktop:ux']).toBe('node scripts/desktop-ux-smoke.mjs');
  });

  it('keeps desktop wide playwright isolated to its spec and tmp output', async () => {
    const config = await readFile(path.join(process.cwd(), 'playwright.desktop-wide.config.js'), 'utf8');
    expect(config).toContain("testMatch: 'desktop-wide.spec.js'");
    expect(config).toContain("outputDir: '../.tmp/playwright-desktop-wide'");
    expect(config).toContain("name: 'desktop-1440'");
    expect(config).toContain("name: 'desktop-1600'");
  });

  it('fails fast for unsupported command line options', () => {
    expect(() => agenticE2EConfig({}, '/repo/app', ['--unknown'])).toThrow(/unsupported agentic e2e option/);
  });
});

describe('agentic e2e sandbox fixture', () => {
  it('creates a bounded test project and returns a deterministic file snapshot', async () => {
    const repoRoot = await mkdtemp(path.join(tmpdir(), 'agentic-e2e-repo-'));
    try {
      const config = agenticE2EConfig({
        SUPER_DOLPHIN_AGENTIC_E2E_RUN_ID: 'sandbox run',
      }, repoRoot);

      await prepareAgenticE2ESandbox(config);
      const readme = await readFile(path.join(config.sandbox.projectDir, 'README.md'), 'utf8');
      const skill = await readFile(path.join(config.sandbox.projectDir, '.agents/skills/e2e-fixture/SKILL.md'), 'utf8');
      const snapshot = await snapshotAgenticE2ESandbox(config);

      expect(readme).toContain('Agentic E2E Sandbox Project');
      expect(skill).toContain('agentic-e2e-fixture');
      expect(snapshot.files).toEqual(expect.arrayContaining([
        '.agents/skills/e2e-fixture/SKILL.md',
        'README.md',
        'docs/note.md',
        'files/sample.txt',
        'src/app.js',
      ]));
    }
    finally {
      await rm(repoRoot, { recursive: true, force: true });
    }
  });
});

describe('agentic e2e strict Wails mock', () => {
  it('responds to known RPCs and records unhandled RPCs', async () => {
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      const sandbox = sandboxFixture('/tmp/agentic-e2e-known');
      await installAgenticE2EMockWails(page, { sandbox });
      await page.goto('data:text/html,<main>mock</main>');

      const known = await callMockWailsRPC(page, 'config/read', {});
      expect(known.result).toEqual({ cwd: sandbox.projectDir });

      const unknown = await callMockWailsRPC(page, 'missing/method', {});
      expect(unknown.error.message).toMatch(/unhandled agentic e2e mock RPC/);

      const state = await readAgenticE2EMockWailsState(page);
      expect(state.calls.map((call) => call.method)).toEqual(['config/read', 'missing/method']);
      expect(state.unhandledRPC).toEqual(['missing/method']);
    }
    finally {
      await browser.close();
    }
  });

  it('returns sandbox paths for project and file pickers and fails on out-of-sandbox project paths', async () => {
    const browser = await chromium.launch({ headless: true });
    try {
      const page = await browser.newPage();
      const sandbox = sandboxFixture('/tmp/agentic-e2e-boundary');
      await installAgenticE2EMockWails(page, { sandbox });
      await page.goto('data:text/html,<main>mock</main>');

      await expect(callMockWailsRPC(page, 'ui/selectProjectDir', { defaultPath: sandbox.projectDir }))
        .resolves.toEqual(expect.objectContaining({ result: { path: sandbox.projectDir } }));
      await expect(callMockWailsRPC(page, 'ui/selectFiles', {}))
        .resolves.toEqual(expect.objectContaining({ result: { paths: [sandbox.uploadFile] } }));
      const outside = await callMockWailsRPC(page, 'ui/projects/add', { cwd: sandbox.projectDir, path: '/tmp/outside-project' });
      expect(outside.error.message).toMatch(/outside sandbox/);

      const state = await readAgenticE2EMockWailsState(page);
      expect(state.sandboxViolations).toEqual([expect.objectContaining({
        method: 'ui/projects/add',
        path: '/tmp/outside-project',
      })]);
      expect(() => assertAgenticE2EMockWailsClean(state)).toThrow(/outside sandbox/);
    }
    finally {
      await browser.close();
    }
  });
});

async function callMockWailsRPC(page, method, params) {
  return page.evaluate(({ method: rpcMethod, params: rpcParams }) => new Promise((resolve, reject) => {
    const socket = new WebSocket('ws://127.0.0.1/wails/ws');
    socket.onerror = () => reject(new Error('mock socket failed'));
    socket.onopen = () => {
      socket.send(JSON.stringify({ jsonrpc: '2.0', id: 1, method: rpcMethod, params: rpcParams }));
    };
    socket.onmessage = (event) => {
      socket.close();
      resolve(JSON.parse(event.data));
    };
  }), { method, params });
}

function sandboxFixture(rootDir) {
  return {
    rootDir,
    homeDir: `${rootDir}/home`,
    projectDir: `${rootDir}/project`,
    uploadFile: `${rootDir}/project/files/sample.txt`,
  };
}
